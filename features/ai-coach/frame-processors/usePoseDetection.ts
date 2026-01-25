import { useState } from 'react';
import { useTensorflowModel } from 'react-native-fast-tflite';
import { useSharedValue } from 'react-native-reanimated';
import { useFrameProcessor } from 'react-native-vision-camera';
import { useRunOnJS } from 'react-native-worklets-core';
import { useResizePlugin } from 'vision-camera-resize-plugin';

export function usePoseDetection() {
  const plugin = useTensorflowModel(require('../../../assets/models/movenet_thunder.tflite'));
  const { resize } = useResizePlugin();

  const poseResult = useSharedValue<number[]>(new Array(17 * 3).fill(0));
  const frameCounter = useSharedValue(0);
  const lastPose = useSharedValue<number[] | null>(null);
  const motionEma = useSharedValue(0);

  const [monitorData, setMonitorData] = useState({
    isWorkingOut: false,
    confidence: 0,
    motion: 0,
  });

  const updateMonitorSafe = useRunOnJS((data) => {
    setMonitorData(data);
  }, []);

  const minKeypointScore = 0.3;
  const minConfidence = 0.2;
  const minMotion = 0.015;
  const motionEmaDecay = 0.7;

  const frameProcessor = useFrameProcessor((frame) => {
    'worklet';
    if (plugin.state !== 'loaded' || plugin.model == null) return;

    frameCounter.value += 1;

    // 1. Preprocessing
    const resized = resize(frame, {
      scale: { width: 256, height: 256 },
      pixelFormat: 'rgb',
      dataType: 'uint8',
    });

    // 2. Inference
    const outputs = plugin.model.runSync([resized]);
    const data = outputs[0];

    if (data) {
      const getVal = (idx: number) => {
        let v = Number(data[idx]);
        return v > 1.0 ? v / 255.0 : v;
      };

      // A. Skeleton Data
      const newPose = new Array(17 * 3);
      for (let i = 0; i < data.length; i++) newPose[i] = getVal(i);
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

      // C. Data Transfer (Every 5 frames)
      if (frameCounter.value % 5 === 0) {
        updateMonitorSafe({
          isWorkingOut: isWorkingOut,
          confidence: confidence,
          motion: smoothedMotion,
        });
      }
    }
  }, [plugin, updateMonitorSafe]);

  return { frameProcessor, poseResult, monitorData, isModelLoaded: plugin.state === 'loaded' };
}
