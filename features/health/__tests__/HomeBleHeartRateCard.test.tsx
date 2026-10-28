import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import React from "react";
import { HomeBleHeartRateCard } from "../HomeBleHeartRateCard";
import type { RegisteredBleDevice } from "../useBleHeartRate";

const mockUseBleHeartRate = jest.fn();
const mockGetRegisteredBleDevice = jest.fn<
  Promise<RegisteredBleDevice | null>,
  []
>();

let focusCleanup: (() => void) | undefined;

jest.mock("expo-router", () => ({
  useFocusEffect: (effect: () => void | (() => void)) => {
    const ReactActual = jest.requireActual<typeof import("react")>("react");
    ReactActual.useEffect(() => {
      const cleanup = effect();
      focusCleanup = typeof cleanup === "function" ? cleanup : undefined;
      return cleanup;
    }, [effect]);
  },
}));

jest.mock("@/components/ui/icon-symbol", () => ({
  IconSymbol: () => null,
}));

jest.mock("@/features/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
  useLocale: () => "ko",
}));

jest.mock("../useBleHeartRate", () => ({
  getRegisteredBleDevice: () => mockGetRegisteredBleDevice(),
  useBleHeartRate: (opts: unknown) => mockUseBleHeartRate(opts),
}));

describe("HomeBleHeartRateCard", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    focusCleanup = undefined;
  });

  it("renders subtle connect chip without enabling BLE scan when no sensor is registered", async () => {
    mockGetRegisteredBleDevice.mockResolvedValue(null);
    mockUseBleHeartRate.mockReturnValue({
      bpm: 0,
      status: "Idle",
      batteryLevel: null,
      connectedDevice: null,
      registeredDevice: null,
      discoveredDevices: [],
      registerDevice: jest.fn(),
      rescanDevices: jest.fn(),
    });

    const { getByTestId, queryByTestId } = render(<HomeBleHeartRateCard />);

    await waitFor(() => expect(mockGetRegisteredBleDevice).toHaveBeenCalled());
    expect(getByTestId("home-ble-connect-chip")).toBeTruthy();
    expect(queryByTestId("home-ble-hr-card")).toBeNull();
    expect(mockUseBleHeartRate).toHaveBeenLastCalledWith({ enabled: false });

    // Tapping the chip enables manual scanning
    fireEvent.press(getByTestId("home-ble-connect-chip"));
    expect(mockUseBleHeartRate).toHaveBeenLastCalledWith({ enabled: true });
  });

  it("auto-connects when a sensor is registered, displays live BPM & battery, and disables BLE on blur", async () => {
    const registered: RegisteredBleDevice = {
      id: "polar-h10-1",
      name: "Polar H10 ABCD",
    };
    mockGetRegisteredBleDevice.mockResolvedValue(registered);
    mockUseBleHeartRate.mockReturnValue({
      bpm: 138,
      status: "Live",
      batteryLevel: 85,
      connectedDevice: registered,
      registeredDevice: registered,
      discoveredDevices: [registered],
      registerDevice: jest.fn(),
      rescanDevices: jest.fn(),
    });

    const { getByTestId, getByText } = render(<HomeBleHeartRateCard />);

    await waitFor(() =>
      expect(mockUseBleHeartRate).toHaveBeenLastCalledWith({ enabled: true }),
    );
    expect(getByTestId("home-ble-hr-card")).toBeTruthy();
    expect(getByText("Polar H10 ABCD")).toBeTruthy();
    expect(getByText("138")).toBeTruthy();
    expect(getByText("85%")).toBeTruthy();

    // Simulate navigating away from Home tab -> must release BLE singleton
    act(() => {
      focusCleanup?.();
    });
    expect(mockUseBleHeartRate).toHaveBeenLastCalledWith({ enabled: false });
  });
});
