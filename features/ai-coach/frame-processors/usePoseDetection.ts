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

  // 📡 [Modified] Monitor Data for Head, Shoulder, Hip
  const [monitorData, setMonitorData] = useState({ 
    headY: 0,
    shoulderY: 0,
    hipY: 0,
    score: 0,
  });

  const updateMonitorSafe = useRunOnJS((data) => {
    setMonitorData(data);
  }, []);

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

      // B. Body Part Coordinates
      // Head (Nose: 0)
      const headY = getVal(0 * 3);

      // Shoulders (Left: 5, Right: 6)
      const shoulderY = (getVal(5 * 3) + getVal(6 * 3)) / 2;

      // Hips (Left: 11, Right: 12)
      const hipY = (getVal(11 * 3) + getVal(12 * 3)) / 2;

      // Score (Avg of hips confidence, or overall confidence)
      const score = (getVal(11 * 3 + 2) + getVal(12 * 3 + 2)) / 2;

      // C. Data Transfer (Every 5 frames)
      if (frameCounter.value % 5 === 0) {
        updateMonitorSafe({
          headY: headY,
          shoulderY: shoulderY,
          hipY: hipY,
          score: score,
        });
      }
    }
  }, [plugin, updateMonitorSafe]);

  return { frameProcessor, poseResult, monitorData, isModelLoaded: plugin.state === 'loaded' };
}
