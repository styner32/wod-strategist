import { useEffect, useLayoutEffect, useRef, useState } from 'react';

import type { TensorflowModel } from 'react-native-fast-tflite';
import { useSharedValue } from 'react-native-reanimated';
import { runAtTargetFps, useFrameProcessor } from 'react-native-vision-camera';
import { useRunOnJS, useSharedValue as useWorkletSharedValue } from 'react-native-worklets-core';
import { createResizePlugin, type ResizePlugin } from 'vision-camera-resize-plugin';

const DEFAULT_INFERENCE_FPS = 2;
const DEFAULT_MODEL = require('../../../assets/models/movenet_thunder.tflite');

/** Float16-weight Thunder model; its input is still uint8 and output float32. */
export const HEAVY_MODEL = require('../../../assets/models/movenet_thunder_f32.tflite');

export interface PoseDetectionOptions {
  modelSource?: number;
  /** Preview defaults to 2 FPS. The standalone test page requests 15 FPS. */
  fps?: number;
}

interface PoseMonitorData {
  isWorkingOut: boolean;
  confidence: number;
  motion: number;
  rawScores: number[];
  rawFirstValues: number[];
  poseData: number[];
}

type PoseStatus = 'disabled' | 'loading' | 'loaded' | 'error';
interface PoseState { status: PoseStatus; error: string | null; hasInference: boolean }
interface PoseRuntime {
  source: number;
  generation: number;
  model: TensorflowModel;
  resize: ResizePlugin['resize'];
}

const EMPTY_MONITOR: PoseMonitorData = {
  isWorkingOut: false, confidence: 0, motion: 0,
  rawScores: [], rawFirstValues: [], poseData: [],
};

/** MoveNet diagnostics for camera previews and the standalone pose test only. */
export function usePoseDetection(enabled = true, options?: PoseDetectionOptions) {
  const modelSource = options?.modelSource ?? DEFAULT_MODEL;
  const requestedFps = options?.fps ?? DEFAULT_INFERENCE_FPS;
  const inferenceFps = Number.isFinite(requestedFps) && requestedFps > 0 ? requestedFps : DEFAULT_INFERENCE_FPS;
  const enabledRef = useRef(enabled);
  useLayoutEffect(() => { enabledRef.current = enabled; }, [enabled]);
  const generationRef = useRef(0);
  const latestInferenceRef = useRef<PoseMonitorData | null>(null);
  const [runtime, setRuntime] = useState<PoseRuntime | null>(null);
  const [state, setState] = useState<PoseState>({ status: enabled ? 'loading' : 'disabled', error: null, hasInference: false });
  const [monitorData, setMonitorData] = useState(EMPTY_MONITOR);

  // Frame processors run on worklets-core, independently of Reanimated's UI runtime.
  const lastPose = useWorkletSharedValue<number[] | null>(null);
  const motionEma = useWorkletSharedValue(0);
  const inferenceFailed = useWorkletSharedValue(false);
  const activeGeneration = useWorkletSharedValue(0);
  // SkeletonOverlay consumes a Reanimated value; only the JS bridge writes it.
  const poseResult = useSharedValue<number[]>([]);

  // worklets-core exposes mutable native SharedValues; these assignments reset camera-runtime history.
  /* eslint-disable react-hooks/immutability */
  useEffect(() => {
    const generation = ++generationRef.current;
    activeGeneration.value = generation;
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- Retire the previous native runtime when its lifecycle inputs change.
    setRuntime(null);
    latestInferenceRef.current = null;
    setMonitorData(EMPTY_MONITOR);
    poseResult.set([]);
    lastPose.value = null;
    motionEma.value = 0;
    inferenceFailed.value = false;
    setState({ status: enabled ? 'loading' : 'disabled', error: null, hasInference: false });

    if (enabled) {
      void (async () => {
        try {
          // The SDK installs bindings at import. Yield first so a departed preview does no native work.
          await Promise.resolve();
          if (cancelled || !enabledRef.current || generation !== generationRef.current) return;
          // eslint-disable-next-line @typescript-eslint/no-require-imports -- The SDK's import-time JSI installation must be opt-in, including in Expo/Jest's CommonJS runtime.
          const { loadTensorflowModel } = require('react-native-fast-tflite') as typeof import('react-native-fast-tflite');
          if (cancelled || !enabledRef.current || generation !== generationRef.current) return;
          const { resize } = createResizePlugin();
          const model = await loadTensorflowModel(modelSource);
          if (cancelled || !enabledRef.current || generation !== generationRef.current) return;
          setRuntime({ source: modelSource, generation, model, resize });
          setState({ status: 'loaded', error: null, hasInference: false });
        } catch (error) {
          if (cancelled || !enabledRef.current || generation !== generationRef.current) return;
          setState({ status: 'error', error: error instanceof Error ? error.message : String(error), hasInference: false });
        }
      })();
    }

    return () => {
      cancelled = true;
      activeGeneration.value = 0;
      // eslint-disable-next-line react-hooks/exhaustive-deps -- This counter deliberately invalidates native deliveries, rather than referencing a DOM node.
      generationRef.current++;
    };
  }, [enabled, modelSource, poseResult, lastPose, motionEma, inferenceFailed, activeGeneration]);
  /* eslint-enable react-hooks/immutability */

  // Publish diagnostics at a steady UI cadence, without recording-frame statistics.
  useEffect(() => {
    if (!enabled || runtime === null) return;
    let published: PoseMonitorData | null = null;
    const timer = setInterval(() => {
      if (!enabledRef.current || runtime.generation !== generationRef.current) return;
      const latest = latestInferenceRef.current;
      if (latest !== null && latest !== published) {
        published = latest;
        setMonitorData(latest);
      }
    }, 500);
    return () => clearInterval(timer);
  }, [enabled, runtime]);

  const updateMonitorSafe = useRunOnJS((generation: number, data: PoseMonitorData) => {
    if (!enabledRef.current || generation !== generationRef.current) return;
    const firstInference = latestInferenceRef.current === null;
    latestInferenceRef.current = data;
    poseResult.set(data.poseData);
    if (firstInference) {
      setMonitorData(data);
      setState({ status: 'loaded', error: null, hasInference: true });
    }
  }, [poseResult]);

  const reportInferenceError = useRunOnJS((generation: number, message: string) => {
    if (!enabledRef.current || generation !== generationRef.current) return;
    generationRef.current++;
    setRuntime(null);
    latestInferenceRef.current = null;
    poseResult.set([]);
    setMonitorData(EMPTY_MONITOR);
    setState({ status: 'error', error: message, hasInference: false });
  }, [poseResult]);

  const model = runtime?.model;
  const resize = runtime?.resize;
  const generation = runtime?.generation ?? 0;
  const frameProcessor = useFrameProcessor((frame) => {
    'worklet';
    if (!enabled || !model || !resize || activeGeneration.value !== generation || inferenceFailed.value) return;
    // Keep this throttle: resizing and inference on every camera frame can exhaust memory.
    runAtTargetFps(inferenceFps, () => {
      'worklet';
      try {
        const resized = resize(frame, {
          scale: { width: 256, height: 256 }, pixelFormat: 'rgb', dataType: 'uint8',
        });
        if (activeGeneration.value !== generation) return;
        const data = model.runSync([resized])[0];
        if (activeGeneration.value !== generation) return;
        if (!data || data.length !== 17 * 3) throw new Error('MoveNet must return 51 keypoint values.');
        for (let i = 0; i < data.length; i++) {
          if (!Number.isFinite(Number(data[i]))) throw new Error('MoveNet returned a non-finite keypoint value.');
        }

        // Both bundled models already return float32 [y, x, score] values.
        const orientation = frame.orientation;
        const cropRatio = frame.width > frame.height ? frame.height / frame.width : frame.width / frame.height;
        const cropOffset = (1 - cropRatio) / 2;
        const newPose = new Array<number>(17 * 3);
        for (let i = 0; i < 17; i++) {
          const rawY = Number(data[i * 3]);
          const rawX = Number(data[i * 3 + 1]);
          const score = Number(data[i * 3 + 2]);
          let screenYNorm: number;
          let screenXNorm: number;
          if (orientation === 'landscape-right') {
            screenXNorm = 1 - rawY;
            screenYNorm = rawX * cropRatio + cropOffset;
          } else if (orientation === 'landscape-left') {
            screenXNorm = rawY;
            screenYNorm = (1 - rawX) * cropRatio + cropOffset;
          } else {
            screenXNorm = rawX;
            screenYNorm = rawY;
          }
          newPose[i * 3] = screenYNorm;
          newPose[i * 3 + 1] = screenXNorm;
          newPose[i * 3 + 2] = score;
        }

        let confidenceSum = 0;
        let motion = 0;
        let motionCount = 0;
        let visibleCount = 0;
        const prevPose = lastPose.value;
        for (let i = 0; i < 17; i++) {
          const score = newPose[i * 3 + 2];
          confidenceSum += score;
          if (score >= 0.2) visibleCount++;
          if (prevPose && score >= 0.3 && prevPose[i * 3 + 2] >= 0.3) {
            const dy = newPose[i * 3] - prevPose[i * 3];
            const dx = newPose[i * 3 + 1] - prevPose[i * 3 + 1];
            motion += Math.sqrt(dx * dx + dy * dy);
            motionCount++;
          }
        }
        const confidence = confidenceSum / 17;
        motion = motionCount > 0 ? motion / motionCount : 0;
        const smoothedMotion = motionEma.value * 0.7 + motion * 0.3;
        if (activeGeneration.value !== generation) return;
        motionEma.value = smoothedMotion;
        lastPose.value = newPose;
        const personDetected = confidence > 0.15 || visibleCount >= 5;
        const rawScores = new Array<number>(17);
        for (let i = 0; i < 17; i++) rawScores[i] = newPose[i * 3 + 2];
        const rawFirstValues = new Array<number>(6);
        for (let i = 0; i < 6; i++) rawFirstValues[i] = Number(data[i]);
        updateMonitorSafe(generation, {
          isWorkingOut: personDetected && smoothedMotion > 0.008,
          confidence, motion: smoothedMotion, rawScores, rawFirstValues, poseData: newPose,
        });
      } catch (error) {
        if (activeGeneration.value !== generation) return;
        inferenceFailed.value = true;
        reportInferenceError(generation, error instanceof Error ? error.message : String(error));
      }
    });
  }, [enabled, model, resize, generation, inferenceFps, inferenceFailed, activeGeneration, lastPose, motionEma, updateMonitorSafe, reportInferenceError]);

  const ready = enabled && state.status === 'loaded' && runtime?.source === modelSource;
  return {
    frameProcessor: ready ? frameProcessor : undefined,
    poseResult,
    monitorData: enabled ? monitorData : EMPTY_MONITOR,
    isModelLoaded: ready,
    status: enabled ? state.status : 'disabled' as PoseStatus,
    error: enabled ? state.error : null,
    hasInference: enabled && state.hasInference,
  };
}
