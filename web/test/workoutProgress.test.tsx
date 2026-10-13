jest.mock("react", () => jest.requireActual("../../node_modules/react"));
import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WorkoutProgressPanel } from "../src/history/components/WorkoutProgressPanel";
import { historyApi } from "../src/api/history";
import type { WorkoutProgressResponse, PerformanceRecord } from "../../shared/workoutProgress";
jest.mock("../src/api/history", () => ({ historyApi: { getWorkoutProgress: jest.fn(), confirmPerformanceRecords: jest.fn(), getPerformanceBenchmarks: jest.fn() } }));
const { act, create } = require("react-test-renderer");
const evidence: WorkoutProgressResponse = { progress: { version: 1, clock: "media", source_version: "a", intervals: [{ id: "set-1", start_secs: 10, end_secs: 70, activity: "exercise", movement: "Power Clean", load: { value: 135, unit: "lb", basis: "total" }, observation_refs: [], rep_events: [] }], unassessed: [] }, records: [] };
const baseline: PerformanceRecord = { id: 1, session_id: "old", interval_id: "old-set", source_version: "old", status: "confirmed", movement: "Power Clean", start_secs: 0, end_secs: 60, reps: 3, load: { value: 135, unit: "lb", basis: "total" }, load_kg: 61.2, measurement: "window", technique: "singles", workout_prescription: "test", reps_per_minute: 3, rate_basis: "actual_60_second_window", median_cycle_seconds: null, performed_at: "2026-10-12T00:00:00Z", confirmed_at: "2026-10-12T00:00:00Z" };
const current: PerformanceRecord = { ...baseline, id: 2, session_id: "new", reps: 5, reps_per_minute: 5, rate_basis: "normalized_per_minute" };
beforeEach(() => { jest.clearAllMocks(); (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue(evidence); (historyApi.getPerformanceBenchmarks as jest.Mock).mockResolvedValue({ records: [current, baseline], comparisons: [{ current_record_id: 2, baseline_record_id: 1, kind: "reference", condition_differences: ["unknown_technique", "workout_prescription"], rate_change_percent: 66.7, heavier_load_kg: null, heavier_load_baseline_record_id: null }], best: [baseline] }); });
async function flush() { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }
async function mount(sessionId = "new", isOriginal = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false, gcTime: Infinity } } }); let tree: any;
  const seek = jest.fn(); await act(async () => { tree = create(createElement(QueryClientProvider, { client }, createElement(WorkoutProgressPanel, { sessionId, profileId: 1, isOriginal, seek }))); }); await flush(); return { tree, client, seek };
}
function allText(tree: any): string { return JSON.stringify(tree.toJSON()); }
async function click(tree: any, label: string) { const b = tree.root.findAllByType("button").find((item: any) => item.children.join("") === label); expect(b).toBeDefined(); await act(async () => b.props.onClick()); await flush(); }
async function review(tree: any) { await click(tree, "반복수·구간·중량 확인/수정"); await act(async () => { tree.root.findAllByType("input").find((item: any) => item.props["aria-label"] === "완료 반복수").props.onChange({ target: { value: "5" } }); }); await act(async () => { tree.root.findAllByType("input").find((item: any) => item.props.type === "checkbox").props.onChange({ target: { checked: true } }); }); }
it("shows missing evidence as unknown and prevents seeking transformed media", async () => {
  const { tree, client, seek } = await mount("new", false);
  expect(allText(tree)).toContain("확인 불가"); expect(historyApi.confirmPerformanceRecords).not.toHaveBeenCalled();
  const play = tree.root.findAllByType("button").find((b: any) => b.children.join("") === "영상 구간 확인"); expect(play.props.disabled).toBe(true); expect(seek).not.toHaveBeenCalled();
  await act(() => tree.unmount()); client.clear();
});
it("confirms reviewed values and distinguishes reference comparison and rate basis", async () => {
  (historyApi.confirmPerformanceRecords as jest.Mock).mockResolvedValue({ ...evidence, records: [current] });
  const { tree, client } = await mount(); await review(tree); await click(tree, "확인한 기록 저장");
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("new", { source_version: "a", records: [{ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, measurement: "set", technique: "unspecified" }] });
  expect(allText(tree)).toContain("분당 환산값"); expect(allText(tree)).toContain("실제 60초 측정"); expect(allText(tree)).toContain("참고 비교"); expect(allText(tree)).toContain("WOD 구성"); expect(allText(tree)).toContain("+66.7%");
  await act(() => tree.unmount()); client.clear();
});
it("refreshes current evidence on conflict and discards the previous confirmation editor", async () => {
  (historyApi.confirmPerformanceRecords as jest.Mock).mockRejectedValue({ status: 409 });
  const { tree, client } = await mount(); await review(tree);
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue({ ...evidence, progress: { ...evidence.progress!, source_version: "b" } });
  await click(tree, "확인한 기록 저장"); expect(allText(tree)).toContain("분석 근거가 변경"); expect(tree.root.findAllByType("input")).toHaveLength(0); expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledTimes(1);
  await click(tree, "반복수·구간·중량 확인/수정"); expect(tree.root.findAllByType("input").find((i: any) => i.props.type === "checkbox").props.checked).toBe(false);
  await act(() => tree.unmount()); client.clear();
});
it("retains draft input when saving fails without confirming it", async () => {
  (historyApi.confirmPerformanceRecords as jest.Mock).mockRejectedValue(new Error("offline")); const { tree, client } = await mount(); await review(tree); await click(tree, "확인한 기록 저장"); expect(allText(tree)).toContain("불러오거나 저장하지 못했습니다"); expect(tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "완료 반복수").props.value).toBe("5"); await act(() => tree.unmount()); client.clear();
});
it("ignores a save completing after the session changes", async () => {
  let finish!: (r: WorkoutProgressResponse) => void;
  (historyApi.confirmPerformanceRecords as jest.Mock).mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const { tree, client } = await mount(); await review(tree); await click(tree, "확인한 기록 저장");
  await act(async () => tree.update(createElement(QueryClientProvider, { client }, createElement(WorkoutProgressPanel, { sessionId: "other", profileId: 1, isOriginal: true, seek: jest.fn() })))); await flush();
  await act(async () => finish({ ...evidence, records: [current] })); await flush();
  expect(allText(tree)).not.toContain("확인한 기록을 저장했습니다");
  expect(client.getQueryData(["workout-progress", "other", 1, undefined])).toEqual(evidence);
  await act(() => tree.unmount()); client.clear();
});
it("labels best set groups by reps and window groups by actual elapsed time", async () => {
  const best = [
    { ...baseline, id: 11, measurement: "set", reps: 5, elapsed_seconds: 60, reps_per_minute: 5 },
    { ...baseline, id: 12, measurement: "set", reps: 8, elapsed_seconds: 60, reps_per_minute: 8 },
    { ...baseline, id: 13, measurement: "window", reps: 5, elapsed_seconds: 60, reps_per_minute: 5 },
    { ...baseline, id: 14, measurement: "window", reps: 5, elapsed_seconds: 30, reps_per_minute: 10 },
  ];
  (historyApi.getPerformanceBenchmarks as jest.Mock).mockResolvedValue({ records: [], comparisons: [], best });
  const { tree, client } = await mount(); await click(tree, "이 동작의 비교 이력");
  const rows = tree.root.findAllByType("p").map((row: any) => row.children.join("")).filter((value: string) => value.startsWith("동일 조건 최고 수행률"));
  expect(rows).toHaveLength(4);
  expect(rows[0]).toContain("5회"); expect(rows[0]).toContain("60.0초"); expect(rows[0]).toContain("세트");
  expect(rows[1]).toContain("8회"); expect(rows[1]).toContain("세트");
  expect(rows[2]).toContain("60.0초"); expect(rows[2]).toContain("시간 구간");
  expect(rows[3]).toContain("30.0초"); expect(rows[3]).toContain("시간 구간");
  await act(() => tree.unmount()); client.clear();
});
it("reviews and confirms a 60-second exercise-rest-exercise window", async () => {
  const first = { ...evidence.progress!.intervals[0], end_secs: 20 };
  const progress = { ...evidence.progress!, intervals: [first, { ...first, id: "rest", start_secs: 20, end_secs: 40, activity: "rest", movement: null, load: null }, { ...first, id: "later", start_secs: 40, end_secs: 70 }], unassessed: [] };
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue({ progress, records: [] });
  (historyApi.confirmPerformanceRecords as jest.Mock).mockResolvedValue({ progress, records: [] });
  const { tree, client, seek } = await mount(); await click(tree, "반복수·구간·중량 확인/수정");
  await act(async () => {
    tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "종료(초)").props.onChange({ target: { value: "70" } });
    tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "완료 반복수").props.onChange({ target: { value: "5" } });
    tree.root.findAllByType("select").find((s: any) => s.props.value === "set").props.onChange({ target: { value: "window" } });
  });
  await click(tree, "선택한 영상 범위 확인"); expect(seek).toHaveBeenCalledWith(10, 70);
  await act(async () => tree.root.findAllByType("input").find((i: any) => i.props.type === "checkbox").props.onChange({ target: { checked: true } }));
  await click(tree, "확인한 기록 저장");
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("new", { source_version: "a", records: [{ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, measurement: "window", technique: "unspecified" }] });
  await act(() => tree.unmount()); client.clear();
});
it("displays observed rest cycle changes and missing medians without physiological percentages", async () => {
  const record = { ...current, interval_id: "set-1", source_version: "a", rest_comparisons: [
    { rest_start_secs: 20, rest_end_secs: 40, rest_elapsed_seconds: 20, before_median_cycle_seconds: 4, after_median_cycle_seconds: 6, cycle_change_percent: 50 },
    { rest_start_secs: 45, rest_end_secs: 50, rest_elapsed_seconds: null, before_median_cycle_seconds: null, after_median_cycle_seconds: null, cycle_change_percent: null },
  ] };
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue({ ...evidence, records: [record] });
  const { tree, client } = await mount();
  const rows = tree.root.findAllByType("p").map((p: any) => p.children.join("")).filter((value: string) => value.startsWith("관찰된 휴식 후 반복시간"));
  expect(rows).toHaveLength(2); expect(rows[0]).toContain("휴식 20.00초"); expect(rows[0]).toContain("이전 4.00초 → 이후 6.00초"); expect(rows[0]).toContain("반복 소요시간 변화 +50.0%");
  expect(rows[1]).toContain("이전 확인 불가 → 이후 확인 불가"); expect(rows[1]).toContain("반복 소요시간 변화 확인 불가"); expect(rows[1]).not.toContain("%"); expect(rows.join("")).not.toContain("회복률");
  await act(() => tree.unmount()); client.clear();
});
