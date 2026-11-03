import { fireEvent, render, waitFor } from "@testing-library/react-native";
import React from "react";
import { StyleSheet } from "react-native";
import { listItemsByRelPath } from "@/features/debug/storageScanner";
import ProfileTab from "../profile";

const mockRouterPush = jest.fn();
const mockGetVideoCacheStats = jest.fn();
const DEFAULT_MAX_BYTES = 1.5 * 1024 * 1024 * 1024;

jest.mock("expo-router", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  return {
    router: {
      push: (...args: unknown[]) => mockRouterPush(...args),
      replace: jest.fn(),
    },
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
}));

jest.mock("@/features/auth/useAuthStore", () => ({
  useAuthStore: {
    getState: () => ({ logout: jest.fn().mockResolvedValue(undefined) }),
  },
}));

jest.mock("@/features/debug/storageScanner", () => ({
  deleteAllByRelPath: jest.fn(),
  deleteItemByRelPath: jest.fn(),
  extractCacheToDocuments: jest.fn(),
  extractDirToDocuments: jest.fn(),
  getContainerDirs: jest.fn().mockReturnValue([]),
  listItemsByRelPath: jest.fn().mockResolvedValue([]),
  runStorageScanAndShare: jest.fn(),
}));

jest.mock("@/features/video/videoCacheManager", () => ({
  DEFAULT_MAX_CACHE_BYTES: 1.5 * 1024 * 1024 * 1024,
  clearAllVideoCache: jest.fn().mockResolvedValue(undefined),
  getVideoCacheStats: (...args: unknown[]) => mockGetVideoCacheStats(...args),
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
    restingHrBpm: 52,
    injuries: ["Left Shoulder"],
  }),
  useProfileId: () => 7,
  useProfileStore: (selector: (s: any) => any) =>
    selector({
      profiles: [{ id: 7, name: "Sunjin" }, { id: 8, name: "Coach" }],
      clearActiveProfile: jest.fn(),
    }),
}));

describe("ProfileTab (Phase 2 Light Bento)", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetVideoCacheStats.mockResolvedValue({
      count: 3,
      totalSizeBytes: DEFAULT_MAX_BYTES * 0.4,
      formattedSize: "614.4 MB",
      entries: [],
    });
  });

  it("renders real Athlete Profile Bento, Video Cache progress gauge, and App Store Delete Account without fake telemetry", async () => {
    const { getByTestId, getByText, queryByText } = render(<ProfileTab />);

    await waitFor(() => expect(mockGetVideoCacheStats).toHaveBeenCalled());

    // Tactical Header & Active Athlete Hero Bento
    expect(getByText("profileTab.eyebrow")).toBeTruthy();
    expect(getByText("profileTab.activeAthlete")).toBeTruthy();
    expect(getByText("Sunjin")).toBeTruthy();
    expect(getByText("SU")).toBeTruthy();
    expect(getByText("profileEdit.rx")).toBeTruthy();
    expect(
      getByText('♥ profileTab.restingHrBadge:{"bpm":52}'),
    ).toBeTruthy();
    expect(getByText("Left Shoulder")).toBeTruthy();

    // Never render fake Stitch telemetry
    expect(queryByText(/VO2 Peak/i)).toBeNull();
    expect(queryByText(/Total Volume/i)).toBeNull();
    expect(queryByText(/WOD Score/i)).toBeNull();

    // Video Cache progress bar reflects real 40% usage against DEFAULT_MAX_CACHE_BYTES
    const progressFill = getByTestId("video-cache-progress-fill");
    expect(StyleSheet.flatten(progressFill.props.style)).toEqual(
      expect.objectContaining({
        width: "40%",
        backgroundColor: "#003EC6",
      }),
    );
    expect(getByText("40%")).toBeTruthy();

    // Account navigation actions
    fireEvent.press(getByTestId("profile-edit-btn"));
    expect(mockRouterPush).toHaveBeenCalledWith({
      pathname: "/profile-edit",
      params: { id: "7" },
    });

    fireEvent.press(getByTestId("profile-switch-btn"));
    expect(mockRouterPush).toHaveBeenCalledWith("/profiles");

    // Mandatory App Store Delete Account compliance
    fireEvent.press(getByTestId("delete-account-btn"));
    expect(mockRouterPush).toHaveBeenCalledWith("/settings/deleteAccount");
  });

  it("shows storage item sizes in the correct byte, KB, MB and GB units", async () => {
    jest.mocked(listItemsByRelPath).mockResolvedValueOnce([
      { name: "small.json", isDirectory: false, sizeBytes: 512 },
      { name: "one-kb.json", isDirectory: false, sizeBytes: 1024 },
      { name: "notes.json", isDirectory: false, sizeBytes: 100 * 1024 },
      { name: "one-mb.mp4", isDirectory: false, sizeBytes: 1024 * 1024 },
      { name: "one-gb.mp4", isDirectory: false, sizeBytes: 1024 * 1024 * 1024 },
    ]);
    const { getByText } = render(<ProfileTab />);

    fireEvent.press(getByText("Manage Documents"));

    await waitFor(() => expect(getByText("100.0 KB")).toBeTruthy());
    expect(listItemsByRelPath).toHaveBeenCalledWith("Documents");
    for (const size of ["512 B", "1.0 KB", "1.0 MB", "1.00 GB"]) {
      expect(getByText(size)).toBeTruthy();
    }
  });
});
