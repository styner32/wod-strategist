import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import {
  isStaleProcessing,
  ProcessingSection,
} from "../ui/HistoryList";
import type { AnalysisResult } from "../history";

const mockMergeChunks = jest.fn();
const mockAddPending = jest.fn();

jest.mock("@/features/i18n", () => ({
  t: (key: string) => key,
  useLocale: () => "en",
}));

jest.mock("@/hooks/use-color-scheme", () => ({
  useColorScheme: () => "light",
}));

jest.mock("@/store/useProfileStore", () => ({
  useProfileId: () => 1,
}));

jest.mock("@/store/useMergeStatus", () => ({
  useMergeStatus: {
    getState: () => ({
      addPending: mockAddPending,
    }),
  },
  useHasPendingMerge: () => false,
}));

jest.mock("@/store/useVideoQueue", () => ({
  useVideoQueue: (selector: (state: any) => any) => selector({ items: [] }),
}));

jest.mock("../api", () => ({
  mergeChunks: (...args: unknown[]) => mockMergeChunks(...args),
  archiveHistory: jest.fn(),
  fetchHighlightDownloadURL: jest.fn(),
  fetchHighlightResults: jest.fn().mockResolvedValue([]),
  fetchRelatedWods: jest.fn().mockResolvedValue([]),
  fetchVideoDownloadURL: jest.fn(),
  generateHardSub: jest.fn(),
  generateHighlight: jest.fn(),
  retryAnalysis: jest.fn(),
}));

jest.mock("@/features/video/OriginalVideosPending", () => ({
  OriginalVideosPending: () => null,
}));

jest.mock("@/modules/video-merger", () => ({
  VideoMergerModule: { mergeVideos: jest.fn() },
}));

jest.mock("expo-router", () => ({
  router: { setParams: jest.fn() },
}));

jest.mock("expo-file-system/legacy", () => ({
  deleteAsync: jest.fn(),
}));

jest.mock("expo-media-library/legacy", () => ({
  requestPermissionsAsync: jest.fn().mockResolvedValue({ status: "granted" }),
  saveToLibraryAsync: jest.fn(),
}));

jest.mock("expo-video", () => ({
  VideoView: () => null,
  useVideoPlayer: jest.fn(),
}));

describe("isStaleProcessing", () => {
  it("returns false for falsy or invalid createdAt", () => {
    expect(isStaleProcessing("")).toBe(false);
    expect(isStaleProcessing("invalid-date")).toBe(false);
  });

  it("returns false when less than 1 hour has elapsed", () => {
    const now = 1775700000000;
    // 59 minutes ago
    const createdAt = new Date(now - 59 * 60 * 1000).toISOString();
    expect(isStaleProcessing(createdAt, now)).toBe(false);
  });

  it("returns true when 1 hour or more has elapsed", () => {
    const now = 1775700000000;
    // Exactly 60 minutes ago
    const createdAtExact = new Date(now - 60 * 60 * 1000).toISOString();
    expect(isStaleProcessing(createdAtExact, now)).toBe(true);

    // 2 hours ago
    const createdAtOlder = new Date(now - 120 * 60 * 1000).toISOString();
    expect(isStaleProcessing(createdAtOlder, now)).toBe(true);
  });
});

describe("ProcessingSection", () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  const baseItem: AnalysisResult = {
    id: 101,
    profile_id: 1,
    session_id: "WOD-20261008-01KABCDEFGH1234567890",
    status: "PENDING",
    analysis_type: "wod",
    output: "",
    created_at: new Date(Date.now() - 10 * 60 * 1000).toISOString(), // 10 minutes ago
    updated_at: new Date(Date.now() - 10 * 60 * 1000).toISOString(),
  };

  it("returns null when items and local queue are both empty", () => {
    const view = render(<ProcessingSection items={[]} />);
    expect(view.toJSON()).toBeNull();
  });

  it("renders pending item without re-merge button when under 1 hour", () => {
    const recentItem: AnalysisResult = {
      ...baseItem,
      created_at: new Date(Date.now() - 30 * 60 * 1000).toISOString(), // 30 mins ago
    };

    const view = render(<ProcessingSection items={[recentItem]} />);
    expect(view.getByText("historyList.processingSection")).toBeTruthy();
    expect(view.getByText("historyList.aiAnalyzing")).toBeTruthy();
    expect(view.queryByText("historyList.remerge")).toBeNull();
  });

  it("renders re-merge button when processing has been stuck for over 1 hour", () => {
    const staleItem: AnalysisResult = {
      ...baseItem,
      created_at: new Date(Date.now() - 70 * 60 * 1000).toISOString(), // 70 mins ago
    };

    const view = render(<ProcessingSection items={[staleItem]} />);
    expect(view.getByText("historyList.processingSection")).toBeTruthy();
    expect(view.getByText("historyList.remerge")).toBeTruthy();
  });

  it("triggers confirmation alert when re-merge button is pressed and executes mergeChunks upon confirmation", async () => {
    const staleItem: AnalysisResult = {
      ...baseItem,
      created_at: new Date(Date.now() - 90 * 60 * 1000).toISOString(),
    };

    const onRefreshMock = jest.fn();
    mockMergeChunks.mockResolvedValueOnce({ status: "ok" });

    const alertSpy = jest.spyOn(Alert, "alert");

    const view = render(
      <ProcessingSection items={[staleItem]} onRefresh={onRefreshMock} />,
    );

    const remergeBtn = view.getByText("historyList.remerge");
    fireEvent.press(remergeBtn);

    expect(alertSpy).toHaveBeenCalledWith(
      "historyList.remergeConfirmTitle",
      "historyList.remergeConfirmBody",
      expect.any(Array),
    );

    // Find confirm action in the alert buttons
    const buttons = alertSpy.mock.calls[0][2] as any[];
    const confirmButton = buttons.find(
      (b) => b.text === "historyList.remergeAction",
    );
    expect(confirmButton).toBeDefined();

    // Trigger the confirm action
    await act(async () => {
      await confirmButton.onPress();
    });

    expect(mockMergeChunks).toHaveBeenCalledWith(staleItem.session_id, {
      profileId: 1,
      workoutType: "wod",
    });
    expect(mockAddPending).toHaveBeenCalledWith(staleItem.session_id);
    expect(alertSpy).toHaveBeenCalledWith(
      "upload.success",
      "historyList.remergeStarted",
    );
    expect(onRefreshMock).toHaveBeenCalledTimes(1);
  });
});
