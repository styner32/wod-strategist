import { fireEvent, render, waitFor } from "@testing-library/react-native";
import React from "react";
import { StyleSheet } from "react-native";
import Dashboard from "../index";

const mockFetchProfileReadiness = jest.fn();
const mockRouterPush = jest.fn();

jest.mock("expo-router", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  return {
    router: { push: (...args: unknown[]) => mockRouterPush(...args) },
    useFocusEffect: (effect: () => void | (() => void)) => {
      ReactActual.useEffect(effect, [effect]);
    },
  };
});

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
  setLanguage: jest.fn(),
}));

jest.mock("@/features/health/HomeBleHeartRateCard", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    HomeBleHeartRateCard: () => <Text>mock-ble-hr-card</Text>,
  };
});

jest.mock("@/features/wod/api", () => ({
  fetchProfileReadiness: (...args: unknown[]) =>
    mockFetchProfileReadiness(...args),
}));

jest.mock("@/store/useProfileStore", () => ({
  useActiveProfile: () => ({
    id: 7,
    name: "Sunjin",
    gender: "male",
    birthYear: 1990,
    heightCm: 178,
    weightKg: 76,
    fitnessLevel: "rx",
    injuries: ["Left Shoulder"],
  }),
  useProfileId: () => 7,
  useProfileStore: (selector: (s: any) => any) =>
    selector({ loadProfiles: jest.fn().mockResolvedValue(undefined) }),
}));

describe("Home Dashboard", () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it("renders profile, BLE HR status, readiness card, and single Start Workout CTA without dev copy or duplicate menu cards", async () => {
    mockFetchProfileReadiness.mockResolvedValue({
      profile_id: 7,
      overall_fatigue_score: 42,
      overall_state: "moderate",
      overall_state_ko: "보통",
      evidence_status: "complete",
      evidence: {
        total_sessions: 2,
        valid_sessions: 2,
        excluded_sessions: 0,
        unresolved_time_sessions: 0,
      },
      as_of: new Date().toISOString(),
      last_workout_at: new Date(Date.now() - 3600_000).toISOString(),
      muscle_readiness: [],
      recent_workouts: [],
    });

    const { getByTestId, getByText, queryByText } = render(<Dashboard />);

    await waitFor(() =>
      expect(mockFetchProfileReadiness).toHaveBeenCalledWith(
        7,
        expect.anything(),
      ),
    );

    // Active profile + BLE HR + readiness + single Start Workout CTA
    expect(getByText('home.greeting:{"name":"Sunjin"}')).toBeTruthy();
    expect(getByText("mock-ble-hr-card")).toBeTruthy();
    expect(getByTestId("home-readiness-card")).toBeTruthy();
    const startCta = getByTestId("home-start-workout-cta");
    expect(startCta).toBeTruthy();
    expect(StyleSheet.flatten(startCta.props.style)).toEqual(
      expect.objectContaining({
        flexDirection: "row",
        backgroundColor: "#091E28",
        borderColor: "#00E5FF",
      }),
    );
    expect(getByText("home.startWorkout")).toBeTruthy();

    fireEvent.press(startCta);
    expect(mockRouterPush).toHaveBeenCalledWith("/workout/setup");

    // Removed dev/duplicate items must not appear
    expect(queryByText("home.subtitle")).toBeNull();
    expect(queryByText(/Ready to verify AI Engine/i)).toBeNull();
    expect(queryByText("home.devZone")).toBeNull();
    expect(queryByText("home.uploadVideo")).toBeNull();
    expect(queryByText("home.videoQueue")).toBeNull();
    expect(queryByText("home.workoutHistory")).toBeNull();
  });
});
