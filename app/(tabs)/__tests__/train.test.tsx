import { fireEvent, render } from "@testing-library/react-native";
import React from "react";
import TrainScreen from "../train";

const mockRouterPush = jest.fn();

jest.mock("expo-router", () => ({
  router: { push: (...args: unknown[]) => mockRouterPush(...args) },
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

jest.mock("@/store/useVideoQueue", () => ({
  useVideoQueue: (selector: (s: any) => any) =>
    selector({ items: [{ id: "q1" }, { id: "q2" }] }),
}));

describe("TrainScreen (Phase 2 Light Bento)", () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it("renders Tactical Top Bar, Bento action cards, live queue count badge, and routes cleanly", () => {
    const { getByTestId, getByText } = render(<TrainScreen />);

    expect(getByText("train.eyebrow")).toBeTruthy();
    expect(getByText("train.heroTitle")).toBeTruthy();
    expect(getByText("2")).toBeTruthy();

    fireEvent.press(getByTestId("train-start-workout-card"));
    expect(mockRouterPush).toHaveBeenCalledWith("/workout/setup");

    fireEvent.press(getByTestId("train-upload-card"));
    expect(mockRouterPush).toHaveBeenCalledWith("/upload");

    fireEvent.press(getByTestId("train-queue-card"));
    expect(mockRouterPush).toHaveBeenCalledWith("/queue");
  });
});
