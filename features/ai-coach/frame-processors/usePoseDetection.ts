import { useState } from 'react';
import { useTensorflowModel } from 'react-native-fast-tflite';
import { useSharedValue } from 'react-native-reanimated';
import { useFrameProcessor } from 'react-native-vision-camera';
import { useRunOnJS } from 'react-native-worklets-core';
import { useResizePlugin } from 'vision-camera-resize-plugin';

export function usePoseDetection() {
  const plugin = useTensorflowModel(require('../../../assets/models/movenet_thunder.tflite'));
  const { resize } = useResizePlugin();

  const isSquatting = useSharedValue(false);
  const repCount = useSharedValue(0);
  const frameCounter = useSharedValue(0);

  // 📡 [요청 반영] 모든 데이터를 다 보여주기 위한 State
  const [monitorData, setMonitorData] = useState({ 
    x: 0, y: 0,           // 엉덩이
    kneeY: 0,             // 무릎
    squatThresh: 0,       // 앉기 기준
    standThresh: 0,       // 서기 기준 (이게 중요!)
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

    const resized = resize(frame, {
      scale: { width: 256, height: 256 },
      pixelFormat: 'rgb',
      dataType: 'uint8',
    });

    const outputs = plugin.model.runSync([resized]);
    const data = outputs[0];

    if (data) {
      const getVal = (idx: number) => {
        let v = Number(data[idx]);
        return v > 1.0 ? v / 255.0 : v;
      };

      // 1. 좌표 추출
      const hipY = (getVal(11*3) + getVal(12*3)) / 2;
      const hipX = (getVal(11*3+1) + getVal(12*3+1)) / 2;
      const kneeY = (getVal(13*3) + getVal(14*3)) / 2;
      const score = (getVal(11*3+2) + getVal(12*3+2)) / 2;

      // 2. [판정 로직 튜닝]
      // 스쿼트 깊이 (앉기): 무릎 높이(-0.02)
      const squatThreshold = kneeY - 0.02; 
      
      // 🚨 리셋 높이 (서기): 기준 완화!
      // 기존 0.15 -> 0.10으로 변경 (덜 일어서도 인정)
      const standThreshold = kneeY - 0.10;

      if (score > 0.2) {
        if (!isSquatting.value && hipY > squatThreshold) {
          isSquatting.value = true; // ⬇️ 앉았다!
        } else if (isSquatting.value && hipY < standThreshold) {
          isSquatting.value = false; // ⬆️ 일어났다!
          repCount.value += 1;
        }
      }

      // 3. 데이터 전송 (매 5프레임)
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

  return { frameProcessor, monitorData, isModelLoaded: plugin.state === 'loaded' };
}