import { useCallback, useEffect, useRef, useState } from 'react';
import { NativeEventEmitter, NativeModules, Platform } from 'react-native';

const { AppleHealthKit } = NativeModules;
const healthKitEmitter = AppleHealthKit ? new NativeEventEmitter(AppleHealthKit) : null;

const PERMISSIONS = {
  permissions: {
    read: ["HeartRate"],
    write: []
  }
};

export function useHeartRate() {
  const [bpm, setBpm] = useState(0);
  const [status, setStatus] = useState("Init");
  const [isAuthorized, setIsAuthorized] = useState(false);
  
  // 1초 내 중복 호출 방지 (Throttle)
  const lastUpdateRef = useRef<number>(0);

  // 1. 데이터 조회 함수 (이벤트 & 폴링 공용)
  const fetchLatestHeartRate = useCallback(() => {
    const now = Date.now();
    // 너무 잦은 갱신 방지 (1초 텀)
    if (now - lastUpdateRef.current < 1000) return;
    lastUpdateRef.current = now;

    if (!AppleHealthKit?.getHeartRateSamples) return;

    const options = {
      unit: 'bpm',
      startDate: new Date(Date.now() - 5 * 60 * 1000).toISOString(), // 최근 5분
      limit: 1,
      ascending: false,
    };

    AppleHealthKit.getHeartRateSamples(options, (err: any, res: any[]) => {
      if (err) {
        console.error('[HealthKit] Get Heart Rate Samples Error:', err);
        return;
      }

      if (res && res.length > 0) {
        console.log('[HealthKit] Get Heart Rate Samples:', res);
        setBpm(res[0].value);
        setStatus("Live");
      }
    });
  }, []);

  // 2. 초기화
  useEffect(() => {
    if (!AppleHealthKit || Platform.OS !== 'ios') return;

    AppleHealthKit.initHealthKit(PERMISSIONS, (err: any) => {
      if (!err) {
        setIsAuthorized(true);
        
        // [복구] 라이브러리 공식 옵저버 실행
        // 경고가 뜨더라도 현재 버전에서 사용 가능한 유일한 함수입니다.
        if (AppleHealthKit.initHeartRateObserver) {
          AppleHealthKit.initHeartRateObserver({ window: 60 }); 
        }

        fetchLatestHeartRate();
      } else {
        console.warn("HealthKit Init Failed:", err);
      }
    });
  }, [fetchLatestHeartRate]);

  // 3. [핵심] 하이브리드 리스너 (이벤트 + 폴링)
  useEffect(() => {
    if (!isAuthorized || !healthKitEmitter) return;

    // (A) 이벤트 리스너: 라이브러리 버전에 따라 이벤트명이 다를 수 있어 안전하게 둘 다 구독
    const sub1 = healthKitEmitter.addListener('healthKit:HeartRate:new', fetchLatestHeartRate);
    const sub2 = healthKitEmitter.addListener('healthKit:HeartRate:sample', fetchLatestHeartRate);

    // (B) 강제 폴링: 녹화 중 시스템 부하로 이벤트가 씹힐 때를 대비한 안전장치
    // 2초마다 무조건 데이터를 긁어옵니다.
    const pollingInterval = setInterval(fetchLatestHeartRate, 2000);

    return () => {
      sub1.remove();
      sub2.remove();
      clearInterval(pollingInterval);
    };
  }, [isAuthorized, fetchLatestHeartRate]);

  return { bpm, status };
}