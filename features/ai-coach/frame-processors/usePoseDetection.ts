/*
import { useState } from 'react';
import { useTensorflowModel } from 'react-native-fast-tflite';
import { runOnJS, useSharedValue } from 'react-native-reanimated';
import { useFrameProcessor } from 'react-native-vision-camera';
import { useResizePlugin } from 'vision-camera-resize-plugin';

export function usePoseDetection() {
  // 모델 로드
  const plugin = useTensorflowModel(require('../../../assets/models/movenet_thunder.tflite'));
  const { resize } = useResizePlugin();

  const poseResult = useSharedValue<number[]>(new Array(17 * 3).fill(0));
  
  // [프레임수, X좌표, Y좌표, 로딩상태(0:로딩중, 1:완료)]
  const debugInfo = useSharedValue<number[]>([0, 0, 0, 0]); 
  const [statusMsg, setStatusMsg] = useState<string>("Initializing...");

  const frameProcessor = useFrameProcessor((frame) => {
    'worklet';

    // 🚨 1. 엔진 생존 신고 (모델 로딩 여부와 관계없이 무조건 증가)
    debugInfo.value[0] += 1;

    // 2. 모델 로딩 체크
    if (plugin.state !== 'loaded' || plugin.model == null) {
      // 1초에 한번만 상태 전송 (JS 스레드 부하 방지)
      if (debugInfo.value[0] % 60 === 0) {
        runOnJS(setStatusMsg)(`Model Loading... (${plugin.state})`);
      }
      return;
    }

    try {
      // 3. 로딩 완료됨!
      if (debugInfo.value[3] === 0) {
         debugInfo.value[3] = 1; // 완료 플래그
         runOnJS(setStatusMsg)("✅ AI Active!");
      }

      const resized = resize(frame, {
        scale: { width: 256, height: 256 },
        pixelFormat: 'rgb',
        dataType: 'uint8',
      });

      const outputs = plugin.model.runSync([resized]);
      const data = outputs[0];

      if (data) {
        // 좌표 업데이트
        debugInfo.value[1] = Number(data[1]); // Nose X
        debugInfo.value[2] = Number(data[0]); // Nose Y

        for (let i = 0; i < data.length; i++) {
          let val = Number(data[i]);
          if (val > 1.0) val = val / 255.0;
          poseResult.value[i] = val;
        }
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Error";
      runOnJS(setStatusMsg)(`Error: ${msg}`);
    }
  }, [plugin]);

  return { 
    frameProcessor, 
    poseResult, 
    debugInfo, // SharedValue 그대로 리턴
    modelState: plugin.state,
    errorMsg: statusMsg 
  };
}
  */

import { useState } from 'react';
import { runOnJS, useSharedValue } from 'react-native-reanimated';
import { useFrameProcessor } from 'react-native-vision-camera';

export function usePoseDetection() {
  // 모델 로딩, 리사이즈 다 제거 -> 오직 Worklet 엔진 테스트만 수행
  const debugInfo = useSharedValue<number[]>([0]); 
  const [status, setStatus] = useState("Waiting...");

  const frameProcessor = useFrameProcessor((frame) => {
    'worklet';
    
    // 1. 단순 숫자 증가 (이게 되면 Worklet 설치 성공)
    debugInfo.value[0] += 1;
    
    // 2. 60프레임마다(1초에 한번) 생존 신고
    if (debugInfo.value[0] % 60 === 0) {
      // 콘솔에도 찍고, 화면에도 보냄
      console.log(`🫀 Heartbeat: Frame ${debugInfo.value[0]}`);
      runOnJS(setStatus)(`Engine Running... Frame ${debugInfo.value[0]}`);
    }
  }, []);

  return { 
    frameProcessor, 
    poseResult: debugInfo, // 임시 연결
    debugInfo, 
    modelState: 'test-mode',
    errorMsg: status // 에러 메시지 대신 상태 표시
  };
}