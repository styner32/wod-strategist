import { Buffer } from "buffer";
import { useEffect, useRef, useState } from "react";
import { PermissionsAndroid, Platform } from "react-native";
import { BleManager, Device, State } from "react-native-ble-plx";

// [중요] Manager는 컴포넌트 밖에서 한 번만 생성 (메모리 릭 방지)
const manager = new BleManager();

const HR_SERVICE_UUID = "180D";
const HR_CHARACTERISTIC_UUID = "2A37";

export function useBleHeartRate() {
  const [bpm, setBpm] = useState(0);
  const [status, setStatus] = useState<
    "Init" | "Scanning" | "Connecting" | "Live" | "Error"
  >("Init");
  const deviceRef = useRef<Device | null>(null);

  useEffect(() => {
    // 1. 블루투스 상태 감지 (PoweredOn 될 때까지 대기)
    const subscription = manager.onStateChange((state) => {
      console.log("🔹 BLE State:", state); // 로그 확인 필수

      if (state === State.PoweredOn) {
        startScan();
        subscription.remove(); // 한 번 켜지면 리스너 해제
      }
    }, true); // true: 현재 상태 즉시 검사

    return () => {
      // 클린업: 스캔 중단 및 연결 해제
      manager.stopDeviceScan();
      deviceRef.current?.cancelConnection();
      subscription.remove();
    };
  }, []);

  const startScan = async () => {
    // Android 권한 요청 (iOS는 Info.plist 자동 처리됨)
    if (Platform.OS === "android") {
      const granted = await PermissionsAndroid.requestMultiple([
        PermissionsAndroid.PERMISSIONS.BLUETOOTH_SCAN,
        PermissionsAndroid.PERMISSIONS.BLUETOOTH_CONNECT,
        PermissionsAndroid.PERMISSIONS.ACCESS_FINE_LOCATION,
      ]);
      if (granted["android.permission.BLUETOOTH_SCAN"] !== "granted") {
        console.warn("BLE Permission Denied");
        return;
      }
    }

    console.log("🚀 Scanning started...");
    setStatus("Scanning");

    // [핵심] UUID 자리에 null을 넣어 "모든 기기"를 다 찾습니다.
    // HeartCast가 UUID를 숨기고 광고할 수 있기 때문입니다.
    manager.startDeviceScan(null, null, (error, device) => {
      if (error) {
        console.error("❌ Scan Error:", error);
        setStatus("Error");
        return;
      }

      // 로그로 발견된 기기 이름 확인 (디버깅용)
      // if (device?.name) console.log("Found:", device.name);

      // [필터링] HeartCast(앱) 또는 Polar(심박계) 찾기
      // HeartCast는 보통 이름에 'Heart'가 들어가거나, 180D 서비스 UUID를 가짐
      const isTargetDevice =
        (device?.name &&
          (device.name.includes("HeartCast") ||
            device.name.includes("Polar"))) ||
        (device?.serviceUUIDs && device.serviceUUIDs.includes(HR_SERVICE_UUID));

      if (isTargetDevice && device) {
        console.log("✅ Target Found:", device.name);
        manager.stopDeviceScan(); // 찾으면 스캔 즉시 중단
        connectToDevice(device);
      }
    });
  };

  const connectToDevice = async (device: Device) => {
    try {
      setStatus("Connecting");
      console.log(`🔗 Connecting to ${device.name}...`);

      const connectedDevice = await device.connect();
      console.log("🔗 Connected. Discovering services...");

      // [필수] 서비스 및 특성 검색
      await connectedDevice.discoverAllServicesAndCharacteristics();
      deviceRef.current = connectedDevice;

      console.log("❤️ Monitoring Heart Rate...");
      connectedDevice.monitorCharacteristicForService(
        HR_SERVICE_UUID,
        HR_CHARACTERISTIC_UUID,
        (error, characteristic) => {
          if (error) {
            console.error("Monitor Error:", error);
            return;
          }
          if (characteristic?.value) {
            parseHeartRate(characteristic.value);
          }
        },
      );
      setStatus("Live");
    } catch (e) {
      console.error("❌ Connection Failed:", e);
      setStatus("Error");
      // 실패 시 재스캔 로직을 넣을 수도 있음
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
    } catch (error) {
      console.warn("Parse Error:", error);
    }
  };

  return { bpm, status };
}
