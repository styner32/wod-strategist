jest.mock("react", () => jest.requireActual("../../node_modules/react"));
import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RecognitionReviewPanel } from "../src/history/components/RecognitionReviewPanel";
import { recognitionReviewInputSeconds, recognitionReviewSelection } from "../src/history/recognitionReview";
import { historyApi, type RecognitionEvaluation, type RecognitionRecordEvaluation, type RecognitionReviewRun } from "../src/api/history";
import type { PerformanceRecord } from "../../shared/workoutProgress";
jest.mock("../src/api/history", () => ({ historyApi: {
  getRecognitionEvaluation: jest.fn(), createRecognitionReview: jest.fn(), listRecognitionReviews: jest.fn(), getRecognitionReview: jest.fn(),
} }));
const { act, create } = require("react-test-renderer");
const record: PerformanceRecord = { id: 1, session_id: "session", interval_id: "set-1", source_version: "source", status: "confirmed", movement: "Power Clean", start_secs: 10, end_secs: 40, reps: 5, load: null, load_kg: null, measurement: "set", technique: "singles", workout_prescription: "test", reps_per_minute: 10, rate_basis: "normalized_per_minute", median_cycle_seconds: null, performed_at: null, confirmed_at: "2026-10-28T00:00:00Z" };
const row: RecognitionRecordEvaluation = { record, predicted_movements: [{ movement: "Deadlift", reps: 3 }], observed_complete_reps: 3, movement_match: false, reps_delta: -2, evidence_complete: true, status: "mismatch", gaps: [], source_refs: [] };
const missing: RecognitionRecordEvaluation = { ...row, record: { ...record, id: 2, start_secs: 50, end_secs: 70, reps: 0 }, predicted_movements: [], observed_complete_reps: null, movement_match: null, reps_delta: null, evidence_complete: false, status: "insufficient_evidence", gaps: [{ start_secs: 50, end_secs: 70, reason: "not_analyzed", clock: "media" }] };
const evaluation: RecognitionEvaluation = { source_version: "source", fingerprint: "fingerprint", records: [row, missing], summary: { total: 2, matched: 0, mismatch: 1, insufficient_evidence: 1 } };
const run: RecognitionReviewRun = {
  id: 9, status: "COMPLETED", source_version: "source", evaluation_fingerprint: "fingerprint", record_ids: [1], start_secs: 0, end_secs: 70, model: "test-model", prompt_version: "v1", schema_version: "v1", outdated: false, input_tokens: 0, output_tokens: null, total_tokens: null, created_at: "2026-10-28T00:00:00Z", updated_at: "2026-10-28T00:01:00Z", evaluation,
  batches: [{ id: 0, record_id: 1, start_secs: 10, end_secs: 40, core_start_secs: 10, core_end_secs: 40, status: "COMPLETED", findings: [{ start_secs: 20, end_secs: 23, category: "movement_confusion", stage: "deep_observation", difference: "Power Clean을 Deadlift로 분류", original_evidence: "바닥에서 들어 올리는 동작으로 기록됨", visual_evidence: "어깨까지 바벨을 받는 모습이 보임", possible_reason: "받는 순간이 다른 사람에 가려졌을 가능성", limitations: "기존 모델의 내부 판단 이유는 확인 불가", evidence_level: "video_supported_hypothesis" }] }],
};

beforeEach(() => {
  jest.clearAllMocks();
  Object.defineProperty(global, "crypto", { configurable: true, value: { randomUUID: jest.fn(() => "request-id") } });
  (historyApi.getRecognitionEvaluation as jest.Mock).mockResolvedValue(evaluation);
  (historyApi.listRecognitionReviews as jest.Mock).mockResolvedValue({ runs: [] });
  (historyApi.getRecognitionReview as jest.Mock).mockResolvedValue(run);
  (historyApi.createRecognitionReview as jest.Mock).mockResolvedValue({ run_id: 9, status: "QUEUED" });
});
async function flush() { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }
async function mount(extra: Record<string, unknown> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } });
  const seek = jest.fn(); let tree: any;
  const render = (props = {}) => createElement(QueryClientProvider, { client }, createElement(RecognitionReviewPanel, { sessionId: "session", profileId: 1, sourceUpdatedAt: "first", currentTime: 20, duration: 100, isOriginal: true, seek, ...extra, ...props }));
  await act(async () => { tree = create(render()); }); await flush();
  return { tree, client, seek, render, cleanup: async () => { await act(() => tree.unmount()); client.clear(); } };
}
function text(tree: any) {
  const read = (node: any): string => node == null ? "" : typeof node === "string" ? node : Array.isArray(node) ? node.map(read).join("") : read(node.children);
  return read(tree.toJSON());
}
function button(tree: any, label: string) { return tree.root.findAllByType("button").find((item: any) => item.children.join("") === label); }
async function click(tree: any, label: string) { const found = button(tree, label); expect(found).toBeDefined(); await act(async () => found.props.onClick()); await flush(); }
async function input(tree: any, label: string, value: string) { await act(async () => tree.root.findAllByType("input").find((item: any) => item.props["aria-label"] === label).props.onChange({ target: { value } })); await flush(); }

it("compares confirmed records without submitting AI and preserves missing versus zero", async () => {
  const { tree, cleanup } = await mount();
  expect(text(tree)).toContain("확정 2구간"); expect(text(tree)).toContain("Deadlift 3회"); expect(text(tree)).toContain("Power Clean 0회"); expect(text(tree)).toContain("근거 부족");
  expect(text(tree)).toContain("-2회"); expect(text(tree)).toContain("확인 불가");
  expect(historyApi.createRecognitionReview).not.toHaveBeenCalled();
  expect(tree.root.findByProps({ "data-testid": "recognition-review-preview" }).children.join("")).toContain("2구간");
  await cleanup();
});

it("excludes a record crossing the selected start and submits only full eligible records", async () => {
  const { tree, cleanup } = await mount();
  await click(tree, "현재 영상 위치 사용");
  expect(text(tree)).toContain("경계에 걸쳐 제외");
  expect(tree.root.findByProps({ "data-testid": "recognition-review-preview" }).children.join("")).toContain("1구간");
  await click(tree, "선택 구간 오류 진단");
  expect(historyApi.createRecognitionReview).toHaveBeenCalledWith("session", { record_ids: [2], start_secs: 20, end_secs: 70, source_version: "source", evaluation_fingerprint: "fingerprint", client_request_id: "request-id" });
  await cleanup();
});

it("keeps known movement with missing count unknown and labels positive partial counts as confirmed observations", async () => {
  const noCount = { ...row, observed_complete_reps: null, reps_delta: null, evidence_complete: false, status: "insufficient_evidence", predicted_movements: [{ movement: "Power Clean", reps: 0 }], gaps: [{ start_secs: 10, end_secs: 40, reason: "no_countable_evidence" }] };
  const partial = { ...missing, record: { ...missing.record, reps: 4 }, predicted_movements: [{ movement: "Snatch", reps: 2 }] };
  (historyApi.getRecognitionEvaluation as jest.Mock).mockResolvedValue({ ...evaluation, records: [noCount, partial] });
  const { tree, cleanup } = await mount();
  expect(text(tree)).toContain("Power Clean · 횟수 확인 불가"); expect(text(tree)).not.toContain("Power Clean 0회");
  expect(text(tree)).toContain("Snatch · 확인된 2회"); expect(text(tree)).toContain("일부 관찰만 확인됨");
  await cleanup();
});

it("prevents duplicate clicks while a request is pending", async () => {
  let finish!: (value: unknown) => void;
  (historyApi.createRecognitionReview as jest.Mock).mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const { tree, cleanup } = await mount(); const submit = button(tree, "선택 구간 오류 진단");
  await act(async () => { submit.props.onClick(); submit.props.onClick(); });
  expect(historyApi.createRecognitionReview).toHaveBeenCalledTimes(1);
  await act(async () => finish({ run_id: 9, status: "QUEUED" })); await flush(); await cleanup();
});

it("keeps the idempotency key for retrying an uncertain response", async () => {
  (historyApi.createRecognitionReview as jest.Mock).mockRejectedValueOnce(new Error("offline"));
  const { tree, cleanup } = await mount(); await click(tree, "선택 구간 오류 진단"); await click(tree, "선택 구간 오류 진단");
  expect(historyApi.createRecognitionReview).toHaveBeenCalledTimes(2);
  expect((historyApi.createRecognitionReview as jest.Mock).mock.calls[0][1].client_request_id).toBe((historyApi.createRecognitionReview as jest.Mock).mock.calls[1][1].client_request_id);
  await cleanup();
});

it("rejects invalid timestamps and an end beyond the original video", async () => {
  const { tree, cleanup } = await mount();
  await input(tree, "영상 진단 시작시간", "00:80"); expect(button(tree, "선택 구간 오류 진단").props.disabled).toBe(true);
  await input(tree, "영상 진단 시작시간", "0"); await input(tree, "영상 진단 종료시간", "101"); expect(text(tree)).toContain("원본 영상 길이를 넘습니다"); expect(button(tree, "선택 구간 오류 진단").props.disabled).toBe(true);
  expect(historyApi.createRecognitionReview).not.toHaveBeenCalled(); await cleanup();
});

it("loads previous detailed diagnoses and marks outdated partial reports with unknown tokens", async () => {
  const oldRun = { ...run, status: "PARTIAL", outdated: true, evaluation_fingerprint: "old", batches: [...run.batches, { ...run.batches[0], id: 1, status: "FAILED", findings: [], error: "영상 준비 실패" }] };
  (historyApi.listRecognitionReviews as jest.Mock).mockResolvedValue({ runs: [oldRun] }); (historyApi.getRecognitionReview as jest.Mock).mockResolvedValue(oldRun);
  const { tree, seek, cleanup } = await mount();
  expect(text(tree)).toContain("변경 전 기록"); expect(text(tree)).toContain("일부 영상 구간의 진단이 실패"); expect(text(tree)).toContain("영상 근거에 따른 추정");
  expect(text(tree)).toContain("입력 토큰 0"); expect(text(tree)).toContain("출력 토큰 확인 불가"); expect(text(tree)).toContain("확인의 한계"); expect(text(tree)).toContain("받는 순간이 다른 사람에 가려졌을 가능성");
  await click(tree, "00:20–00:23 영상 근거"); expect(seek).toHaveBeenCalledWith(20);
  expect(historyApi.createRecognitionReview).not.toHaveBeenCalled(); await cleanup();
});

it("disables current-position and evidence seeking on transformed video", async () => {
  (historyApi.listRecognitionReviews as jest.Mock).mockResolvedValue({ runs: [run] });
  const { tree, seek, cleanup } = await mount({ isOriginal: false });
  expect(button(tree, "현재 영상 위치 사용").props.disabled).toBe(true);
  expect(button(tree, "00:20–00:23 영상 근거").props.disabled).toBe(true); expect(seek).not.toHaveBeenCalled(); await cleanup();
});

it("shows plain Korean stage labels and keeps unrecognized stages unknown", async () => {
  const stages = ["chunk_observation", "segment_selection", "deep_observation", "aggregation", "unknown", "new_internal_stage"];
  const labeledRun = { ...run, batches: [{ ...run.batches[0], findings: stages.map(stage => ({ ...run.batches[0].findings[0], stage })) }] };
  (historyApi.listRecognitionReviews as jest.Mock).mockResolvedValue({ runs: [labeledRun] });
  (historyApi.getRecognitionReview as jest.Mock).mockResolvedValue(labeledRun);
  const { tree, cleanup } = await mount();
  for (const label of ["짧은 구간 관찰", "분석 구간 선택", "심층 관찰", "결과 집계", "확인 불가"]) expect(text(tree)).toContain(label);
  for (const stage of stages) expect(text(tree)).not.toContain(stage);
  await cleanup();
});

it("does not turn partial batch token usage into a complete run total", async () => {
  const mixedUsageRun = { ...run, input_tokens: null, output_tokens: null, total_tokens: null, batches: [
    { ...run.batches[0], input_tokens: 50, output_tokens: 10, total_tokens: 60 },
    { ...run.batches[0], id: 1, input_tokens: null, output_tokens: null, total_tokens: null },
  ] };
  (historyApi.listRecognitionReviews as jest.Mock).mockResolvedValue({ runs: [mixedUsageRun] });
  (historyApi.getRecognitionReview as jest.Mock).mockResolvedValue(mixedUsageRun);
  const { tree, cleanup } = await mount();
  expect(text(tree)).toContain("입력 토큰 확인 불가"); expect(text(tree)).toContain("총 토큰 확인 불가");
  expect(text(tree)).not.toContain("총 토큰 60");
  await cleanup();
});

it("separates a full confirmed record total from each split review window", async () => {
  const fullRecord = { ...row, record: { ...record, start_secs: 0, end_secs: 300, reps: 50 } };
  const splitRun = { ...run, evaluation: { ...evaluation, records: [fullRecord] }, batches: [
    { ...run.batches[0], core_start_secs: 0, core_end_secs: 150, start_secs: 0, end_secs: 155 },
    { ...run.batches[0], id: 1, core_start_secs: 150, core_end_secs: 300, start_secs: 145, end_secs: 300 },
  ] };
  (historyApi.listRecognitionReviews as jest.Mock).mockResolvedValue({ runs: [splitRun] });
  (historyApi.getRecognitionReview as jest.Mock).mockResolvedValue(splitRun);
  const { tree, cleanup } = await mount();
  const paragraphs = tree.root.findAllByType("p").map((item: any) => item.children.join(""));
  expect(paragraphs.filter((value: string) => value === "확정 기록 전체: Power Clean 50회 (00:00–05:00)")).toHaveLength(2);
  expect(paragraphs).toContain("검토 구간: 00:00–02:30"); expect(paragraphs).toContain("검토 구간: 02:30–05:00");
  expect(paragraphs.filter((value: string) => value.startsWith("검토 구간:")).every((value: string) => !value.includes("50회"))).toBe(true);
  await cleanup();
});

it("ignores a request completing after the source changes", async () => {
  let finish!: (value: unknown) => void;
  (historyApi.createRecognitionReview as jest.Mock).mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const { tree, render, cleanup } = await mount(); await click(tree, "선택 구간 오류 진단");
  await act(async () => tree.update(render({ sourceUpdatedAt: "second" }))); await flush();
  await act(async () => finish({ run_id: 99, status: "QUEUED" })); await flush();
  expect(historyApi.getRecognitionReview).not.toHaveBeenCalledWith("session", 99); await cleanup();
});

it("refreshes the comparison after a stale confirmation conflict without resubmitting", async () => {
  (historyApi.createRecognitionReview as jest.Mock).mockRejectedValue({ status: 409 });
  const { tree, cleanup } = await mount(); const before = (historyApi.getRecognitionEvaluation as jest.Mock).mock.calls.length;
  await click(tree, "선택 구간 오류 진단"); expect(text(tree)).toContain("최신 상태를 확인");
  expect((historyApi.getRecognitionEvaluation as jest.Mock).mock.calls.length).toBeGreaterThan(before); expect(historyApi.createRecognitionReview).toHaveBeenCalledTimes(1); await cleanup();
});

it("resumes failed status reads without submitting another AI job", async () => {
  (historyApi.listRecognitionReviews as jest.Mock).mockRejectedValueOnce(new Error("offline"));
  const { tree, cleanup } = await mount(); await click(tree, "진단 상태 다시 확인");
  expect(historyApi.listRecognitionReviews).toHaveBeenCalledTimes(2); expect(historyApi.createRecognitionReview).not.toHaveBeenCalled(); await cleanup();
});

it("counts boundary-context input once per split and deduplicates selected IDs", () => {
  const long = { ...row, record: { ...record, start_secs: 0, end_secs: 301 } };
  expect(recognitionReviewInputSeconds([long])).toBe(321);
  expect(recognitionReviewSelection([row, missing], [1, 1, 2], 20, 70)).toMatchObject({ valid: true, excludedCount: 1, records: [missing], inputSeconds: 20 });
  expect(recognitionReviewSelection([row], [1], 40, 40).valid).toBe(false);
});
