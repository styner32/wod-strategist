import { useCallback, useEffect, useRef, useState } from 'react';

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

  // --- Ref-based bridge: worklet → useRunOnJS → ref → polling → React state ---
  // Why not SharedValues? Reading .value on the JS thread triggers Reanimated
  // strict-mode warnings and returns stale/zero values during recording.
  // Why not setMonitorData directly in useRunOnJS? That worked but caused
  // UI blinking when called at high frequency.
  // Solution: useRunOnJS writes to a ref (always fast), polling reads ref
  // and updates React state at a steady 500ms cadence.
  const latestInferenceRef = useRef({
    confidence: 0,
    motion: 0,
    isWorkingOut: false,
    poseData: [] as number[],
    rawScores: [] as number[],
    rawFirstValues: [] as number[],
  });

  // Frame count refs — updated in useRunOnJS callback, read in
  // resetFrameCounts/getWorkoutConfidence. Declared before updateMonitorSafe
  // for readability (the callback references them but isn't invoked synchronously).
  const inferenceFrameCountRef = useRef(0);
  const workoutFrameCountRef = useRef(0);

  // Polling: read from the ref (plain JS, no SharedValue .value reads)
  // and push to React state at a steady cadence.
  useEffect(() => {
    const interval = setInterval(() => {
      const latest = latestInferenceRef.current;
      setMonitorData({
        confidence: latest.confidence,
        motion: latest.motion,
        isWorkingOut: latest.isWorkingOut,
        poseData: latest.poseData,
        rawScores: latest.rawScores,
        rawFirstValues: latest.rawFirstValues,
      });
    }, 500);
    return () => clearInterval(interval);
  }, []);

  // useRunOnJS callback — writes to ref + frame counting + debug logging.
  // The ref write is cheap (no React state update), so even at 15fps on
  // the poseTestPage there's no performance concern.
  const updateMonitorSafe = useRunOnJS((data) => {
    // Write to ref — always succeeds, no render triggered
    latestInferenceRef.current = {
      confidence: data.confidence,
      motion: data.motion,
      isWorkingOut: data.isWorkingOut,
      poseData: data.poseData,
      rawScores: data.rawScores,
      rawFirstValues: data.rawFirstValues,
    };
    // Always count frames — refs are reset at each chunk boundary by
    // resetFrameCounts(), so only inter-chunk frames contribute to confidence.
    // (Gating on data._isRecording was tried and failed — the SV read from
    // the worklet returns false during recording on this device, zeroing
    // the counts. Preview-frame pollution at 1fps is tolerable.)
    inferenceFrameCountRef.current++;
    if (data.isWorkingOut) {
      workoutFrameCountRef.current++;
    }
    // Log every 15th frame to Metro console for debugging
    if (data._debugCount !== undefined && data._debugCount % 15 === 0) {
      console.log(`🦴 POSE [${data._debugCount}] conf=${(data.confidence * 100).toFixed(1)}% rec=${data._isRecording} refTotal=${inferenceFrameCountRef.current} visible=${data._visibleCount} scores=[${
        data.rawScores?.slice(0, 5).map((v: number) => (v * 100).toFixed(1)).join(', ') ?? 'none'
      }...]`);
    }
  }, []);

  const minKeypointScore = 0.3;
  const minConfidence = 0.12; // lowered from 0.2 — avg across 17 keypoints drops fast with partial occlusion
  const minMotion = 0.008;    // lowered from 0.015 — EMA at 2fps with decay=0.7 ramps slowly
  const motionEmaDecay = 0.7;

  const frameProcessor = useFrameProcessor((frame) => {
    'worklet';
    if (plugin.state !== 'loaded' || plugin.model == null) return;

    // Throttle to target FPS — skips frames that exceed the budget,
    // preventing the resize→inference pipeline from allocating buffers
    // on every camera frame (critical for Android memory stability).
    // Use lower FPS in preview mode (1fps) to save battery while still
    // providing live CONF/MOTION/STATE feedback on the dashboard.
    runAtTargetFps(isRecordingSV.value ? inferenceFps : 1, () => {
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

        // Person is "working out" if detected AND moving.
        // Detection alone (standing still in frame) doesn't count as exercising.
        // (a) person detected: high confidence OR enough keypoints visible, AND
        // (b) there's actual motion between frames
        let visibleCount = 0;
        for (let vi = 0; vi < 17; vi++) {
          if (newPose[vi * 3 + 2] >= 0.2) visibleCount++;
        }
        const personDetected = confidence > 0.15 || visibleCount >= 5;
        const isWorkingOut = personDetected && smoothedMotion > minMotion;

        // Collect raw per-keypoint scores for debug
        const rawScores = new Array(17);
        for (let ki = 0; ki < 17; ki++) rawScores[ki] = newPose[ki * 3 + 2];

        // First 6 raw model output values (before getVal normalization)
        const rawFirstValues = new Array(Math.min(6, data.length));
        for (let ri = 0; ri < rawFirstValues.length; ri++) rawFirstValues[ri] = Number(data[ri]);

        debugCounter.value = debugCounter.value + 1;

        // Bridge all data to JS thread via useRunOnJS → ref
        updateMonitorSafe({
          isWorkingOut: isWorkingOut,
          confidence: confidence,
          motion: smoothedMotion,
          rawScores: rawScores,
          rawFirstValues: rawFirstValues,
          poseData: newPose,
          _isRecording: isRecordingSV.value,
          _debugCount: debugCounter.value,
          _visibleCount: visibleCount,
          _frameW,
          _frameH,
          _frameOrientation,
        });
      }
    });
  }, [plugin, updateMonitorSafe]);

  /**
   * Returns the accumulated workout/total frame counts since the last reset.
   * Call this at chunk boundaries to compute workout confidence, then reset.
   */
  const resetFrameCounts = useCallback(() => {
    const total = inferenceFrameCountRef.current;
    const workout = workoutFrameCountRef.current;
    inferenceFrameCountRef.current = 0;
    workoutFrameCountRef.current = 0;
    return { total, workout };
  }, []);

  /**
   * Returns the current workout confidence (0..1) without resetting counters.
   * Reads from refs updated by useRunOnJS at inference FPS.
   */
  const getWorkoutConfidence = useCallback((): number => {
    const total = inferenceFrameCountRef.current;
    return total > 0 ? workoutFrameCountRef.current / total : 0;
  }, []);

  /**
   * Returns the latest motion/confidence snapshot directly from the ref.
   * Avoids stale React state closures — safe for telemetry providers.
   */
  const getLatestMotion = useCallback(() => ({
    detected: latestInferenceRef.current.isWorkingOut,
    confidence: latestInferenceRef.current.confidence,
  }), []);

  return { frameProcessor, poseResult, monitorData, isModelLoaded: plugin.state === 'loaded', resetFrameCounts, getWorkoutConfidence, getLatestMotion };
}
