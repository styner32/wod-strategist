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
async function mount(sessionId = "new", isOriginal = true, wodDescription?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false, gcTime: Infinity } } }); let tree: any;
  const seek = jest.fn(); await act(async () => { tree = create(createElement(QueryClientProvider, { client }, createElement(WorkoutProgressPanel, { sessionId, profileId: 1, isOriginal, seek, wodDescription }))); }); await flush(); return { tree, client, seek };
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
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("new", { source_version: "a", records: [{ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, measurement: "set", technique: "singles" }] });
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
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("new", { source_version: "a", records: [{ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, measurement: "window", technique: "singles" }] });
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
it("renders total movement summary and applies WOD description weights with one click", async () => {
  const unweightedEvidence: WorkoutProgressResponse = {
    progress: {
      version: 1,
      clock: "media",
      source_version: "a",
      intervals: [
        { id: "set-1", start_secs: 10, end_secs: 70, activity: "exercise", movement: "Power Clean", load: null, observation_refs: [], rep_events: [{ movement: "Power Clean", unit: "reps", start_secs: 12, end_secs: 16, complete: true, evidence: "Rep 1" }] },
      ],
      unassessed: [],
    },
    records: [],
  };
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue(unweightedEvidence);
  (historyApi.confirmPerformanceRecords as jest.Mock).mockResolvedValue({ ...unweightedEvidence, records: [current] });
  const { tree, client } = await mount("new", true, "30 Power Cleans (135/95 lb)");
  expect(allText(tree)).toContain("WOD 설명");
  expect(allText(tree)).toContain("30 Power Cleans (135/95 lb)");
  expect(allText(tree)).toContain("총 수행 운동 및 개수 요약");
  expect(allText(tree)).toContain("총 1종목 · 1구간");
  expect(allText(tree)).toContain("관찰 완료 1회");
  expect(allText(tree)).toContain("135 lb · 총합 중량 (바벨·단일 기구) / 95 lb · 총합 중량 (바벨·단일 기구) · WOD 설명 중량 후보");
  await click(tree, "반복수·구간·중량 확인/수정");
  expect(allText(tree)).toContain("수행 중량 구분");
  expect(allText(tree)).toContain("맨몸 수행 (추가 중량 없음)");
  expect(allText(tree)).toContain("중량 기구 사용 (바벨·덤벨·케틀벨 등)");
  await click(tree, "95 lb 적용");
  await act(async () => {
    tree.root.findAllByType("input").find((i: any) => i.props.type === "checkbox").props.onChange({ target: { checked: true } });
  });
  await click(tree, "확인한 기록 저장");
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("new", {
    source_version: "a",
    records: [{ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 1, load: { value: 95, unit: "lb", basis: "total" }, measurement: "set", technique: "singles" }],
  });
  await act(() => tree.unmount()); client.clear();
});
it("merges contiguous 10-second segment intervals and confirms them as a single set", async () => {
  const splitEvidence: WorkoutProgressResponse = {
    progress: {
      version: 1,
      clock: "media",
      source_version: "split",
      intervals: [
        { id: "seg-0", start_secs: 0, end_secs: 9, activity: "exercise", movement: "Power Clean", load: { value: 135, unit: "lb", basis: "total" }, observation_refs: [], rep_events: [{ movement: "Power Clean", unit: "reps", start_secs: 1, end_secs: 3, complete: true, evidence: "Rep 1" }] },
        { id: "seg-1", start_secs: 11, end_secs: 20, activity: "exercise", movement: "Power Clean", load: { value: 135, unit: "lb", basis: "total" }, observation_refs: [], rep_events: [{ movement: "Power Clean", unit: "reps", start_secs: 12, end_secs: 14, complete: true, evidence: "Rep 2" }] },
      ],
      unassessed: [{ start_secs: 9, end_secs: 11, clock: "media", reason: "not_analyzed" }],
    },
    records: [],
  };
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue(splitEvidence);
  (historyApi.confirmPerformanceRecords as jest.Mock).mockResolvedValue({ ...splitEvidence, records: [current] });
  const { tree, client } = await mount("new", true);
  expect(tree.root.find((node: any) => node.props?.["data-testid"] === "contiguous-badge-seg-0").children.join("")).toContain("연속 구간");
  expect(tree.root.find((node: any) => node.props?.["data-testid"] === "contiguous-badge-seg-1").children.join("")).toContain("연속 구간");
  await click(tree, "반복수·구간·중량 확인/수정");
  await click(tree, "연속된 구간 합치기");
  await act(async () => {
    tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "완료 반복수").props.onChange({ target: { value: "10" } });
    tree.root.findAllByType("input").find((i: any) => i.props.type === "checkbox").props.onChange({ target: { checked: true } });
  });
  await click(tree, "확인한 기록 저장");
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("new", {
    source_version: "split",
    records: [{ interval_id: "seg-0", start_secs: 0, end_secs: 20, reps: 10, load: { value: 135, unit: "lb", basis: "total" }, measurement: "set", technique: "singles" }],
  });
  await act(() => tree.unmount()); client.clear();
});

it("renders compact rest rows, highlights confirmed records including merged spans, and offers only 끊김/연속 technique options", async () => {
  const confirmedMerged: PerformanceRecord = { ...current, interval_id: "seg-0", source_version: "split", start_secs: 0, end_secs: 20, reps: 10 };
  const splitWithRest: WorkoutProgressResponse = {
    progress: {
      version: 1,
      clock: "media",
      source_version: "split",
      intervals: [
        { id: "seg-0", start_secs: 0, end_secs: 10, activity: "exercise", movement: "Power Clean", load: { value: 135, unit: "lb", basis: "total" }, observation_refs: [], rep_events: [] },
        { id: "seg-1", start_secs: 10, end_secs: 20, activity: "exercise", movement: "Power Clean", load: { value: 135, unit: "lb", basis: "total" }, observation_refs: [], rep_events: [] },
        { id: "rest-2", start_secs: 20, end_secs: 30, activity: "rest", movement: null, load: null, observation_refs: [] },
      ],
      unassessed: [],
    },
    records: [confirmedMerged],
  };
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue(splitWithRest);
  const { tree, client } = await mount("new", true);
  const restRow = tree.root.find((node: any) => node.props?.["data-testid"] === "rest-interval-rest-2");
  expect(restRow).toBeDefined();
  expect(restRow.findAllByType("details")).toHaveLength(0);
  const badge0 = tree.root.find((node: any) => node.props?.["data-testid"] === "confirmed-badge-seg-0");
  const badge1 = tree.root.find((node: any) => node.props?.["data-testid"] === "confirmed-badge-seg-1");
  expect(badge0.children.join("")).toContain("확정 기록");
  expect(badge1.children.join("")).toContain("확정 기록");
  const card0 = tree.root.find((node: any) => node.props?.["data-testid"] === "exercise-interval-seg-0");
  expect(card0.props.className).toContain("border-success/50");

  await click(tree, "반복수·구간·중량 확인/수정");
  const techniqueSelect = tree.root.findAllByType("select").find((s: any) => s.props.value === "singles");
  expect(techniqueSelect).toBeDefined();
  const options = techniqueSelect.findAllByType("option").map((o: any) => ({ value: o.props.value, label: o.children.join("") }));
  expect(options).toEqual([
    { value: "singles", label: "끊김" },
    { value: "touch_and_go", label: "연속" },
  ]);
  await act(() => tree.unmount()); client.clear();
});

it("remembers edited values when closing and reopening an interval or opening the next interval of the same movement", async () => {
  const multiSet: WorkoutProgressResponse = {
    progress: {
      version: 1,
      clock: "media",
      source_version: "multi",
      intervals: [
        { id: "set-1", start_secs: 0, end_secs: 20, activity: "exercise", movement: "Power Clean", load: null, observation_refs: [], rep_events: [] },
        { id: "rest-1", start_secs: 20, end_secs: 30, activity: "rest", movement: null, load: null, observation_refs: [] },
        { id: "set-2", start_secs: 30, end_secs: 50, activity: "exercise", movement: "Power Clean", load: null, observation_refs: [], rep_events: [] },
      ],
      unassessed: [],
    },
    records: [],
  };
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue(multiSet);
  const { tree, client } = await mount("new", true, "Power Clean 135/95 lb");
  const editButtons = () => tree.root.findAllByType("button").filter((b: any) => b.children.join("") === "반복수·구간·중량 확인/수정");

  // Open set-1 and customize reps, weight, and technique
  await act(async () => editButtons()[0].props.onClick());
  await flush();
  await click(tree, "95 lb 적용");
  await act(async () => {
    tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "완료 반복수").props.onChange({ target: { value: "12" } });
    tree.root.findAllByType("select").find((s: any) => s.props.value === "singles").props.onChange({ target: { value: "touch_and_go" } });
  });

  // Switch to set-2: weight (95 lb) and technique (touch_and_go) should be remembered for Power Clean
  await act(async () => editButtons()[1].props.onClick());
  await flush();
  expect(tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "중량").props.value).toBe("95");
  expect(tree.root.findAllByType("select").find((s: any) => s.props.value === "touch_and_go")).toBeDefined();

  // Close set-2 and reopen set-1: set-1's reps ("12"), weight ("95"), and technique ("touch_and_go") are preserved
  await click(tree, "닫기");
  await act(async () => editButtons()[0].props.onClick());
  await flush();
  expect(tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "완료 반복수").props.value).toBe("12");
  expect(tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "중량").props.value).toBe("95");
  expect(tree.root.findAllByType("select").find((s: any) => s.props.value === "touch_and_go")).toBeDefined();
  await act(() => tree.unmount()); client.clear();
});

it("lets the user choose which movement to merge as when contiguous intervals have different movements", async () => {
  const scPlusFs: WorkoutProgressResponse = {
    progress: {
      version: 1,
      clock: "media",
      source_version: "sc-fs",
      intervals: [
        {
          id: "seg-sc",
          start_secs: 10,
          end_secs: 13,
          activity: "exercise",
          movement: "Squat Clean",
          load: { value: 135, unit: "lb", basis: "total" },
          observation_refs: ["chunk:1"],
          rep_events: [{ movement: "Squat Clean", unit: "reps", start_secs: 10, end_secs: 13, complete: true, evidence: "clean" }],
        },
        {
          id: "seg-fs",
          start_secs: 13,
          end_secs: 25,
          activity: "exercise",
          movement: "Front Squat",
          load: { value: 135, unit: "lb", basis: "total" },
          observation_refs: ["chunk:1", "chunk:2"],
          rep_events: [
            { movement: "Front Squat", unit: "reps", start_secs: 13, end_secs: 16, complete: true, evidence: "squat 1" },
            { movement: "Front Squat", unit: "reps", start_secs: 16, end_secs: 19, complete: true, evidence: "squat 2" },
            { movement: "Front Squat", unit: "reps", start_secs: 19, end_secs: 22, complete: true, evidence: "squat 3" },
            { movement: "Front Squat", unit: "reps", start_secs: 22, end_secs: 25, complete: true, evidence: "squat 4" },
          ],
        },
      ],
      unassessed: [],
    },
    records: [],
  };
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue(scPlusFs);
  (historyApi.confirmPerformanceRecords as jest.Mock).mockResolvedValue(scPlusFs);
  const { tree, client } = await mount("new", true);

  // Open the first interval (1 Squat Clean)
  await click(tree, "반복수·구간·중량 확인/수정");
  expect(allText(tree)).toContain("Squat Clean 기준으로 합치기");
  expect(allText(tree)).toContain("Front Squat 기준으로 합치기");

  // Merge as Front Squat: sets start=10, end=25, suggested reps=5 (1 Squat Clean + 4 Front Squat), and targetIntervalId="seg-fs"
  await click(tree, "Front Squat 기준으로 합치기");
  expect(tree.root.findAllByType("input").find((i: any) => i.props["aria-label"] === "완료 반복수").props.value).toBe("5");
  expect(allText(tree)).toContain("합친 구간 기준 동작");
  await act(async () => {
    tree.root.findAllByType("input").find((item: any) => item.props.type === "checkbox").props.onChange({ target: { checked: true } });
  });

  await click(tree, "확인한 기록 저장");
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("new", {
    source_version: "sc-fs",
    records: [
      {
        interval_id: "seg-fs",
        start_secs: 10,
        end_secs: 25,
        reps: 5,
        load: { value: 135, unit: "lb", basis: "total" },
        measurement: "set",
        technique: "singles",
      },
    ],
  });
  await act(() => tree.unmount()); client.clear();
});

it("shows incomplete cycle notice when observed complete reps are 0 and confirms 0-rep (incomplete/no-rep) with one click", async () => {
  const zeroConfirmed: PerformanceRecord = {
    ...current,
    id: 99,
    interval_id: "set-incomplete",
    source_version: "zero-src",
    start_secs: 10,
    end_secs: 20,
    elapsed_seconds: 10,
    reps: 0,
    load: null,
    load_kg: null,
    reps_per_minute: 0,
  };
  const incompleteEvidence: WorkoutProgressResponse = {
    progress: {
      version: 1,
      clock: "media",
      source_version: "zero-src",
      intervals: [
        {
          id: "set-incomplete",
          start_secs: 10,
          end_secs: 20,
          activity: "exercise",
          movement: "Power Clean",
          load: null,
          observation_refs: ["chunk:1"],
          rep_events: [
            {
              movement: "Power Clean",
              unit: "reps",
              start_secs: 12,
              end_secs: 15,
              complete: false,
              evidence: "Failed catch before front rack lockout",
            },
          ],
        },
      ],
      unassessed: [],
    },
    records: [],
  };
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue(incompleteEvidence);
  (historyApi.confirmPerformanceRecords as jest.Mock).mockResolvedValue({
    ...incompleteEvidence,
    records: [zeroConfirmed],
  });
  const { tree, client } = await mount("new", true);
  expect(allText(tree)).toContain("랩 기준 미달(불완전 반복) 1회 감지 · 완료 반복 0회");

  await click(tree, "반복수·구간·중량 확인/수정");
  await click(tree, "0회(미완료/노렙)로 확정");

  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("new", {
    source_version: "zero-src",
    records: [
      {
        interval_id: "set-incomplete",
        start_secs: 10,
        end_secs: 20,
        reps: 0,
        load: null,
        measurement: "set",
        technique: "singles",
      },
    ],
  });
  expect(allText(tree)).toContain("0회 · 미완료/노렙");
  expect(allText(tree)).toContain("확정 총 0회");
  await act(() => tree.unmount()); client.clear();
});


