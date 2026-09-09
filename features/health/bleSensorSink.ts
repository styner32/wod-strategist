import type { Device } from "react-native-ble-plx";

/**
 * Interface decoupling BLE connection management (useBleHeartRate)
 * from high-frequency sensor recording and PMD streaming.
 */
export interface BleSensorSink {
  /** Called whenever a connected device handle is ready (scan or reconnect). */
  onDeviceReady(device: Device, hasPmd: boolean): void;
  /** Called when the device disconnects or connection is lost. */
  onDeviceLost(reason: string): void;
  /** Direct push from GATT 0x2A37 parseHeartRate to bypass React state re-render latency. */
  onHeartRate(bpm: number, rrIntervalsMs: number[], receivedAtMs: number, contact?: boolean): void;
  /** Optional battery percentage update from GATT 0x2A19. */
  onBattery?(batteryPercent: number): void;
}
