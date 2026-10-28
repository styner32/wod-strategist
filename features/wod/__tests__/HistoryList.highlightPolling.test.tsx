import React from "react";
import { Alert, AppState, type AppStateStatus } from "react-native";
import { act, cleanup, fireEvent, render } from "@testing-library/react-native";
import { HistoryList } from "../ui/HistoryList";
import type { AnalysisResult, HighlightResult } from "../history";

const mockFetchHighlightResults = jest.fn();
const mockGenerateHighlight = jest.fn();
let mockFocused = true;

jest.mock("@/features/i18n", () => ({
  t: (key: string) => key,
  useLocale: () => "en",
}));
jest.mock("@/hooks/use-color-scheme", () => ({ useColorScheme: () => "light" }));
jest.mock("@/store/useProfileStore", () => ({ useProfileId: () => 1 }));
jest.mock("@/store/useVideoQueue", () => ({
  useVideoQueue: (selector: (state: any) => any) => selector({ items: [] }),
}));
jest.mock("@/store/useMergeStatus", () => ({
  useHasPendingMerge: () => false,
  useMergeStatus: Object.assign(jest.fn(), {
    getState: () => ({}),
    subscribe: () => () => {},
  }),
}));
jest.mock("../api", () => ({
  fetchHighlightResults: (...args: unknown[]) => mockFetchHighlightResults(...args),
  generateHighlight: (...args: unknown[]) => mockGenerateHighlight(...args),
  fetchRelatedWods: jest.fn().mockResolvedValue({ related: [] }),
  archiveHistory: jest.fn(),
  fetchHighlightDownloadURL: jest.fn(),
  fetchVideoDownloadURL: jest.fn(),
  generateHardSub: jest.fn(),
  mergeChunks: jest.fn(),
  retryAnalysis: jest.fn(),
}));
jest.mock("expo-router", () => ({ router: { setParams: jest.fn() } }));
jest.mock("expo-router/react-navigation", () => ({ useIsFocused: () => mockFocused }));
jest.mock("expo-file-system/legacy", () => ({ deleteAsync: jest.fn() }));
jest.mock("expo-media-library/legacy", () => ({}));
jest.mock("@/features/video/OriginalVideosPending", () => ({ OriginalVideosPending: () => null }));
jest.mock("../ui/WODTrainingCard", () => ({ WODTrainingCard: () => null }));
jest.mock("../ui/WorkoutProgressCard", () => ({ WorkoutProgressCard: () => null }));
jest.mock("../../environment/EnvironmentCard", () => ({ EnvironmentHistoryCard: () => null }));
jest.mock("../ui/ActivitySummaryCard", () => ({ ActivitySummaryCard: () => null }));
jest.mock("../ui/HeartRateSummaryCard", () => ({ HeartRateSummaryCard: () => null }));
jest.mock("../ui/RelatedWodsCard", () => ({ RelatedWodsCard: () => null }));
jest.mock("../ui/StretchRecommendationsCard", () => ({ StretchRecommendationsCard: () => null }));
jest.mock("../ui/WorkoutFatigueCard", () => ({
  WorkoutFatigueCard: () => null,
  getFatigueColor: () => "#FFFFFF",
}));
jest.mock("../ui/HighlightVideoPlayer", () => ({ HighlightVideoPlayer: () => null }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const item: AnalysisResult = {
  id: 101,
  profile_id: 1,
  session_id: "WOD-20261028-01KABCDEFGH1234567890",
  analysis_type: "wod",
  status: "COMPLETED",
  output: "Workout analysis",
  highlight_segments: '[{"start_secs":0,"end_secs":10}]',
  created_at: "2026-10-28T00:00:00.000Z",
  updated_at: "2026-10-28T00:00:00.000Z",
};

function highlight(status: string, title = "Highlight Reel"): HighlightResult {
  return {
    id: 1,
    session_id: item.session_id,
    profile_id: 1,
    title,
    status,
    gcs_uri: "gs://videos/highlight.mp4",
    music_gcs_uri: "",
    segments: "[]",
    duration_sec: 10,
    output: "",
    created_at: item.created_at,
    updated_at: item.updated_at,
  };
}

const POLL_MS = 5_000;
const DEADLINE_MS = 5 * 60 * 1_000;

async function advance(ms: number) {
  await act(async () => {
    await jest.advanceTimersByTimeAsync(ms);
  });
}

async function showHistory() {
  const view = render(<HistoryList data={[item]} loading={false} />);
  await act(async () => {});
  return view;
}

async function createHighlights(view: Awaited<ReturnType<typeof showHistory>>) {
  await act(async () => {
    fireEvent.press(view.getByText("historyList.createHighlights"));
  });
}

describe("HistoryList highlight request lifecycle", () => {
  let appStateListeners: Set<(state: AppStateStatus) => void>;
  let alertSpy: jest.SpyInstance;

  beforeEach(() => {
    jest.useFakeTimers();
    jest.setSystemTime(new Date("2026-10-28T00:10:00.000Z"));
    mockFocused = true;
    AppState.currentState = "active";
    mockFetchHighlightResults.mockReset().mockResolvedValue([]);
    mockGenerateHighlight.mockReset().mockResolvedValue({ task_id: "task-1" });
    alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    appStateListeners = new Set();
    jest.spyOn(AppState, "addEventListener").mockImplementation((_event, listener) => {
      appStateListeners.add(listener);
      return { remove: () => appStateListeners.delete(listener) };
    });
  });

  afterEach(() => {
    cleanup();
    jest.clearAllTimers();
    jest.useRealTimers();
    jest.restoreAllMocks();
  });

  function changeAppState(state: AppStateStatus) {
    act(() => {
      AppState.currentState = state;
      appStateListeners.forEach((listener) => listener(state));
    });
  }

  it("aborts an unfinished create on unmount and ignores its late success", async () => {
    const create = deferred<unknown>();
    mockGenerateHighlight.mockReturnValue(create.promise);
    const view = await showHistory();
    await createHighlights(view);
    const signal = mockGenerateHighlight.mock.calls[0][3] as AbortSignal;
    expect(signal.aborted).toBe(false);

    view.unmount();
    expect(signal.aborted).toBe(true);
    await act(async () => create.resolve({ task_id: "late-task" }));
    await advance(DEADLINE_MS + POLL_MS);

    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(1);
    expect(alertSpy).not.toHaveBeenCalled();
  });

  it("ignores a create rejection delivered after unmount", async () => {
    const create = deferred<unknown>();
    mockGenerateHighlight.mockReturnValue(create.promise);
    const view = await showHistory();
    await createHighlights(view);
    view.unmount();

    await act(async () => create.reject(new Error("late network failure")));
    await advance(DEADLINE_MS + POLL_MS);

    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(1);
    expect(alertSpy).not.toHaveBeenCalled();
  });

  it("aborts the initial results query on unmount and ignores its late result", async () => {
    const results = deferred<HighlightResult[]>();
    mockFetchHighlightResults.mockReturnValue(results.promise);
    const view = await showHistory();
    const signal = mockFetchHighlightResults.mock.calls[0][1] as AbortSignal;

    view.unmount();
    expect(signal.aborted).toBe(true);
    await act(async () => results.resolve([highlight("COMPLETED")]));
    await advance(DEADLINE_MS + POLL_MS);

    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(1);
    expect(alertSpy).not.toHaveBeenCalled();
  });

  it("aborts an in-flight poll on unmount and ignores late failure results", async () => {
    const results = deferred<HighlightResult[]>();
    const view = await showHistory();
    mockFetchHighlightResults.mockReturnValue(results.promise);
    await createHighlights(view);
    await advance(POLL_MS);
    const signal = mockFetchHighlightResults.mock.calls[1][1] as AbortSignal;

    view.unmount();
    expect(signal.aborted).toBe(true);
    await act(async () => results.resolve([highlight("FAILED")]));
    await advance(DEADLINE_MS + POLL_MS);

    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);
    expect(alertSpy).not.toHaveBeenCalled();
  });

  it("stops repeated query errors at the five-minute deadline", async () => {
    const view = await showHistory();
    mockFetchHighlightResults.mockRejectedValue(new Error("offline"));
    await createHighlights(view);
    await advance(DEADLINE_MS - 1);
    expect(mockFetchHighlightResults.mock.calls.length).toBeGreaterThan(2);
    expect(alertSpy).not.toHaveBeenCalled();
    expect(view.getByText("historyList.generatingHighlights")).toBeTruthy();

    await advance(1);
    expect(alertSpy).toHaveBeenCalledTimes(1);
    expect(alertSpy).toHaveBeenCalledWith("historyList.timeout", "historyList.timeoutDesc");
    expect(view.getByText("historyList.createHighlights")).toBeTruthy();
    const callsAtDeadline = mockFetchHighlightResults.mock.calls.length;
    await advance(DEADLINE_MS);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(callsAtDeadline);
    expect(alertSpy).toHaveBeenCalledTimes(1);
  });

  it("aborts a hung results query at the deadline and ignores a late completion", async () => {
    const results = deferred<HighlightResult[]>();
    const view = await showHistory();
    mockFetchHighlightResults.mockReturnValue(results.promise);
    await createHighlights(view);
    await advance(POLL_MS);
    const signal = mockFetchHighlightResults.mock.calls[1][1] as AbortSignal;
    await advance(DEADLINE_MS - POLL_MS);

    expect(signal.aborted).toBe(true);
    expect(view.getByText("historyList.createHighlights")).toBeTruthy();
    expect(alertSpy).toHaveBeenCalledWith("historyList.timeout", "historyList.timeoutDesc");
    await act(async () => results.resolve([highlight("COMPLETED", "Late highlight")]));
    await advance(DEADLINE_MS);
    expect(view.queryByText("Late highlight")).toBeNull();
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);
    expect(alertSpy).toHaveBeenCalledTimes(1);
  });

  it("times out and aborts a hung create before polling can start", async () => {
    const create = deferred<unknown>();
    mockGenerateHighlight.mockReturnValue(create.promise);
    const view = await showHistory();
    await createHighlights(view);
    const signal = mockGenerateHighlight.mock.calls[0][3] as AbortSignal;
    await advance(DEADLINE_MS);

    expect(signal.aborted).toBe(true);
    expect(view.getByText("historyList.createHighlights")).toBeTruthy();
    expect(alertSpy).toHaveBeenCalledWith("historyList.timeout", "historyList.timeoutDesc");
    await act(async () => create.resolve({ task_id: "too-late" }));
    await advance(DEADLINE_MS);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(1);
    expect(alertSpy).toHaveBeenCalledTimes(1);
  });

  it("allows retry after timeout without letting the old create restart polling", async () => {
    const oldCreate = deferred<unknown>();
    mockGenerateHighlight.mockReturnValueOnce(oldCreate.promise);
    const view = await showHistory();
    await createHighlights(view);
    await advance(DEADLINE_MS);

    await createHighlights(view);
    expect(mockGenerateHighlight).toHaveBeenCalledTimes(2);
    await act(async () => oldCreate.resolve({ task_id: "expired-task" }));
    mockFetchHighlightResults.mockResolvedValue([highlight("COMPLETED", "Retry highlight")]);
    await advance(POLL_MS);

    expect(view.getByText("Retry highlight")).toBeTruthy();
    await advance(DEADLINE_MS);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);
    expect(alertSpy).toHaveBeenCalledTimes(1);
  });

  it("renders completed highlights and stops polling", async () => {
    const view = await showHistory();
    mockFetchHighlightResults.mockResolvedValue([highlight("COMPLETED")]);
    await createHighlights(view);
    await advance(POLL_MS);

    expect(view.getByText("Highlight Reel")).toBeTruthy();
    expect(view.getByText("historyList.regenerate")).toBeTruthy();
    expect(view.queryByText("historyList.generatingHighlights")).toBeNull();
    await advance(DEADLINE_MS);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);
    expect(alertSpy).not.toHaveBeenCalled();
  });

  it("reports terminal generation failure once and stops polling", async () => {
    const view = await showHistory();
    mockFetchHighlightResults.mockResolvedValue([highlight("FAILED")]);
    await createHighlights(view);
    await advance(POLL_MS);

    expect(view.getByText("historyList.createHighlights")).toBeTruthy();
    expect(alertSpy).toHaveBeenCalledWith("historyList.generationFailed", "historyList.generationFailedDesc");
    await advance(DEADLINE_MS);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);
    expect(alertSpy).toHaveBeenCalledTimes(1);
  });

  it("waits for a slow query to settle before scheduling another", async () => {
    const results = deferred<HighlightResult[]>();
    const view = await showHistory();
    mockFetchHighlightResults.mockReturnValueOnce(results.promise).mockResolvedValue([]);
    await createHighlights(view);
    await advance(POLL_MS * 4);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);

    await act(async () => results.resolve([highlight("PROCESSING")]));
    await advance(POLL_MS - 1);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);
    await advance(1);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(3);
  });

  it("does not start queries while the history screen is unfocused", async () => {
    mockFocused = false;
    const view = await showHistory();
    await advance(POLL_MS * 2);
    expect(mockFetchHighlightResults).not.toHaveBeenCalled();

    mockFocused = true;
    view.rerender(<HistoryList data={[item]} loading={false} />);
    await act(async () => {});
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(1);
  });

  it("waits for an active app before starting the initial query", async () => {
    AppState.currentState = "background";
    await showHistory();
    await advance(POLL_MS * 2);
    expect(mockFetchHighlightResults).not.toHaveBeenCalled();

    changeAppState("active");
    await act(async () => {});
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(1);
  });

  it("pauses polling and timeout alerts while blurred, retaining the original deadline", async () => {
    const results = deferred<HighlightResult[]>();
    const view = await showHistory();
    mockFetchHighlightResults.mockReturnValue(results.promise);
    await createHighlights(view);
    await advance(POLL_MS);
    const signal = mockFetchHighlightResults.mock.calls[1][1] as AbortSignal;

    mockFocused = false;
    view.rerender(<HistoryList data={[item]} loading={false} />);
    expect(signal.aborted).toBe(true);
    await advance(DEADLINE_MS);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);
    expect(alertSpy).not.toHaveBeenCalled();

    mockFocused = true;
    view.rerender(<HistoryList data={[item]} loading={false} />);
    await act(async () => {});
    expect(alertSpy).toHaveBeenCalledWith("historyList.timeout", "historyList.timeoutDesc");
    expect(view.getByText("historyList.createHighlights")).toBeTruthy();
    expect(mockGenerateHighlight).toHaveBeenCalledTimes(1);
  });

  it("resumes after backgrounding without repeating create or consuming stale results", async () => {
    const oldResults = deferred<HighlightResult[]>();
    const view = await showHistory();
    mockFetchHighlightResults.mockReturnValueOnce(oldResults.promise).mockResolvedValue([]);
    await createHighlights(view);
    await advance(POLL_MS);
    const oldSignal = mockFetchHighlightResults.mock.calls[1][1] as AbortSignal;

    changeAppState("background");
    expect(oldSignal.aborted).toBe(true);
    await advance(POLL_MS * 3);
    expect(mockFetchHighlightResults).toHaveBeenCalledTimes(2);
    expect(alertSpy).not.toHaveBeenCalled();

    changeAppState("active");
    await advance(POLL_MS);
    expect(mockFetchHighlightResults.mock.calls.length).toBeGreaterThan(2);
    await act(async () => oldResults.resolve([highlight("COMPLETED", "Stale highlight")]));
    expect(view.queryByText("Stale highlight")).toBeNull();
    expect(view.getByText("historyList.generatingHighlights")).toBeTruthy();
    expect(mockGenerateHighlight).toHaveBeenCalledTimes(1);

    mockFetchHighlightResults.mockResolvedValue([highlight("COMPLETED", "Current highlight")]);
    await advance(POLL_MS);
    expect(view.getByText("Current highlight")).toBeTruthy();
    expect(alertSpy).not.toHaveBeenCalled();
  });
});
