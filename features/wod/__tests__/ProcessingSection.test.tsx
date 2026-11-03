import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import {
  isStaleProcessing,
  ProcessingSection,
} from "../ui/HistoryList";
import { useMergeStatus } from "@/store/useMergeStatus";
import { parseRecentWorkoutSetup, RECENT_WORKOUT_TTL_MS } from "../workoutType";
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
    useMergeStatus.getState().resetRemergeState();
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
    expect(
      useMergeStatus
        .getState()
        .pending.some((p) => p.sessionId === staleItem.session_id),
    ).toBe(true);
    expect(alertSpy).toHaveBeenCalledWith(
      "upload.success",
      "historyList.remergeStarted",
    );
    expect(onRefreshMock).toHaveBeenCalledTimes(1);
    alertSpy.mockRestore();
  });

  it("queues multiple re-merge requests, keeps buttons disabled across refresh, and dispatches one by one as each completes", async () => {
    const item1: AnalysisResult = {
      ...baseItem,
      id: 201,
      session_id: "WOD-20261020-01SESSION00000000000000001",
      created_at: new Date(Date.now() - 90 * 60 * 1000).toISOString(),
    };
    const item2: AnalysisResult = {
      ...baseItem,
      id: 202,
      session_id: "WOD-20261020-01SESSION00000000000000002",
      created_at: new Date(Date.now() - 95 * 60 * 1000).toISOString(),
    };
    const item3: AnalysisResult = {
      ...baseItem,
      id: 203,
      session_id: "WOD-20261020-01SESSION00000000000000003",
      created_at: new Date(Date.now() - 100 * 60 * 1000).toISOString(),
    };

    mockMergeChunks.mockResolvedValue({ status: "ok" });
    const alertSpy = jest.spyOn(Alert, "alert");

    const view = render(<ProcessingSection items={[item1, item2, item3]} />);

    // Press re-merge on all 3 items and confirm each
    for (let i = 0; i < 3; i++) {
      const remergeButtons = view.getAllByText("historyList.remerge");
      fireEvent.press(remergeButtons[0]);
      const alertCalls = alertSpy.mock.calls;
      const lastCall = alertCalls[alertCalls.length - 1];
      const buttons = lastCall[2] as any[];
      const confirmBtn = buttons.find((b) => b.text === "historyList.remergeAction");
      await act(async () => {
        await confirmBtn.onPress();
      });
    }

    // Only the first video is dispatched immediately; the other two wait in queue
    expect(mockMergeChunks).toHaveBeenCalledTimes(1);
    expect(mockMergeChunks).toHaveBeenLastCalledWith(item1.session_id, {
      profileId: 1,
      workoutType: "wod",
    });

    // Even after a refresh with the same items, no active "historyList.remerge" button remains
    view.rerender(<ProcessingSection items={[item1, item2, item3]} />);
    expect(view.queryByText("historyList.remerge")).toBeNull();
    expect(view.getByText("historyList.remergeInProgress")).toBeTruthy();
    expect(view.getAllByText("historyList.remergeQueuedWithPosition")).toHaveLength(2);

    // When item1 finishes and disappears from pending items on refresh, item2 is automatically dispatched
    await act(async () => {
      view.rerender(<ProcessingSection items={[item2, item3]} />);
    });
    await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(2));
    expect(mockMergeChunks).toHaveBeenLastCalledWith(item2.session_id, {
      profileId: 1,
      workoutType: "wod",
    });

    // And item2 & item3 buttons are STILL disabled (no duplicate re-merge button)
    expect(view.queryByText("historyList.remerge")).toBeNull();

    // When item2 finishes, item3 is automatically dispatched
    await act(async () => {
      view.rerender(<ProcessingSection items={[item3]} />);
    });
    await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(3));
    expect(mockMergeChunks).toHaveBeenLastCalledWith(item3.session_id, {
      profileId: 1,
      workoutType: "wod",
    });

    alertSpy.mockRestore();
  });
});

describe("parseRecentWorkoutSetup (24-hour pre-workout setup reuse)", () => {
  const now = 1775700000000;
  const sample = {
    savedAt: now - 25 * 60 * 1000, // 25 minutes ago
    profileId: 7,
    workoutType: "wod",
    wodDescription: "For Time: 21-15-9 Thrusters + Pull-ups",
    rawText: "21-15-9 Thrusters (43kg) Pull-ups",
    selectedMovements: ["Thruster", "Pull-up"],
    customMovements: [],
    sessionAppearance: "Black shirt",
  };

  it("returns the saved workout setup when captured within TTL", () => {
    const parsed = parseRecentWorkoutSetup(JSON.stringify(sample), now);
    expect(parsed).toEqual(sample);
  });

  it("returns null when the saved workout setup is older than TTL", () => {
    const expired = {
      ...sample,
      savedAt: now - RECENT_WORKOUT_TTL_MS - 1000,
    };
    expect(parseRecentWorkoutSetup(JSON.stringify(expired), now)).toBeNull();
  });

  it("returns null for a snapshot that does not name the profile it belongs to", () => {
    const { profileId: _profileId, ...unscoped } = sample;
    expect(parseRecentWorkoutSetup(JSON.stringify(unscoped), now)).toBeNull();
  });
});
