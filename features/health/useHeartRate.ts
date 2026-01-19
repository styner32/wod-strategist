import { useEffect, useState } from 'react';
import { NativeModules, Platform } from 'react-native';

// 🚨 [핵심 패치] 라이브러리 import 대신 NativeModules 직접 사용
// RN 0.76 호환성 문제를 해결하기 위해 '다이렉트'로 연결합니다.
const { AppleHealthKit } = NativeModules;

// 권한 설정 (라이브러리 상수 대신 직접 문자열 사용)
const PERMISSIONS = {
  permissions: {
    read: ["HeartRate"], // "HeartRate" 문자열 직접 입력
    write: [],
  },
};

export function useHeartRate() {
  const [bpm, setBpm] = useState<number>(0);
  const [isAuthorized, setIsAuthorized] = useState(false);
  const [status, setStatus] = useState("Initializing...");

  useEffect(() => {
    if (Platform.OS !== 'ios') return;

    // 1. 네이티브 모듈 연결 확인
    // 플러그인 설정이 안 되어있으면 여기서 걸러집니다.
    if (!AppleHealthKit) {
      console.error("❌ HealthKit Native Module Not Found.");
      setStatus("Native Module Missing (Rebuild Required)");
      return;
    }

    setStatus("Requesting Auth...");

    // 2. 초기화 (이제 함수가 없다는 에러가 안 날 것입니다)
    AppleHealthKit.initHealthKit(PERMISSIONS, (error: string) => {
      if (error) {
        console.log('[HealthKit] Init Error:', error);
        setStatus(`Error: ${error}`);
        return;
      }
      setIsAuthorized(true);
      setStatus("Authorized");
      
      // 즉시 조회 시작
      fetchLatestHeartRate();
    });
  }, []);

  const fetchLatestHeartRate = () => {
    // 안전 장치
    if (!AppleHealthKit || !AppleHealthKit.getHeartRateSamples) return;

    const options = {
      unit: 'bpm',
      startDate: new Date(new Date().getTime() - 1000 * 60 * 60).toISOString(), // 1시간 전
      limit: 1,
      ascending: false,
    };

    AppleHealthKit.getHeartRateSamples(options, (err: object, results: any[]) => {
      if (err) return;
      if (results && results.length > 0) {
        setBpm(results[0].value);
        setStatus("Live");
      }
    });
  };

  // 3초마다 갱신
  useEffect(() => {
    if (!isAuthorized) return;
    const interval = setInterval(fetchLatestHeartRate, 3000);
    return () => clearInterval(interval);
  }, [isAuthorized]);

  return { bpm, status };
}