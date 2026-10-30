jest.mock("react", () => jest.requireActual("../../node_modules/react"));
import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WorkoutProgressPanel } from "../src/history/components/WorkoutProgressPanel";
import { historyApi } from "../src/api/history";
import type { PerformanceRecord, WorkoutProgressInterval, WorkoutProgressResponse } from "../../shared/workoutProgress";

jest.mock("../src/api/history", () => ({ historyApi: { getWorkoutProgress: jest.fn(), confirmPerformanceRecords: jest.fn(), getPerformanceBenchmarks: jest.fn() } }));
const { act, create } = require("react-test-renderer");

const exercise = (id: string, start: number, end: number, reps = 1): WorkoutProgressInterval => ({
  id, start_secs: start, end_secs: end, activity: "exercise", movement: "Burpee", load: null, observation_refs: [],
  rep_events: Array.from({ length: reps }, (_, i) => ({ movement: "Burpee", unit: "reps", start_secs: start + i, end_secs: start + i + 0.5, complete: true, evidence: "Visible complete rep" })),
});
const response = (intervals: WorkoutProgressInterval[], records: PerformanceRecord[] = []): WorkoutProgressResponse => ({
  progress: { version: 1, clock: "media", source_version: "keyboard-source", intervals, unassessed: [] }, records,
});
const confirmed = (interval: WorkoutProgressInterval): PerformanceRecord => ({
  id: 1, session_id: "keyboard", interval_id: interval.id, source_version: "keyboard-source", status: "confirmed", movement: interval.movement ?? "Burpee",
  start_secs: interval.start_secs, end_secs: interval.end_secs, reps: 1, load: null, load_kg: null, measurement: "set", technique: "singles", workout_prescription: "",
  reps_per_minute: 6, rate_basis: "normalized_per_minute", median_cycle_seconds: null, performed_at: null, confirmed_at: "2026-10-29T00:00:00Z",
});

beforeEach(() => {
  jest.clearAllMocks();
  (historyApi.getPerformanceBenchmarks as jest.Mock).mockResolvedValue({ records: [], comparisons: [], best: [] });
});
async function flush() { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }
async function mount(data: WorkoutProgressResponse) {
  (historyApi.getWorkoutProgress as jest.Mock).mockResolvedValue(data);
  let saved = [...data.records];
  (historyApi.confirmPerformanceRecords as jest.Mock).mockImplementation(async (_session, request) => {
    const input = request.records[0];
    const interval = data.progress!.intervals.find(row => row.id === input.interval_id)!;
    saved = [...saved, { ...confirmed(interval), ...input, id: saved.length + 10 }];
    return { ...data, records: saved };
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false, gcTime: Infinity } } });
  const nodes: { focus: jest.Mock; scrollIntoView: jest.Mock }[] = [];
  let tree: any;
  await act(async () => {
    tree = create(createElement(QueryClientProvider, { client }, createElement(WorkoutProgressPanel, {
      sessionId: "keyboard", profileId: 1, isOriginal: true, seek: jest.fn(),
    })), {
      createNodeMock: (element: any) => {
        if (element.props["data-testid"] !== "performance-editor") return null;
        const node = { focus: jest.fn(), scrollIntoView: jest.fn() };
        nodes.push(node);
        return node;
      },
    });
  });
  await flush();
  return { tree, client, nodes, cleanup: async () => { await act(() => tree.unmount()); client.clear(); } };
}
const editor = (tree: any) => tree.root.find((node: any) => node.props["data-testid"] === "performance-editor");
const input = (tree: any, label: string) => tree.root.findAllByType("input").find((node: any) => node.props["aria-label"] === label);
async function open(tree: any, id: string) {
  const card = tree.root.find((node: any) => node.props["data-testid"] === `exercise-interval-${id}`);
  const button = card.findAllByType("button").find((node: any) => node.children.join("") === "반복수·구간·중량 확인/수정");
  await act(async () => button.props.onClick());
  await flush();
}
function keyEvent(overrides: Record<string, unknown> = {}) {
  const target = { tagName: "DIV", isContentEditable: false, closest: () => null };
  return { key: "Enter", repeat: false, altKey: false, ctrlKey: false, metaKey: false, shiftKey: false, nativeEvent: {}, preventDefault: jest.fn(), stopPropagation: jest.fn(), target, currentTarget: target, ...overrides };
}
async function enter(tree: any, overrides: Record<string, unknown> = {}) {
  const event = keyEvent(overrides);
  await act(async () => editor(tree).props.onKeyDown(event));
  await flush();
  return event;
}

it("confirms without checking the checkbox, focuses the next record, and completes with repeated Enter", async () => {
  const mounted = await mount(response([exercise("first", 0, 10), exercise("second", 20, 30, 2)]));
  const { tree, nodes } = mounted;
  await open(tree, "first");
  expect(tree.root.findAllByType("input").find((node: any) => node.props.type === "checkbox").props.checked).toBe(false);
  expect(editor(tree).props.tabIndex).toBe(-1);
  expect(nodes[nodes.length - 1].focus).toHaveBeenCalled();
  await enter(tree);
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledTimes(1);
  expect(historyApi.confirmPerformanceRecords).toHaveBeenLastCalledWith("keyboard", {
    source_version: "keyboard-source", records: [expect.objectContaining({ interval_id: "first", start_secs: 0, end_secs: 10, reps: 1, load: null })],
  });
  expect(input(tree, "시작(초)").props.value).toBe("20");
  expect(input(tree, "완료 반복수").props.value).toBe("2");
  expect(nodes[nodes.length - 1].focus).toHaveBeenCalled();
  expect(nodes[nodes.length - 1].scrollIntoView).toHaveBeenCalled();
  await enter(tree);
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledTimes(2);
  expect(tree.root.findAll((node: any) => node.props["data-testid"] === "performance-editor")).toHaveLength(0);
  expect(tree.root.findAllByProps({ role: "status" }).map((node: any) => node.children.join("")).join(" ")).toContain("저장");
  await mounted.cleanup();
});

it("skips rest, unknown intervals, gaps, and exercise rows covered by an existing confirmation", async () => {
  const covered = exercise("covered", 40, 50);
  const data = response([
    exercise("first", 0, 10),
    { ...exercise("rest", 10, 20), activity: "rest", movement: null, rep_events: [] },
    { ...exercise("unknown", 20, 30), activity: "unknown", movement: null, rep_events: [] },
    covered, exercise("next", 60, 70),
  ], [confirmed(covered)]);
  data.progress!.unassessed = [{ start_secs: 30, end_secs: 40, clock: "media", reason: "not_analyzed" }];
  const mounted = await mount(data);
  await open(mounted.tree, "first");
  await enter(mounted.tree);
  expect(input(mounted.tree, "시작(초)").props.value).toBe("60");
  await mounted.cleanup();
});

it("advances past the entire saved merged span", async () => {
  const mounted = await mount(response([exercise("first", 0, 10), exercise("middle", 10, 20), exercise("last", 20, 30), exercise("next", 40, 50)]));
  await open(mounted.tree, "first");
  await act(async () => input(mounted.tree, "종료(초)").props.onChange({ target: { value: "30" } }));
  await enter(mounted.tree);
  expect(historyApi.confirmPerformanceRecords).toHaveBeenLastCalledWith("keyboard", {
    source_version: "keyboard-source", records: [expect.objectContaining({ interval_id: "first", end_secs: 30 })],
  });
  expect(input(mounted.tree, "시작(초)").props.value).toBe("40");
  await mounted.cleanup();
});

it("saves all short-rest merged intervals with Enter and advances beyond the complete window", async () => {
  const mounted = await mount(response([
    exercise("first", 0, 10),
    { ...exercise("rest-1", 10, 15), activity: "rest", movement: null, rep_events: [] },
    exercise("middle", 15, 25),
    { ...exercise("rest-2", 25, 35), activity: "rest", movement: null, rep_events: [] },
    exercise("last", 35, 45),
    exercise("next", 60, 70),
  ]));
  await open(mounted.tree, "middle");
  const merge = mounted.tree.root.findAllByType("button").find((node: any) => node.children.join("") === "연속된 구간 합치기");
  expect(merge).toBeDefined();
  await act(async () => merge.props.onClick());
  await enter(mounted.tree);
  expect(historyApi.confirmPerformanceRecords).toHaveBeenLastCalledWith("keyboard", {
    source_version: "keyboard-source", records: [expect.objectContaining({ interval_id: "middle", start_secs: 0, end_secs: 45, reps: 3, measurement: "window" })],
  });
  expect(input(mounted.tree, "시작(초)").props.value).toBe("60");
  await mounted.cleanup();
});

it("rejects 0 repetitions on Enter while allowing blank optional-count confirmation to advance", async () => {
  const mounted = await mount(response([exercise("first", 0, 10, 0), exercise("next", 20, 30)]));
  await open(mounted.tree, "first");
  await act(async () => input(mounted.tree, "완료 반복수").props.onChange({ target: { value: "0" } }));
  await enter(mounted.tree);
  expect(historyApi.confirmPerformanceRecords).not.toHaveBeenCalled();
  expect(input(mounted.tree, "시작(초)").props.value).toBe("0");
  expect(JSON.stringify(mounted.tree.toJSON())).toContain("0회는 저장할 수 없습니다");

  await act(async () => input(mounted.tree, "완료 반복수").props.onChange({ target: { value: "" } }));
  await enter(mounted.tree);
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledWith("keyboard", {
    source_version: "keyboard-source",
    records: [expect.objectContaining({ interval_id: "first", reps: null })],
  });
  expect(input(mounted.tree, "시작(초)").props.value).toBe("20");
  await mounted.cleanup();
});

it("requires the actual load before Enter can confirm a weighted movement", async () => {
  const mounted = await mount(response([{ ...exercise("first", 0, 10), movement: "Power Clean" }]));
  await open(mounted.tree, "first");
  await enter(mounted.tree);
  expect(historyApi.confirmPerformanceRecords).not.toHaveBeenCalled();
  expect(input(mounted.tree, "중량").props.value).toBe("");
  expect(JSON.stringify(mounted.tree.toJSON())).toContain("0보다 큰 중량");
  await mounted.cleanup();
});

it("preserves the existing save button behavior instead of automatically opening another editor", async () => {
  const mounted = await mount(response([exercise("first", 0, 10), exercise("next", 20, 30)]));
  await open(mounted.tree, "first");
  await act(async () => mounted.tree.root.findAllByType("input").find((node: any) => node.props.type === "checkbox").props.onChange({ target: { checked: true } }));
  const save = mounted.tree.root.findAllByType("button").find((node: any) => node.children.join("") === "확인한 기록 저장");
  await act(async () => save.props.onClick());
  await flush();
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledTimes(1);
  expect(mounted.tree.root.findAll((node: any) => node.props["data-testid"] === "performance-editor")).toHaveLength(0);
  await mounted.cleanup();
});

it("retains the current editor after a failed save and retries with Enter", async () => {
  const mounted = await mount(response([exercise("first", 0, 10), exercise("next", 20, 30)]));
  (historyApi.confirmPerformanceRecords as jest.Mock).mockRejectedValueOnce(new Error("offline"));
  await open(mounted.tree, "first");
  await enter(mounted.tree);
  expect(input(mounted.tree, "시작(초)").props.value).toBe("0");
  expect(JSON.stringify(mounted.tree.toJSON())).toContain("불러오거나 저장하지 못했습니다");
  await enter(mounted.tree);
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledTimes(2);
  expect(input(mounted.tree, "시작(초)").props.value).toBe("20");
  await mounted.cleanup();
});

it("confirms a corrected repetition count with Enter from its input", async () => {
  const mounted = await mount(response([exercise("first", 0, 10), exercise("next", 20, 30)]));
  await open(mounted.tree, "first");
  await act(async () => input(mounted.tree, "완료 반복수").props.onChange({ target: { value: "3" } }));
  await enter(mounted.tree, { target: { tagName: "INPUT", type: "number", isContentEditable: false, closest: () => null } });
  expect(historyApi.confirmPerformanceRecords).toHaveBeenLastCalledWith("keyboard", {
    source_version: "keyboard-source", records: [expect.objectContaining({ interval_id: "first", reps: 3 })],
  });
  expect(input(mounted.tree, "시작(초)").props.value).toBe("20");
  await mounted.cleanup();
});

it("allows the next Enter confirmation while comparison refreshes remain pending", async () => {
  const mounted = await mount(response([exercise("first", 0, 10), exercise("next", 20, 30)]));
  let finishRefresh!: () => void;
  const refresh = new Promise<void>(resolve => { finishRefresh = resolve; });
  const invalidate = jest.spyOn(mounted.client, "invalidateQueries").mockReturnValue(refresh);
  await open(mounted.tree, "first");
  await enter(mounted.tree);
  expect(invalidate).toHaveBeenCalledTimes(4);
  expect(input(mounted.tree, "시작(초)").props.value).toBe("20");
  expect(input(mounted.tree, "시작(초)").props.disabled).toBe(false);
  await enter(mounted.tree);
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledTimes(2);
  finishRefresh();
  await flush();
  invalidate.mockRestore();
  await mounted.cleanup();
});

const ignoredKeyEvents: [string, Record<string, unknown>][] = [
  ["IME composition", { nativeEvent: { isComposing: true } }],
  ["IME completion key", { nativeEvent: { keyCode: 229 } }],
  ["held Enter", { repeat: true }],
  ["Shift+Enter", { shiftKey: true }],
  ["Ctrl+Enter", { ctrlKey: true }],
  ["Alt+Enter", { altKey: true }],
  ["Meta+Enter", { metaKey: true }],
  ["already handled Enter", { defaultPrevented: true }],
  ["another key", { key: "Escape" }],
  ...["BUTTON", "SELECT", "A", "TEXTAREA"].map<[string, Record<string, unknown>]>(tagName => [tagName, { target: { tagName, isContentEditable: false, closest: () => ({}) } }]),
  ["editable content", { target: { tagName: "DIV", isContentEditable: true, closest: () => ({}) } }],
];
it.each(ignoredKeyEvents)("does not confirm for %s", async (_name, overrides) => {
  const mounted = await mount(response([exercise("first", 0, 10)]));
  await open(mounted.tree, "first");
  const event = await enter(mounted.tree, overrides);
  expect(historyApi.confirmPerformanceRecords).not.toHaveBeenCalled();
  expect(event.preventDefault).not.toHaveBeenCalled();
  await mounted.cleanup();
});

it("sends only one save when Enter is pressed twice in the same render", async () => {
  const data = response([exercise("first", 0, 10), exercise("next", 20, 30)]);
  const mounted = await mount(data);
  let finish!: (value: WorkoutProgressResponse) => void;
  (historyApi.confirmPerformanceRecords as jest.Mock).mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  await open(mounted.tree, "first");
  const handler = editor(mounted.tree).props.onKeyDown;
  await act(async () => { handler(keyEvent()); handler(keyEvent()); });
  expect(historyApi.confirmPerformanceRecords).toHaveBeenCalledTimes(1);
  expect(input(mounted.tree, "시작(초)").props.value).toBe("0");
  await act(async () => finish({ ...data, records: [confirmed(data.progress!.intervals[0])] }));
  await flush();
  expect(input(mounted.tree, "시작(초)").props.value).toBe("20");
  await mounted.cleanup();
});
