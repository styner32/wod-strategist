import { useEffect, useRef, useState } from "react";
import { PermissionsAndroid, Platform } from "react-native";
import { BleManager, Device, State, Subscription } from "react-native-ble-plx";

import { HeartRateQuality, type HeartRateReading } from "./heartRateQuality";
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

export const REGISTERED_BLE_HR_DEVICE_KEY = "wod_registered_ble_hr_device";

export interface RegisteredBleDevice {
  id: string;
  name: string;
}

let cachedRegisteredDevice: RegisteredBleDevice | null = null;
const registeredDeviceListeners = new Set<(device: RegisteredBleDevice | null) => void>();

function getStorage(): {
  getItem: (key: string) => Promise<string | null>;
  setItem: (key: string, value: string) => Promise<void>;
  removeItem: (key: string) => Promise<void>;
} | null {
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const mod = require("@react-native-async-storage/async-storage");
    return mod?.default ?? mod ?? null;
  } catch {
    return null;
  }
}

export async function getRegisteredBleDevice(): Promise<RegisteredBleDevice | null> {
  try {
    const storage = getStorage();
    if (!storage) return cachedRegisteredDevice;
    const raw = await storage.getItem(REGISTERED_BLE_HR_DEVICE_KEY);
    if (!raw) {
      cachedRegisteredDevice = null;
      return null;
    }
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed.id === "string" && parsed.id.length > 0) {
      cachedRegisteredDevice = {
        id: parsed.id,
        name: typeof parsed.name === "string" && parsed.name.length > 0 ? parsed.name : parsed.id,
      };
      return cachedRegisteredDevice;
    }
  } catch {
    // Fallback to in-memory cache when AsyncStorage is unavailable
  }
  return cachedRegisteredDevice;
}

export async function setRegisteredBleDevice(device: RegisteredBleDevice | null): Promise<void> {
  cachedRegisteredDevice = device;
  for (const listener of registeredDeviceListeners) {
    listener(device);
  }
  try {
    const storage = getStorage();
    if (!storage) return;
    if (device) {
      await storage.setItem(REGISTERED_BLE_HR_DEVICE_KEY, JSON.stringify(device));
    } else {
      await storage.removeItem(REGISTERED_BLE_HR_DEVICE_KEY);
    }
  } catch {
    // Ignore storage persistence errors in non-native environments
  }
}

export function isHeartRateCandidateDevice(
  device: Pick<Device, "name" | "serviceUUIDs"> | null | undefined,
): boolean {
  if (!device) return false;
  const isPolarApp = device?.name?.includes("Polar mobile");
  return Boolean(
    !isPolarApp &&
      ((device?.name &&
        (device.name.includes("HeartCast") ||
          device.name.includes("Polar"))) ||
        (device?.serviceUUIDs && device.serviceUUIDs.includes(HR_SERVICE_UUID))),
  );
}

export function matchesRegisteredBleDevice(
  device: Pick<Device, "id" | "name"> | null | undefined,
  registered: RegisteredBleDevice | null | undefined,
): boolean {
  if (!device) return false;
  if (!registered) return true;
  if (device.id === registered.id) return true;
  if (registered.name && device.name && device.name === registered.name) return true;
  return false;
}

export interface UseBleHeartRateOptions {
  enabled?: boolean;
  sink?: BleSensorSink;
  recording?: boolean;
  paused?: boolean;
  onReading?: (reading: HeartRateReading) => void;
}

export function useBleHeartRate(options?: UseBleHeartRateOptions) {
  const enabled = options?.enabled ?? true;
  const enabledRef = useRef(enabled);
  enabledRef.current = enabled;

  const [reading, setReading] = useState<HeartRateReading>({ bpm: null, reason: "missing", receivedAt: 0 });
  const quality = useRef(new HeartRateQuality());
  const latestReading = useRef(reading);
  const onReadingRef = useRef(options?.onReading);
  onReadingRef.current = options?.onReading;
  const publish = (next: HeartRateReading) => {
    latestReading.current = next;
    setReading(next);
    onReadingRef.current?.(next);
  };
  const getReading = (): HeartRateReading => {
    const current = latestReading.current;
    return Date.now() - current.receivedAt >= 5000
      ? { ...current, bpm: null, reason: "missing" } : current;
  };
  const resetQuality = (recovering = false) => {
    quality.current.reset(recovering);
    publish({ bpm: null, reason: "missing", receivedAt: 0 });
  };
  useEffect(() => { resetQuality(); }, [options?.recording, options?.paused]);
  useEffect(() => {
    const timer = setInterval(() => {
      if (latestReading.current.reason !== "missing" && Date.now() - latestReading.current.receivedAt >= 5000) {
        quality.current.markMissing();
        publish({ bpm: null, reason: "missing", receivedAt: 0 });
      }
    }, 250);
    return () => clearInterval(timer);
  }, []);
  const [batteryLevel, setBatteryLevel] = useState<number | null>(null);
  const [status, setStatus] = useState<
    "Init" | "Scanning" | "Connecting" | "Live" | "Error"
  >("Init");
  const [connectedDevice, setConnectedDevice] = useState<RegisteredBleDevice | null>(null);
  const [registeredDevice, setRegisteredDevice] = useState<RegisteredBleDevice | null>(
    cachedRegisteredDevice,
  );
  const [discoveredDevices, setDiscoveredDevices] = useState<RegisteredBleDevice[]>([]);

  const registeredDeviceRef = useRef<RegisteredBleDevice | null>(cachedRegisteredDevice);
  const discoveredDevicesRef = useRef<Map<string, Device>>(new Map());
  const sinkRef = useRef<BleSensorSink | undefined>(options?.sink);
  const hasPmdRef = useRef(false);

  const recordDiscoveredDevice = (device: Device) => {
    if (!device.id) return;
    discoveredDevicesRef.current.set(device.id, device);
    const entry: RegisteredBleDevice = {
      id: device.id,
      name: device.name || device.id,
    };
    setDiscoveredDevices((prev) => {
      const existingIdx = prev.findIndex((item) => item.id === entry.id);
      if (existingIdx >= 0) {
        if (prev[existingIdx].name === entry.name) return prev;
        const next = [...prev];
        next[existingIdx] = entry;
        return next;
      }
      return [...prev, entry];
    });
  };

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
  // The strap that just dropped, kept so the reconnect can target it directly:
  // an unfiltered scan finds nothing while iOS runs the app in the background.
  const lostDeviceRef = useRef<Device | null>(null);
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

  const applyRegisteredDeviceSelection = async (next: RegisteredBleDevice | null) => {
    registeredDeviceRef.current = next;
    if (isMountedRef.current) {
      setRegisteredDevice(next);
    }
    if (!isMountedRef.current || !enabledRef.current) return;

    // If connected to a device that no longer matches the registered target, disconnect and switch
    if (next && deviceRef.current && !matchesRegisteredBleDevice(deviceRef.current, next)) {
      await cleanupConnection("switch-registered-device");
    }

    if (!deviceRef.current && !isConnectingRef.current) {
      if (next) {
        for (const candidate of discoveredDevicesRef.current.values()) {
          if (matchesRegisteredBleDevice(candidate, next)) {
            stopScan();
            await connectToDevice(candidate);
            return;
          }
        }
      }
      if (!isScanningRef.current && bleStateRef.current === State.PoweredOn) {
        await startScan();
      }
    }
  };

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    const listener = (next: RegisteredBleDevice | null) => {
      void applyRegisteredDeviceSelection(next);
    };
    registeredDeviceListeners.add(listener);
    void getRegisteredBleDevice().then((stored) => {
      if (isMountedRef.current && stored !== registeredDeviceRef.current) {
        void applyRegisteredDeviceSelection(stored);
      }
    });
    return () => {
      registeredDeviceListeners.delete(listener);
    };
  }, []);

  useEffect(() => {
    if (!enabled) {
      lostDeviceRef.current = null;
      clearReconnectTimer();
      clearInactivityTimer();
      stopScan();
      void cleanupConnection("disabled");
      setStatus("Init");
      return;
    }

    // 1. 블루투스 상태 감지 (PoweredOn 될 때까지 대기)
    const subscription = manager.onStateChange((state) => {
      if (!enabledRef.current || !isMountedRef.current) return;
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
      lostDeviceRef.current = null;
      clearReconnectTimer();
      clearInactivityTimer();
      stopScan();
      cleanupSubscriptions();
      if (deviceRef.current) {
        const activeDevice = deviceRef.current;
        deviceRef.current = null;
        sinkRef.current?.onDeviceLost(isMountedRef.current ? "disabled" : "unmount");
        void Promise.resolve(activeDevice.cancelConnection()).catch(() => {});
      }
      if (isMountedRef.current) {
        setConnectedDevice(null);
        setBatteryLevel(null);
        resetQuality(true);
        setStatus("Init");
      }
      subscription.remove();
    };
  }, [enabled]);

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
    resetQuality(true);
    cleanupSubscriptions();
    clearInactivityTimer();
    const device = deviceRef.current;
    deviceRef.current = null;
    setConnectedDevice(null);
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
    if (!isMountedRef.current || !enabledRef.current) return;
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
    if (!isMountedRef.current || !enabledRef.current || isReconnectingRef.current) return;
    isReconnectingRef.current = true;

    try {
      console.log(`♻️ Reconnecting now (${reason})`);
      const lastDevice = deviceRef.current ?? lostDeviceRef.current;
      lostDeviceRef.current = null;
      await cleanupConnection(`reconnect-${reason}`);
      resetQuality(true);

      if (lastDevice && matchesRegisteredBleDevice(lastDevice, registeredDeviceRef.current)) {
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
    if (
      !isMountedRef.current ||
      !enabledRef.current ||
      isScanningRef.current ||
      isConnectingRef.current ||
      deviceRef.current
    ) {
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
      !enabledRef.current ||
      (bleStateRef.current && bleStateRef.current !== State.PoweredOn)
    ) {
      isScanningRef.current = false;
      return;
    }

    console.log("🚀 Scanning started...");
    setStatus("Scanning");

    manager.startDeviceScan(null, null, (error, device) => {
      if (!enabledRef.current || !isMountedRef.current) {
        stopScan();
        return;
      }
      if (error) {
        console.error("❌ Scan Error:", error);
        setStatus("Error");
        stopScan();
        requestReconnect("scan-error");
        return;
      }

      const isPolarApp = device?.name?.includes("Polar mobile");
      const isTargetDevice = !isPolarApp && isHeartRateCandidateDevice(device);

      if (isTargetDevice && device) {
        recordDiscoveredDevice(device);
        if (!matchesRegisteredBleDevice(device, registeredDeviceRef.current)) {
          return;
        }
        console.log("✅ Target Found:", device.name);
        stopScan();
        connectToDevice(device);
      }
    });
  };

  const connectToDevice = async (device: Device) => {
    if (!isMountedRef.current || !enabledRef.current || isConnectingRef.current) return;
    isConnectingRef.current = true;
    clearReconnectTimer();
    stopScan();

    try {
      setStatus("Connecting");
      console.log(`🔗 Connecting to ${device.name}...`);

      const connected = await device.connect({ timeout: CONNECTION_TIMEOUT_MS });
      if (!isMountedRef.current || !enabledRef.current) {
        void Promise.resolve(connected.cancelConnection()).catch(() => {});
        return;
      }
      console.log("🔗 Connected. Discovering services...");

      await connected.discoverAllServicesAndCharacteristics();
      if (!isMountedRef.current || !enabledRef.current) {
        void Promise.resolve(connected.cancelConnection()).catch(() => {});
        return;
      }
      cleanupSubscriptions();
      deviceRef.current = connected;
      lostDeviceRef.current = null;
      recordDiscoveredDevice(connected);
      setConnectedDevice({
        id: connected.id,
        name: connected.name || connected.id,
      });
      reconnectAttemptsRef.current = 0;

      // Check for PMD Service
      let hasPmd = false;
      try {
        const services = await connected.services();
        hasPmd = services.some(
          (s) => s.uuid.toLowerCase() === PMD_SERVICE_UUID.toLowerCase(),
        );
      } catch (e) {
        console.warn("⚠️ Failed to list services for PMD check:", e);
      }
      hasPmdRef.current = hasPmd;

      // Read Battery Level (0x2A19)
      try {
        const battChar = await connected.readCharacteristicForService(
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
        batterySubscriptionRef.current = connected.monitorCharacteristicForService(
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

      disconnectSubscriptionRef.current = connected.onDisconnected(
        (error) => {
          console.warn("🔌 Disconnected:", error);
          clearInactivityTimer();
          lostDeviceRef.current = connected;
          deviceRef.current = null;
          setConnectedDevice(null);
          setStatus("Scanning");
          resetQuality(true);
          setBatteryLevel(null);
          sinkRef.current?.onDeviceLost("disconnected");
          requestReconnect("disconnected");
        },
      );

      console.log("❤️ Monitoring Heart Rate...");
      monitorSubscriptionRef.current = connected.monitorCharacteristicForService(
        HR_SERVICE_UUID,
        HR_CHARACTERISTIC_UUID,
        (error, characteristic) => {
          if (error) {
            resetQuality(true);
            console.error("Monitor Error:", error);
            setStatus("Error");
            requestReconnect("monitor-error");
            return;
          }
          if (characteristic) {
            parseHeartRate(characteristic.value ?? "");
          }
        },
      );

      // Notify sink that device is ready and whether PMD is available
      sinkRef.current?.onDeviceReady(connected, hasPmd);

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

  const registerDevice = async (device: RegisteredBleDevice | null) => {
    await setRegisteredBleDevice(device);
  };

  const rescanDevices = async () => {
    lostDeviceRef.current = null;
    clearReconnectTimer();
    stopScan();
    await cleanupConnection("manual-rescan");
    if (!enabledRef.current || !isMountedRef.current) return;
    await startScan();
  };

  const parseHeartRate = (base64Value: string) => {
    let measurement: ReturnType<typeof parseHeartRateMeasurement>;
    try {
      measurement = parseHeartRateMeasurement(base64Value);
    } catch (error) {
      console.warn("Parse Error:", error);
      measurement = { bpm: 0, rrIntervalsMs: [] };
    }
    const { bpm: heartRate, rrIntervalsMs, contact } = measurement;
    const receivedAt = Date.now();
    publish(quality.current.update(receivedAt, heartRate, contact));
    resetInactivityTimer();
    try {
      sinkRef.current?.onHeartRate(heartRate, rrIntervalsMs, receivedAt, contact);
    } catch (error) {
      console.warn("Sensor recording error:", error);
    }
  };

  return {
    bpm: reading.bpm ?? 0,
    quality: reading.reason,
    getReading,
    resetQuality,
    status,
    batteryLevel,
    connectedDevice,
    registeredDevice,
    discoveredDevices,
    registerDevice,
    rescanDevices,
  };
}
