import { Buffer } from "buffer";
import { useEffect, useRef, useState } from "react";
import { PermissionsAndroid, Platform } from "react-native";
import { BleManager, Device, State, Subscription } from "react-native-ble-plx";

// [중요] Manager는 컴포넌트 밖에서 한 번만 생성 (메모리 릭 방지)
const manager = new BleManager();

const HR_SERVICE_UUID = "180D";
const HR_CHARACTERISTIC_UUID = "2A37";
const CONNECTION_TIMEOUT_MS = 10000;
const INACTIVITY_TIMEOUT_MS = 15000;
const RECONNECT_MIN_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 10000;

export function useBleHeartRate() {
  const [bpm, setBpm] = useState(0);
  const [status, setStatus] = useState<
    "Init" | "Scanning" | "Connecting" | "Live" | "Error"
  >("Init");
  const deviceRef = useRef<Device | null>(null);
  const bleStateRef = useRef<State | null>(null);
  const isMountedRef = useRef(true);
  const isScanningRef = useRef(false);
  const isConnectingRef = useRef(false);
  const isReconnectingRef = useRef(false);
  const reconnectAttemptsRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inactivityTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const monitorSubscriptionRef = useRef<Subscription | null>(null);
  const disconnectSubscriptionRef = useRef<Subscription | null>(null);

  useEffect(() => {
    // 1. 블루투스 상태 감지 (PoweredOn 될 때까지 대기)
    const subscription = manager.onStateChange((state) => {
      console.log("🔹 BLE State:", state); // 로그 확인 필수
      bleStateRef.current = state;

      if (state === State.PoweredOn) {
        if (!deviceRef.current && !isScanningRef.current) {
          startScan();
        }
      } else {
        stopScan();
        clearInactivityTimer();
        void cleanupConnection();
        setStatus("Init");
      }
    }, true); // true: 현재 상태 즉시 검사

    return () => {
      isMountedRef.current = false;
      clearReconnectTimer();
      clearInactivityTimer();
      // 클린업: 스캔 중단 및 연결 해제
      stopScan();
      cleanupSubscriptions();
      deviceRef.current?.cancelConnection();
      subscription.remove();
    };
  }, []);

  const clearReconnectTimer = () => {
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  };

  const clearInactivityTimer = () => {
    if (inactivityTimerRef.current) {
      clearTimeout(inactivityTimerRef.current);
      inactivityTimerRef.current = null;
    }
  };

  const resetInactivityTimer = () => {
    clearInactivityTimer();
    inactivityTimerRef.current = setTimeout(() => {
      if (!isMountedRef.current) return;
      console.warn("⚠️ Heart rate inactive. Reconnecting...");
      requestReconnect("inactive");
    }, INACTIVITY_TIMEOUT_MS);
  };

  const cleanupSubscriptions = () => {
    monitorSubscriptionRef.current?.remove();
    monitorSubscriptionRef.current = null;
    disconnectSubscriptionRef.current?.remove();
    disconnectSubscriptionRef.current = null;
  };

  const stopScan = () => {
    if (isScanningRef.current) {
      manager.stopDeviceScan();
      isScanningRef.current = false;
    }
  };

  const cleanupConnection = async () => {
    cleanupSubscriptions();
    clearInactivityTimer();
    const device = deviceRef.current;
    deviceRef.current = null;
    if (device) {
      try {
        await device.cancelConnection();
      } catch (error) {
        console.warn("Disconnect cleanup error:", error);
      }
    }
  };

  const requestReconnect = (reason: string) => {
    if (!isMountedRef.current) return;
    if (reconnectTimerRef.current) return;

    const attempt = reconnectAttemptsRef.current;
    const delay = Math.min(
      RECONNECT_MIN_DELAY_MS * 2 ** attempt,
      RECONNECT_MAX_DELAY_MS,
    );
    reconnectAttemptsRef.current = Math.min(attempt + 1, 5);

    console.log(`♻️ Reconnect scheduled (${reason}) in ${delay}ms`);
    setStatus("Scanning");
    reconnectTimerRef.current = setTimeout(() => {
      reconnectTimerRef.current = null;
      void reconnectNow(reason);
    }, delay);
  };

  const reconnectNow = async (reason: string) => {
    if (!isMountedRef.current || isReconnectingRef.current) return;
    isReconnectingRef.current = true;

    try {
      console.log(`♻️ Reconnecting now (${reason})`);
      const lastDevice = deviceRef.current;
      await cleanupConnection();
      setBpm(0);

      if (lastDevice) {
        await connectToDevice(lastDevice);
        if (deviceRef.current) {
          return;
        }
      }

      await startScan();
    } finally {
      isReconnectingRef.current = false;
    }
  };

  const startScan = async () => {
    if (isScanningRef.current || isConnectingRef.current || deviceRef.current) {
      return;
    }

    if (bleStateRef.current && bleStateRef.current !== State.PoweredOn) {
      return;
    }

    clearReconnectTimer();
    stopScan();
    isScanningRef.current = true;

    // Android 권한 요청 (iOS는 Info.plist 자동 처리됨)
    if (Platform.OS === "android") {
      const granted = await PermissionsAndroid.requestMultiple([
        PermissionsAndroid.PERMISSIONS.BLUETOOTH_SCAN,
        PermissionsAndroid.PERMISSIONS.BLUETOOTH_CONNECT,
        PermissionsAndroid.PERMISSIONS.ACCESS_FINE_LOCATION,
      ]);
      if (granted["android.permission.BLUETOOTH_SCAN"] !== "granted") {
        console.warn("BLE Permission Denied");
        isScanningRef.current = false;
        setStatus("Error");
        return;
      }
    }

    if (
      !isMountedRef.current ||
      (bleStateRef.current && bleStateRef.current !== State.PoweredOn)
    ) {
      isScanningRef.current = false;
      return;
    }

    console.log("🚀 Scanning started...");
    setStatus("Scanning");

    // [핵심] UUID 자리에 null을 넣어 "모든 기기"를 다 찾습니다.
    // HeartCast가 UUID를 숨기고 광고할 수 있기 때문입니다.
    manager.startDeviceScan(null, null, (error, device) => {
      if (error) {
        console.error("❌ Scan Error:", error);
        setStatus("Error");
        stopScan();
        requestReconnect("scan-error");
        return;
      }

      // 로그로 발견된 기기 이름 확인 (디버깅용)
      // if (device?.name) console.log("Found:", device.name);

      // [필터링] HeartCast(앱) 또는 Polar(심박계) 찾기
      // "Polar mobile"은 폰 앱(브릿지)이므로 제외 — 실제 H10/OH1 스트랩만 연결
      const isPolarApp = device?.name?.includes("Polar mobile");
      const isTargetDevice =
        !isPolarApp &&
        ((device?.name &&
          (device.name.includes("HeartCast") ||
            device.name.includes("Polar"))) ||
          (device?.serviceUUIDs && device.serviceUUIDs.includes(HR_SERVICE_UUID)));

      if (isTargetDevice && device) {
        console.log("✅ Target Found:", device.name);
        stopScan(); // 찾으면 스캔 즉시 중단
        connectToDevice(device);
      }
    });
  };

  const connectToDevice = async (device: Device) => {
    if (isConnectingRef.current) return;
    isConnectingRef.current = true;
    clearReconnectTimer();
    stopScan();

    try {
      setStatus("Connecting");
      console.log(`🔗 Connecting to ${device.name}...`);

      const connectedDevice = await device.connect({ timeout: CONNECTION_TIMEOUT_MS });
      console.log("🔗 Connected. Discovering services...");

      // [필수] 서비스 및 특성 검색
      await connectedDevice.discoverAllServicesAndCharacteristics();
      cleanupSubscriptions();
      deviceRef.current = connectedDevice;
      reconnectAttemptsRef.current = 0;

      disconnectSubscriptionRef.current = connectedDevice.onDisconnected(
        (error) => {
          console.warn("🔌 Disconnected:", error);
          clearInactivityTimer();
          setStatus("Scanning");
          setBpm(0);
          requestReconnect("disconnected");
        },
      );

      console.log("❤️ Monitoring Heart Rate...");
      monitorSubscriptionRef.current = connectedDevice.monitorCharacteristicForService(
        HR_SERVICE_UUID,
        HR_CHARACTERISTIC_UUID,
        (error, characteristic) => {
          if (error) {
            console.error("Monitor Error:", error);
            setStatus("Error");
            requestReconnect("monitor-error");
            return;
          }
          if (characteristic?.value) {
            parseHeartRate(characteristic.value);
          }
        },
      );
      resetInactivityTimer();
      setStatus("Live");
    } catch (e) {
      console.error("❌ Connection Failed:", e);
      setStatus("Error");
      requestReconnect("connect-failed");
    } finally {
      isConnectingRef.current = false;
    }
  };

  const parseHeartRate = (base64Value: string) => {
    try {
      const buffer = Buffer.from(base64Value, "base64");
      const flags = buffer.readUInt8(0);
      const is16Bit = (flags & 1) !== 0;

      let heartRate = 0;
      if (is16Bit) {
        heartRate = buffer.readUInt16LE(1);
      } else {
        heartRate = buffer.readUInt8(1);
      }

      // console.log(`BPM: ${heartRate}`);
      setBpm(heartRate);
      resetInactivityTimer();
    } catch (error) {
      console.warn("Parse Error:", error);
    }
  };

  return { bpm, status };
}
