import { act, renderHook } from "@testing-library/react-native";
import { fetchActivitySummary, fetchChunkAnalysis, type ChunkAnalysisResult } from "../api";
import { useLiveWorkoutFeedback } from "../useLiveWorkoutFeedback";
import type { ActivitySummary } from "../../../shared/activity";

jest.mock("../api", () => ({ fetchActivitySummary: jest.fn(), fetchChunkAnalysis: jest.fn() }));
jest.mock("../../i18n", () => ({ useLocale: () => "en" }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
const row = (session: string): ChunkAnalysisResult => ({
  id: 1, session_id: session, status: "COMPLETED", output: "Basic cue", start_secs: 0, end_secs: 10,
  created_at: "2026-09-16T00:00:00Z", updated_at: "2026-09-16T00:00:00Z",
  capture_assessment: { state: "needs_adjustment", advice: { ko: "발끝까지", en: "Include feet" } },
});
const summary = (count: number, state: ActivitySummary["review_state"] = "provisional"): ActivitySummary => ({
  version: 1, available: true, source_version: "v1", review_version: 1, review_state: state,
  coverage_scope: "recorded_chunks", movements: [{ movement: "Air Squat", unit: "reps", count, seconds: 0 }], unassessed: [], reviews: [],
});

describe("live feedback polling", () => {
  beforeEach(() => { jest.useFakeTimers(); jest.setSystemTime(10000); jest.resetAllMocks(); });
  afterEach(() => { jest.useRealTimers(); });
  it("delivers camera guidance without waiting for a slow aggregate response", async () => {
    const pending = deferred<ActivitySummary>();
    jest.mocked(fetchChunkAnalysis).mockResolvedValue([row("s")]);
    jest.mocked(fetchActivitySummary).mockReturnValue(pending.promise);
    const session = { current: "s" }, start = { current: 0 };
    const { result, unmount } = renderHook(() => useLiveWorkoutFeedback(true, 1, session, start));
    await act(async () => { await Promise.resolve(); });
    expect(result.current.captureAdvice).toBe("Include feet");
    expect(result.current.coaching).toBe("Basic cue");
    expect(result.current.summary).toBeNull();
    unmount();
  });
  it("ignores old-session results and replaces a provisional total with the reviewed total", async () => {
    const oldChunks = deferred<ChunkAnalysisResult[]>(), oldSummary = deferred<ActivitySummary>();
    jest.mocked(fetchChunkAnalysis).mockReturnValueOnce(oldChunks.promise).mockResolvedValue([row("new")]);
    jest.mocked(fetchActivitySummary).mockReturnValueOnce(oldSummary.promise).mockResolvedValueOnce(summary(1)).mockResolvedValue(summary(2, "completed"));
    const session = { current: "old" }, start = { current: 0 };
    const { result, rerender, unmount } = renderHook(() => useLiveWorkoutFeedback(true, 1, session, start));
    session.current = "new"; rerender({});
    await act(async () => { oldChunks.resolve([row("old")]); oldSummary.resolve(summary(999)); await Promise.resolve(); });
    expect(result.current.summary).toBeNull();
    expect(result.current.coaching).toBeNull();
    await act(async () => { jest.advanceTimersByTime(3000); await Promise.resolve(); });
    expect(result.current.summary?.movements[0].count).toBe(1);
    await act(async () => { jest.advanceTimersByTime(3000); await Promise.resolve(); });
    expect(result.current.summary?.movements[0].count).toBe(2);
    expect(result.current.summary?.review_state).toBe("completed");
    unmount();
  });
  it("clears display and aborts in-flight requests when recording stops", async () => {
    const pending = deferred<ChunkAnalysisResult[]>();
    jest.mocked(fetchChunkAnalysis).mockReturnValue(pending.promise);
    jest.mocked(fetchActivitySummary).mockResolvedValue(summary(1));
    const session = { current: "s" }, start = { current: 0 };
    const { result, rerender } = renderHook(({ recording }: { recording: boolean }) => useLiveWorkoutFeedback(recording, 1, session, start), { initialProps: { recording: true } });
    await act(async () => { await Promise.resolve(); });
    const signal = jest.mocked(fetchChunkAnalysis).mock.calls[0][1];
    rerender({ recording: false });
    expect(signal?.aborted).toBe(true);
    await act(async () => { pending.resolve([row("s")]); await Promise.resolve(); });
    expect(result.current.summary).toBeNull(); expect(result.current.captureAdvice).toBeNull();
  });
});
