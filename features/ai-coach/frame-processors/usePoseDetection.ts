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
  const isSquatting = useSharedValue(false);
  const repCount = useSharedValue(0);
  const frameCounter = useSharedValue(0);

  // 📡 [복구됨] 모든 디버깅 데이터를 포함하는 State
  const [monitorData, setMonitorData] = useState({ 
    x: 0, y: 0,           // 엉덩이 좌표
    kneeY: 0,             // 무릎 좌표 (기준)
    squatThresh: 0,       // 앉기 목표선 (Goal)
    standThresh: 0,       // 일어서기 목표선 (Reset)
    score: 0,             // 신뢰도
    count: 0,             // 개수
    state: 'STAND'        // 상태
  });

  const updateMonitorSafe = useRunOnJS((data) => {
    setMonitorData(data);
  }, []);

  const frameProcessor = useFrameProcessor((frame) => {
    'worklet';
    if (plugin.state !== 'loaded' || plugin.model == null) return;

    frameCounter.value += 1;

    // 1. 전처리
    const resized = resize(frame, {
      scale: { width: 256, height: 256 },
      pixelFormat: 'rgb',
      dataType: 'uint8',
    });

    // 2. 추론
    const outputs = plugin.model.runSync([resized]);
    const data = outputs[0];

    if (data) {
      const getVal = (idx: number) => {
        let v = Number(data[idx]);
        return v > 1.0 ? v / 255.0 : v;
      };

      // A. 스켈레톤 데이터
      const newPose = new Array(17 * 3);
      for (let i = 0; i < data.length; i++) newPose[i] = getVal(i);
      poseResult.value = newPose;

      // B. 카운팅 로직
      const hipY = (getVal(11*3) + getVal(12*3)) / 2;
      const hipX = (getVal(11*3+1) + getVal(12*3+1)) / 2;
      const kneeY = (getVal(13*3) + getVal(14*3)) / 2;
      const score = (getVal(11*3+2) + getVal(12*3+2)) / 2;

      // 기준값
      const squatThreshold = kneeY - 0.02; 
      const standThreshold = kneeY - 0.10;

      if (score > 0.2) {
        if (!isSquatting.value && hipY > squatThreshold) {
          isSquatting.value = true;
        } else if (isSquatting.value && hipY < standThreshold) {
          isSquatting.value = false;
          repCount.value += 1;
        }
      }

      // C. 데이터 전송 (5프레임마다)
      if (frameCounter.value % 5 === 0) {
        updateMonitorSafe({
          x: hipX, 
          y: hipY, 
          kneeY: kneeY,
          squatThresh: squatThreshold,
          standThresh: standThreshold,
          score: score,
          count: repCount.value,
          state: isSquatting.value ? 'SQUAT' : 'STAND'
        });
      }
    }
  }, [plugin, updateMonitorSafe]);

  return { frameProcessor, poseResult, monitorData, isModelLoaded: plugin.state === 'loaded' };
}