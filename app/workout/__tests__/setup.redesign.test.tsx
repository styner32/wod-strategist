import { RECENT_WORKOUT_SETUP_KEY } from "@/features/wod/workoutType";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import React from "react";
import { Alert } from "react-native";
import WorkoutSetup from "../setup";

const mockRouterBack = jest.fn();
const mockRouterPush = jest.fn();
const mockFetchMovementGroups = jest.fn();
const mockFetchPreWodAdvice = jest.fn();
const mockFetchRelatedWods = jest.fn();

const mockStorageMap = new Map<string, string>();

jest.mock("@react-native-async-storage/async-storage", () => ({
  getItem: jest.fn((key: string) =>
    Promise.resolve(mockStorageMap.get(key) ?? null),
  ),
  setItem: jest.fn((key: string, val: string) => {
    mockStorageMap.set(key, val);
    return Promise.resolve();
  }),
  removeItem: jest.fn((key: string) => {
    mockStorageMap.delete(key);
    return Promise.resolve();
  }),
}));

jest.mock("expo-router", () => ({
  router: {
    back: () => mockRouterBack(),
    push: (...args: unknown[]) => mockRouterPush(...args),
  },
}));

jest.mock("expo-image-picker", () => ({
  launchCameraAsync: jest.fn(),
  launchImageLibraryAsync: jest.fn(),
}));

jest.mock("react-native-vision-camera", () => ({
  useCameraDevice: () => undefined,
}));

jest.mock("react-native-safe-area-context", () => {
  const { View } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    SafeAreaView: ({ children, ...props }: any) => (
      <View {...props}>{children}</View>
    ),
  };
});

jest.mock("@/components/ui/icon-symbol", () => ({
  IconSymbol: () => null,
}));

jest.mock("@/features/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
  useLocale: () => "ko",
}));

jest.mock("@/features/health/useBleHeartRate", () => ({
  getRegisteredBleDevice: jest.fn().mockResolvedValue(null),
  setRegisteredBleDevice: jest.fn().mockResolvedValue(undefined),
}));

jest.mock("@/features/wod/api", () => ({
  fetchMovementGroups: () => mockFetchMovementGroups(),
  fetchPreWodAdvice: (...args: unknown[]) => mockFetchPreWodAdvice(...args),
  fetchRelatedWods: (...args: unknown[]) => mockFetchRelatedWods(...args),
  parseWorkoutImage: jest.fn(),
}));

jest.mock("@/features/wod/ui/PreWodStrategyCard", () => ({
  PreWodStrategyCard: () => null,
}));

jest.mock("@/features/wod/ui/RelatedWodsCard", () => ({
  RelatedWodsCard: () => null,
}));

jest.mock("@/store/useProfileStore", () => ({
  useActiveProfile: () => ({
    id: 7,
    name: "Sunjin",
    fitnessLevel: "rx",
    injuries: [],
  }),
}));

describe("WorkoutSetup redesign", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockStorageMap.clear();
    mockFetchMovementGroups.mockResolvedValue([
      { category: "Barbell", movements: ["Thruster", "Power Snatch", "Snatch"] },
      { category: "Gymnastics", movements: ["Pull-up", "Chest-to-Bar Pull-up"] },
    ]);
    mockFetchPreWodAdvice.mockResolvedValue(null);
    mockFetchRelatedWods.mockResolvedValue({ related: [] });
  });

  it("defaults to Photo scan mode and keeps manual movement catalog behind a secondary optional action", async () => {
    const { getByText, queryByPlaceholderText } = render(<WorkoutSetup />);

    await waitFor(() => expect(mockFetchMovementGroups).toHaveBeenCalled());

    // Photo mode is active by default
    expect(getByText("setup.photoPrimaryTitle")).toBeTruthy();
    expect(getByText("setup.takePhoto")).toBeTruthy();
    expect(getByText("setup.chooseFromGallery")).toBeTruthy();
    expect(queryByPlaceholderText("setup.searchMovements")).toBeNull();

    // Open secondary movements picker
    fireEvent.press(getByText("setup.secondaryMovementsBtn"));
    expect(getByText("setup.backToWodInput")).toBeTruthy();

    // Return back to primary Photo input
    fireEvent.press(getByText("setup.backToWodInput"));
    expect(getByText("setup.photoPrimaryTitle")).toBeTruthy();
  });

  it("auto-extracts movements from pre-entered WOD text, saves for later (24h), and restores in 1 tap", async () => {
    const alertSpy = jest.spyOn(Alert, "alert");
    const { getByText, getByPlaceholderText, getByTestId, unmount } = render(
      <WorkoutSetup />,
    );

    await waitFor(() => expect(mockFetchMovementGroups).toHaveBeenCalled());

    // Switch to Text tab and pre-enter today's WOD
    fireEvent.press(getByText("setup.inputMethodText"));
    fireEvent.changeText(
      getByPlaceholderText("setup.wodTextPlaceholder"),
      "For Time: 21-15-9 Thrusters (43kg) and Pull-ups",
    );

    // Save for later
    await act(async () => {
      fireEvent.press(getByTestId("save-workout-for-later-text-btn"));
    });

    expect(AsyncStorage.setItem).toHaveBeenCalledWith(
      RECENT_WORKOUT_SETUP_KEY,
      expect.any(String),
    );
    const savedJson = JSON.parse(mockStorageMap.get(RECENT_WORKOUT_SETUP_KEY)!);
    expect(savedJson.profileId).toBe(7);
    expect(savedJson.selectedMovements).toEqual(["Thruster", "Pull-up"]);

    // Confirm alert navigates back to Home
    const buttons = alertSpy.mock.calls[0][2] as any[];
    buttons.find((b) => b.text === "common.ok").onPress();
    expect(mockRouterBack).toHaveBeenCalledTimes(1);

    unmount();

    // Re-open WorkoutSetup later the same day -> saved WOD card appears and restores to Confirm step
    const reopened = render(<WorkoutSetup />);
    await waitFor(() =>
      expect(reopened.getByTestId("reuse-recent-workout-card")).toBeTruthy(),
    );

    fireEvent.press(reopened.getByTestId("reuse-recent-workout-card"));
    expect(reopened.getByText("setup.confirmTitle")).toBeTruthy();
    expect(reopened.getByText("Thruster")).toBeTruthy();
    expect(reopened.getByText("Pull-up")).toBeTruthy();

    // Verify weightKg was preserved for fetchRelatedWods even when selectedMovements is populated
    await waitFor(() =>
      expect(mockFetchRelatedWods).toHaveBeenCalledWith(
        expect.objectContaining({
          profileId: 7,
          movement: "Thruster",
          weightKg: 43,
        }),
      ),
    );

    alertSpy.mockRestore();
  });
});
