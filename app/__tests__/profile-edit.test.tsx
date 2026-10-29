import { fireEvent, render, waitFor } from "@testing-library/react-native";
import React from "react";
import ProfileScreen from "../profile-edit";

const mockRouterBack = jest.fn();
const mockUpdateProfile = jest.fn().mockResolvedValue(undefined);
const mockCreateProfile = jest.fn().mockResolvedValue(undefined);

jest.mock("expo-router", () => ({
  router: {
    back: (...args: unknown[]) => mockRouterBack(...args),
  },
  useLocalSearchParams: () => ({ id: "7" }),
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
  t: (key: string) => key,
  useLocale: () => "ko",
}));

jest.mock("@/features/wod/api", () => ({
  fetchInjuries: jest.fn().mockResolvedValue(["Left Shoulder", "Knee"]),
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
        restingHrBpm: 52,
        fitnessLevel: "advanced",
        injuries: ["Left Shoulder"],
        appearance: "Black tee",
      },
    ],
    updateProfile: (...args: unknown[]) => mockUpdateProfile(...args),
    createProfile: (...args: unknown[]) => mockCreateProfile(...args),
  }),
}));

describe("ProfileScreen (/profile-edit)", () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it("loads existing athlete fields by id, updates profile, and pops back on save or back button", async () => {
    const { getByDisplayValue, getByTestId, getByText } = render(
      <ProfileScreen />,
    );

    expect(getByText("profileEdit.editProfile")).toBeTruthy();
    expect(getByDisplayValue("Sunjin")).toBeTruthy();
    expect(getByDisplayValue("1990")).toBeTruthy();
    expect(getByDisplayValue("52")).toBeTruthy();

    fireEvent.press(getByTestId("profile-edit-back-btn"));
    expect(mockRouterBack).toHaveBeenCalledTimes(1);

    fireEvent.changeText(getByDisplayValue("Sunjin"), "Sunjin Lee");
    fireEvent.press(getByTestId("save-profile-btn"));

    await waitFor(() =>
      expect(mockUpdateProfile).toHaveBeenCalledWith(
        7,
        expect.objectContaining({
          name: "Sunjin Lee",
          birth_year: 1990,
          resting_hr_bpm: 52,
          fitness_level: "advanced",
        }),
      ),
    );
    expect(mockRouterBack).toHaveBeenCalledTimes(2);
  });
});
