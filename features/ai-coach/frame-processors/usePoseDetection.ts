import { useEffect, useState } from 'react';
import { Platform } from 'react-native';
import { useTensorflowModel } from 'react-native-fast-tflite';
import { useSharedValue } from 'react-native-reanimated';
import { useFrameProcessor } from 'react-native-vision-camera';
import { runAtTargetFps } from 'react-native-vision-camera';
import { useRunOnJS } from 'react-native-worklets-core';
import { useResizePlugin } from 'vision-camera-resize-plugin';

// Default: 2fps — sufficient for activity detection (person exercising vs idle)
// while minimizing battery drain. The pose model's primary production role is
// detecting whether someone is actively working out, not real-time form analysis.
// Higher FPS (e.g. 15) can be passed via options for the debug/test page.
const DEFAULT_INFERENCE_FPS = 2;

/** Default lightweight model for activity detection */
const DEFAULT_MODEL = require('../../../assets/models/movenet_thunder.tflite');

/** Higher-accuracy model for the pose test page (12MB float16 v4) */
export const HEAVY_MODEL = require('../../../assets/models/movenet_thunder_f32.tflite');

export interface PoseDetectionOptions {
  /** TFLite model asset (from require()). Defaults to movenet_thunder int8. */
  modelSource?: number;
  /** Inference frames per second. Defaults to 2 for battery savings. */
  fps?: number;
}

export function usePoseDetection(
  isRecording: boolean = false,
  options?: PoseDetectionOptions,
) {
  const modelSource = options?.modelSource ?? DEFAULT_MODEL;
  const inferenceFps = options?.fps ?? DEFAULT_INFERENCE_FPS;

  const plugin = useTensorflowModel(modelSource);
  const { resize } = useResizePlugin();

  const poseResult = useSharedValue<number[]>(new Array(17 * 3).fill(0));
  const lastPose = useSharedValue<number[] | null>(null);
  const motionEma = useSharedValue(0);

  // Use a SharedValue for isRecording so the worklet always sees the latest
  // value without depending on closure re-capture timing.
  const isRecordingSV = useSharedValue(isRecording);
  useEffect(() => {
    isRecordingSV.value = isRecording;
  }, [isRecording]);

  const [monitorData, setMonitorData] = useState({
    isWorkingOut: false,
    confidence: 0,
    motion: 0,
    rawScores: [] as number[],
    rawFirstValues: [] as number[],
    poseData: [] as number[],
  });

  const debugCounter = useSharedValue(0);

  const updateMonitorSafe = useRunOnJS((data) => {
    setMonitorData(data);
    // Log every 15th frame to Metro console for debugging
    if (data._debugCount !== undefined && data._debugCount % 15 === 0) {
      console.log(`🦴 POSE [${data._debugCount}] conf=${(data.confidence * 100).toFixed(1)}% frame=${data._frameW}x${data._frameH} orient=${data._frameOrientation} raw=[${
        data.rawFirstValues?.map((v: number) => v.toFixed(3)).join(', ') ?? 'none'
      }] scores=[${
        data.rawScores?.slice(0, 5).map((v: number) => (v * 100).toFixed(1)).join(', ') ?? 'none'
      }...]`);
    }
  }, []);

  const minKeypointScore = 0.3;
  const minConfidence = 0.2;
  const minMotion = 0.015;
  const motionEmaDecay = 0.7;

  const frameProcessor = useFrameProcessor((frame) => {
    'worklet';
    if (plugin.state !== 'loaded' || plugin.model == null) return;
    
    // Optimization: Skip inference if not recording
    if (!isRecordingSV.value) return;

    // Throttle to target FPS — skips frames that exceed the budget,
    // preventing the resize→inference pipeline from allocating buffers
    // on every camera frame (critical for Android memory stability).
    runAtTargetFps(inferenceFps, () => {
      'worklet';

      // 1. Preprocessing — let the resize plugin center-crop automatically.
      // We handle coordinate remapping manually after inference.
      const resized = resize(frame, {
        scale: { width: 256, height: 256 },
        pixelFormat: 'rgb',
        dataType: 'uint8',
      });

      // Capture frame info for coordinate rotation and debug output
      const _frameW = frame.width;
      const _frameH = frame.height;
      const _frameOrientation = frame.orientation;

      // 2. Inference
      const outputs = plugin.model!.runSync([resized]);
      const data = outputs[0];

      if (data) {
        const getVal = (idx: number) => {
          let v = Number(data[idx]);
          return v > 1.0 ? v / 255.0 : v;
        };

        // A. Build skeleton data with coordinate rotation
        // The camera sensor outputs in landscape orientation. MoveNet outputs
        // [y, x, score] in the model's input space (landscape). We need to
        // rotate these to match the portrait camera preview on screen.
        //
        // Also account for the center-crop: the resize plugin crops the
        // longer dimension to make a square before scaling to 256x256.
        const orientation = _frameOrientation;
        const isLandscape = _frameW > _frameH;
        const cropRatio = isLandscape
          ? _frameH / _frameW  // e.g., 720/1280 = 0.5625
          : _frameW / _frameH; // e.g., 720/1280 = 0.5625
        const cropOffset = (1 - cropRatio) / 2; // e.g., 0.21875

        const newPose = new Array(17 * 3);
        for (let i = 0; i < 17; i++) {
          const rawY = getVal(i * 3);     // model y (along sensor short axis)
          const rawX = getVal(i * 3 + 1); // model x (along sensor long axis)
          const score = getVal(i * 3 + 2);

          let screenYNorm: number;
          let screenXNorm: number;

          if (orientation === 'landscape-right') {
            // Back camera in portrait: sensor x→screen y, sensor y→screen x (inverted)
            screenXNorm = 1 - rawY;
            screenYNorm = rawX * cropRatio + cropOffset;
          } else if (orientation === 'landscape-left') {
            // Front camera in portrait: sensor x→screen y (inverted), sensor y→screen x
            screenXNorm = rawY;
            screenYNorm = (1 - rawX) * cropRatio + cropOffset;
          } else {
            // Portrait-up or portrait-down — no rotation needed
            screenXNorm = rawX;
            screenYNorm = rawY;
          }

          newPose[i * 3] = screenYNorm;     // store as [y, x, score]
          newPose[i * 3 + 1] = screenXNorm;
          newPose[i * 3 + 2] = score;
        }
        poseResult.value = newPose;

        // B. Confidence and motion detection
        let confidenceSum = 0;
        for (let i = 0; i < 17; i++) confidenceSum += newPose[i * 3 + 2];
        const confidence = confidenceSum / 17;

        let motion = 0;
        let motionCount = 0;
        const prevPose = lastPose.value;
        if (prevPose) {
          for (let i = 0; i < 17; i++) {
            const score = newPose[i * 3 + 2];
            const prevScore = prevPose[i * 3 + 2];
            if (score >= minKeypointScore && prevScore >= minKeypointScore) {
              const dy = newPose[i * 3] - prevPose[i * 3];
              const dx = newPose[i * 3 + 1] - prevPose[i * 3 + 1];
              motion += Math.sqrt(dx * dx + dy * dy);
              motionCount += 1;
            }
          }
        }
        motion = motionCount > 0 ? motion / motionCount : 0;
        const smoothedMotion =
          motionEma.value * motionEmaDecay + motion * (1 - motionEmaDecay);
        motionEma.value = smoothedMotion;
        lastPose.value = newPose;

        const isWorkingOut =
          confidence > minConfidence && smoothedMotion > minMotion;

        // Collect raw per-keypoint scores for debug
        const rawScores = new Array(17);
        for (let ki = 0; ki < 17; ki++) rawScores[ki] = newPose[ki * 3 + 2];

        // First 6 raw model output values (before getVal normalization)
        const rawFirstValues = new Array(Math.min(6, data.length));
        for (let ri = 0; ri < rawFirstValues.length; ri++) rawFirstValues[ri] = Number(data[ri]);

        debugCounter.value = debugCounter.value + 1;

        // Update monitor data on every inference frame
        updateMonitorSafe({
          isWorkingOut: isWorkingOut,
          confidence: confidence,
          motion: smoothedMotion,
          rawScores: rawScores,
          rawFirstValues: rawFirstValues,
          poseData: newPose,
          _debugCount: debugCounter.value,
          _frameW,
          _frameH,
          _frameOrientation,
        });
      }
    });
  }, [plugin, updateMonitorSafe]);

  return { frameProcessor, poseResult, monitorData, isModelLoaded: plugin.state === 'loaded' };
}
