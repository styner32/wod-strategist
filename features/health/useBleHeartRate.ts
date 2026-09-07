import { useEffect, useRef, useState } from "react";
import { PermissionsAndroid, Platform } from "react-native";
import { BleManager, Device, State, Subscription } from "react-native-ble-plx";

import type { BleSensorSink } from "./bleSensorSink";
import {
  BATTERY_CHARACTERISTIC_UUID,
  BATTERY_SERVICE_UUID,
  HR_CHARACTERISTIC_UUID,
  HR_SERVICE_UUID,
  PMD_SERVICE_UUID,
  parseBatteryLevel,
  parseHeartRateMeasurement,
} from "./polar/polarPmdProtocol";

// [중요] Manager는 컴포넌트 밖에서 한 번만 생성 (메모리 릭 방지)
const manager = new BleManager();

const CONNECTION_TIMEOUT_MS = 10000;
const INACTIVITY_TIMEOUT_MS = 15000;
const RECONNECT_MIN_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 10000;

export interface UseBleHeartRateOptions {
  sink?: BleSensorSink;
}

export function useBleHeartRate(options?: UseBleHeartRateOptions) {
  const [bpm, setBpm] = useState(0);
  const [batteryLevel, setBatteryLevel] = useState<number | null>(null);
  const [status, setStatus] = useState<
    "Init" | "Scanning" | "Connecting" | "Live" | "Error"
  >("Init");

  const sinkRef = useRef<BleSensorSink | undefined>(options?.sink);
  const hasPmdRef = useRef(false);

  useEffect(() => {
    const prevSink = sinkRef.current;
    sinkRef.current = options?.sink;
    if (options?.sink && options.sink !== prevSink && deviceRef.current) {
      options.sink.onDeviceReady(deviceRef.current, hasPmdRef.current);
      if (batteryLevel !== null) {
        options.sink.onBattery?.(batteryLevel);
      }
    }
  }, [options?.sink, batteryLevel]);

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
  const batterySubscriptionRef = useRef<Subscription | null>(null);
  const disconnectSubscriptionRef = useRef<Subscription | null>(null);

  useEffect(() => {
    // 1. 블루투스 상태 감지 (PoweredOn 될 때까지 대기)
    const subscription = manager.onStateChange((state) => {
      console.log("🔹 BLE State:", state);
      bleStateRef.current = state;

      if (state === State.PoweredOn) {
        if (!deviceRef.current && !isScanningRef.current) {
          startScan();
        }
      } else {
        stopScan();
        clearInactivityTimer();
        void cleanupConnection(`ble-state-${state}`);
        setStatus("Init");
      }
    }, true);

    return () => {
      isMountedRef.current = false;
      clearReconnectTimer();
      clearInactivityTimer();
      stopScan();
      cleanupSubscriptions();
      if (deviceRef.current) {
        sinkRef.current?.onDeviceLost("unmount");
        deviceRef.current.cancelConnection();
      }
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
    batterySubscriptionRef.current?.remove();
    batterySubscriptionRef.current = null;
    disconnectSubscriptionRef.current?.remove();
    disconnectSubscriptionRef.current = null;
  };

  const stopScan = () => {
    if (isScanningRef.current) {
      manager.stopDeviceScan();
      isScanningRef.current = false;
    }
  };

  const cleanupConnection = async (reason: string) => {
    cleanupSubscriptions();
    clearInactivityTimer();
    const device = deviceRef.current;
    deviceRef.current = null;
    setBatteryLevel(null);
    if (device) {
      // Tear-downs that bypass onDisconnected (adapter off, inactivity
      // reconnect) still end the sensor stream — the sink must hear about it
      // or the recording keeps a stale handle and logs no gap.
      sinkRef.current?.onDeviceLost(reason);
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
      await cleanupConnection(`reconnect-${reason}`);
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

    manager.startDeviceScan(null, null, (error, device) => {
      if (error) {
        console.error("❌ Scan Error:", error);
        setStatus("Error");
        stopScan();
        requestReconnect("scan-error");
        return;
      }

      const isPolarApp = device?.name?.includes("Polar mobile");
      const isTargetDevice =
        !isPolarApp &&
        ((device?.name &&
          (device.name.includes("HeartCast") ||
            device.name.includes("Polar"))) ||
          (device?.serviceUUIDs && device.serviceUUIDs.includes(HR_SERVICE_UUID)));

      if (isTargetDevice && device) {
        console.log("✅ Target Found:", device.name);
        stopScan();
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

      await connectedDevice.discoverAllServicesAndCharacteristics();
      cleanupSubscriptions();
      deviceRef.current = connectedDevice;
      reconnectAttemptsRef.current = 0;

      // Check for PMD Service
      let hasPmd = false;
      try {
        const services = await connectedDevice.services();
        hasPmd = services.some(
          (s) => s.uuid.toLowerCase() === PMD_SERVICE_UUID.toLowerCase(),
        );
      } catch (e) {
        console.warn("⚠️ Failed to list services for PMD check:", e);
      }
      hasPmdRef.current = hasPmd;

      // Read Battery Level (0x2A19)
      try {
        const battChar = await connectedDevice.readCharacteristicForService(
          BATTERY_SERVICE_UUID,
          BATTERY_CHARACTERISTIC_UUID,
        );
        if (battChar?.value) {
          const batt = parseBatteryLevel(battChar.value);
          setBatteryLevel(batt);
          sinkRef.current?.onBattery?.(batt);
        }
      } catch (e) {
        // Battery service not present on all devices
      }

      // Monitor Battery characteristic notifications if supported
      try {
        batterySubscriptionRef.current = connectedDevice.monitorCharacteristicForService(
          BATTERY_SERVICE_UUID,
          BATTERY_CHARACTERISTIC_UUID,
          (error, characteristic) => {
            if (!error && characteristic?.value) {
              const batt = parseBatteryLevel(characteristic.value);
              setBatteryLevel(batt);
              sinkRef.current?.onBattery?.(batt);
            }
          },
        );
      } catch (e) {
        // Ignored
      }

      disconnectSubscriptionRef.current = connectedDevice.onDisconnected(
        (error) => {
          console.warn("🔌 Disconnected:", error);
          clearInactivityTimer();
          setStatus("Scanning");
          setBpm(0);
          setBatteryLevel(null);
          sinkRef.current?.onDeviceLost("disconnected");
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

      // Notify sink that device is ready and whether PMD is available
      sinkRef.current?.onDeviceReady(connectedDevice, hasPmd);

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
      const { bpm: heartRate, rrIntervalsMs } = parseHeartRateMeasurement(base64Value);
      setBpm(heartRate);
      resetInactivityTimer();
      sinkRef.current?.onHeartRate(heartRate, rrIntervalsMs, Date.now());
    } catch (error) {
      console.warn("Parse Error:", error);
    }
  };

  return { bpm, status, batteryLevel };
}
