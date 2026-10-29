import { fireEvent, render } from "@testing-library/react-native";
import React from "react";
import ProfilesScreen from "../profiles";

const mockRouterPush = jest.fn();
const mockRouterBack = jest.fn();
const mockSelectProfile = jest.fn();
const mockArchiveProfile = jest.fn();

jest.mock("expo-router", () => ({
  router: {
    push: (...args: unknown[]) => mockRouterPush(...args),
    back: (...args: unknown[]) => mockRouterBack(...args),
  },
}));

jest.mock("react-native-safe-area-context", () => {
  const { View } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    SafeAreaProvider: ({ children }: any) => <View>{children}</View>,
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

jest.mock("@/store/useProfileStore", () => ({
  useProfileStore: () => ({
    profiles: [
      {
        id: 7,
        name: "Sunjin",
        gender: "male",
        birthYear: 1990,
        heightCm: 178,
        weightKg: 76,
        fitnessLevel: "rx",
        injuries: [],
      },
      {
        id: 8,
        name: "Mina",
        gender: "female",
        birthYear: 1994,
        heightCm: 165,
        weightKg: 58,
        fitnessLevel: "intermediate",
        injuries: [],
      },
    ],
    activeProfileId: 7,
    selectProfile: (...args: unknown[]) => mockSelectProfile(...args),
    archiveProfile: (...args: unknown[]) => mockArchiveProfile(...args),
  }),
}));

describe("ProfilesScreen Roster (Phase 2 Light Bento)", () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it("renders roster count badge, active Volt badge, switch/edit pills, and + Add New Profile CTA", () => {
    const { getByTestId, getByText, queryByTestId } = render(
      <ProfilesScreen />,
    );

    expect(getByText("profiles.title")).toBeTruthy();
    expect(getByText('profiles.rosterCount:{"count":2}')).toBeTruthy();
    expect(getByText("Sunjin")).toBeTruthy();
    expect(getByText("Mina")).toBeTruthy();
    expect(getByText("common.active")).toBeTruthy();

    // Active profile does not render Switch button; inactive profile does
    expect(queryByTestId("switch-profile-7")).toBeNull();
    const switchBtn = getByTestId("switch-profile-8");
    fireEvent.press(switchBtn);
    expect(mockSelectProfile).toHaveBeenCalledWith(8);
    expect(mockRouterBack).toHaveBeenCalledTimes(1);

    // Edit button routes to /profile-edit with id param (avoiding /(tabs)/profile collision)
    fireEvent.press(getByTestId("edit-profile-8"));
    expect(mockRouterPush).toHaveBeenCalledWith({
      pathname: "/profile-edit",
      params: { id: "8" },
    });

    // Add New Profile Volt CTA routes to /profile-edit
    fireEvent.press(getByTestId("add-profile-btn"));
    expect(mockRouterPush).toHaveBeenCalledWith("/profile-edit");
  });
});
