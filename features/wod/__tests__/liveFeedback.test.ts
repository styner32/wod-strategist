import { captureAdviceAt, coachingText, latestCaptureResult } from "../liveFeedback";
import { activityReviewPending, activitySummaryPath, type ActivitySummary } from "../../../shared/activity";
import type { ChunkAnalysisResult } from "../api";

const chunk = (id: number, end: number, session = "s"): ChunkAnalysisResult => ({
  id, session_id: session, status: "COMPLETED", output: "basic", start_secs: end - 10, end_secs: end,
  created_at: "2026-09-16T00:00:00Z", updated_at: "2026-09-16T00:00:00Z",
  capture_assessment: { state: "needs_adjustment", issue: "framing", advice: { ko: "발끝까지 보이게 해주세요", en: "Include your feet" } },
});

describe("live feedback capture ordering", () => {
  it("uses capture time instead of completion ID and ignores older responses", () => {
    const newer = chunk(1, 30), older = chunk(2, 20);
    expect(latestCaptureResult(null, [older, newer], "s")).toBe(newer);
    expect(latestCaptureResult(newer, [older], "s")).toBe(newer);
  });
  it("rejects old-session and missing-time evidence", () => {
    expect(latestCaptureResult(chunk(1, 10), [chunk(2, 20, "s")], "new")).toBeNull();
    expect(latestCaptureResult(null, [{ ...chunk(1, 10), end_secs: undefined }], "s")).toBeNull();
  });
  it("expires by capture time even when coaching updates arrive later", () => {
    const row = chunk(1, 10);
    expect(captureAdviceAt(row, 40, "ko")).toBe("발끝까지 보이게 해주세요");
    row.updated_at = "2026-09-16T00:10:00Z";
    expect(captureAdviceAt(row, 40.01, "ko")).toBeNull();
    expect(captureAdviceAt(row, 9, "ko")).toBeNull();
  });
  it("clears advice on good, unknown, or failed newer evidence", () => {
    const previous = chunk(1, 10);
    const good = { ...chunk(2, 20), capture_assessment: { state: "good" as const, advice: { ko: "", en: "" } } };
    expect(captureAdviceAt(latestCaptureResult(previous, [good], "s"), 25, "en")).toBeNull();
    expect(captureAdviceAt({ ...chunk(3, 30), status: "FAILED" }, 35, "ko")).toBeNull();
  });
  it("displays current contextual coaching in both locales without changing independent observations", () => {
    const row = chunk(1, 10);
    row.contextual_coaching = { current_chunk_id: 1, source_chunk_ids: [9], sources: [], text: { ko: "연결 코칭", en: "Connected cue" } };
    const original = JSON.stringify(row);
    expect(coachingText(row, "en")).toBe("Connected cue");
    expect(coachingText(row, "ko")).toBe("연결 코칭");
    expect(JSON.stringify(row)).toBe(original);
    expect(row.output).toBe("basic");
    expect(row.contextual_coaching.source_chunk_ids).toEqual([9]);
  });
  it("falls back to independent coaching when the contextual current-chunk source does not match", () => {
    const row = chunk(1, 10);
    row.contextual_coaching = { current_chunk_id: 2, source_chunk_ids: [9], sources: [], text: { ko: "다른 구간", en: "Different chunk" } };
    expect(coachingText(row, "en")).toBe("basic");
    expect(coachingText(row, "ko")).toBe("basic");
    row.output = "";
    expect(coachingText(row, "ko")).toBeNull();
  });
  it("uses raw current output when contextual text is unavailable in the selected locale", () => {
    const row = chunk(1, 10);
    row.contextual_coaching = { current_chunk_id: 1, source_chunk_ids: [9], sources: [], text: { ko: "", en: "   " } };
    expect(coachingText(row, "en")).toBe("basic");
    expect(coachingText(row, "ko")).toBe("basic");
    row.contextual_coaching = null;
    expect(coachingText(row, "en")).toBe("basic");
    row.output = "";
    expect(coachingText(row, "en")).toBeNull();
  });
  it("does not display coaching from failed or unfinished observations", () => {
    const row = chunk(1, 10);
    row.contextual_coaching = { current_chunk_id: 1, source_chunk_ids: [9], sources: [], text: { ko: "연결 코칭", en: "Connected cue" } };
    expect(coachingText({ ...row, status: "FAILED" }, "en")).toBeNull();
    expect(coachingText({ ...row, status: "PENDING" }, "ko")).toBeNull();
    expect(coachingText(null, "ko")).toBeNull();
  });
  it("polls provisional reviews but stops at completed or partial reviews", () => {
    const summary = { available: true, review_state: "provisional" } as ActivitySummary;
    expect(activityReviewPending(summary)).toBe(true);
    expect(activityReviewPending({ ...summary, review_state: "completed" })).toBe(false);
    expect(activityReviewPending({ ...summary, review_state: "partial" })).toBe(false);
    expect(activitySummaryPath("session/one", 3)).toBe("/sessions/session%2Fone/activity-summary?profile_id=3");
  });
});
