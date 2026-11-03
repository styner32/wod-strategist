jest.mock("react", () => jest.requireActual("../../node_modules/react"));
jest.mock("../src/api/history", () => ({ historyApi: { getWorkoutShareContext: jest.fn() } }));

import { createElement } from "react";
import { historyApi } from "../src/api/history";
import { WorkoutShareDialog } from "../src/history/components/WorkoutShareDialog";
import type { WorkoutShareContext } from "../../shared/workoutShare";

const { act, create } = require("react-test-renderer");
const snapshot: WorkoutShareContext = {
  schema_version: 1,
  generated_at: "2026-11-03T03:00:00Z",
  timezone: "Asia/Seoul",
  session: {
    session_id: "WOD-selected",
    workout_type: "wod",
    performed_at: "2026-11-03T02:00:00Z",
    performed_at_source: "session_ulid",
    workout_prescription: "SHARE-PRESCRIPTION",
    source_version: "source-current",
  },
  confirmed_records: [],
  comparisons: [],
  baseline_records: [],
  observations: [{
    id: "interval-1", start_secs: 0, end_secs: 20, activity: "exercise", movement: "deadlift",
    load: null, observed_reps: 5,
    form_observations: [{ start_secs: 0, end_secs: 20, evidence: "SHARE-AI-ONLY" }],
    rom_observations: [], pace_observations: [],
  }],
  unassessed: [],
  sensor_summary: {
    status: "completed", quality_status: "adequate", avg_bpm: 143, peak_bpm: 181,
    coverage: 0.9, valid_seconds: 100, unknown_seconds: 10, excluded_seconds: 0,
    calculation_version: 3,
  },
  effort: 8,
  warnings: [],
};

let tree: ReturnType<typeof create>;
let copyText: jest.Mock;
let downloadedBlobs: Blob[];
let downloadClick: jest.Mock;
let revoke: jest.Mock;
const tick = async () => { await new Promise(resolve => setTimeout(resolve, 0)); };

beforeEach(() => {
  jest.clearAllMocks();
  downloadedBlobs = [];
  downloadClick = jest.fn();
  revoke = jest.fn();
  copyText = jest.fn().mockResolvedValue(undefined);
  Object.defineProperty(global, "Blob", { configurable: true, value: jest.requireActual("buffer").Blob });
  Object.defineProperty(global, "navigator", { configurable: true, value: { clipboard: { writeText: copyText } } });
  Object.defineProperty(global, "document", { configurable: true, value: {
    activeElement: { focus: jest.fn() },
    createElement: jest.fn(() => ({ click: downloadClick, remove: jest.fn() })),
    body: { appendChild: jest.fn() },
  } });
  Object.defineProperty(global, "window", { configurable: true, value: { setTimeout } });
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: jest.fn((blob: Blob) => { downloadedBlobs.push(blob); return "blob:share-test"; }) });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revoke });
  (historyApi.getWorkoutShareContext as jest.Mock).mockResolvedValue(snapshot);
});

afterEach(async () => {
  if (tree) await act(async () => { tree.unmount(); });
});

async function mount() {
  await act(async () => {
    tree = create(createElement(WorkoutShareDialog, { sessionId: "WOD-selected", profileId: 9 }), {
      createNodeMock: () => ({ querySelectorAll: () => [], addEventListener: jest.fn(), removeEventListener: jest.fn() }),
    });
  });
}

async function click(label: string) {
  const button = tree.root.findAllByType("button").find((node: { children: unknown[] }) => node.children.join("") === label);
  expect(button).toBeDefined();
  await act(async () => { await button.props.onClick(); await tick(); });
}

function preview(): string {
  return tree.root.findByType("textarea").props.value;
}

it("fetches a fresh authenticated snapshot only when opened and resets options after reopening", async () => {
  await mount();
  expect(historyApi.getWorkoutShareContext).not.toHaveBeenCalled();
  await click("AI에 물어보기");
  expect(historyApi.getWorkoutShareContext).toHaveBeenCalledWith("WOD-selected", 9, expect.any(String), expect.any(AbortSignal));
  expect(preview()).toContain("SHARE-AI-ONLY");
  await act(async () => { tree.root.findAllByType("input")[0].props.onChange({ target: { checked: false } }); });
  expect(preview()).not.toContain("SHARE-AI-ONLY");
  await click("닫기");
  expect(tree.root.findAllByType("textarea")).toHaveLength(0);
  expect((historyApi.getWorkoutShareContext as jest.Mock).mock.calls[0][3].aborted).toBe(true);
  await click("AI에 물어보기");
  expect(historyApi.getWorkoutShareContext).toHaveBeenCalledTimes(2);
  expect(tree.root.findAllByType("input").every((input: { props: { checked: boolean } }) => input.props.checked)).toBe(true);
});

it("copies exactly the preview and excludes deselected observations and sensor data", async () => {
  await mount();
  await click("AI에 물어보기");
  expect(preview()).toContain("143");
  await act(async () => {
    for (const checkbox of tree.root.findAllByType("input")) checkbox.props.onChange({ target: { checked: false } });
  });
  const text = preview();
  expect(text).toContain("SHARE-PRESCRIPTION");
  expect(text).not.toContain("SHARE-AI-ONLY");
  expect(text).not.toContain("143");
  await click("복사");
  expect(copyText).toHaveBeenCalledWith(text);
});

it("downloads the current UTF-8 preview and releases the temporary file URL", async () => {
  await mount();
  await click("AI에 물어보기");
  await act(async () => { tree.root.findAllByType("input")[0].props.onChange({ target: { checked: false } }); });
  const text = preview();
  await click("텍스트 파일 저장");
  expect(downloadedBlobs).toHaveLength(1);
  expect(downloadedBlobs[0].type).toBe("text/plain;charset=utf-8");
  expect(await downloadedBlobs[0].text()).toBe(text);
  expect(downloadClick).toHaveBeenCalledTimes(1);
  await tick();
  expect(revoke).toHaveBeenCalledWith("blob:share-test");
});

it("retains the selected preview when clipboard access fails and offers manual copying", async () => {
  copyText.mockRejectedValue(new Error("denied"));
  await mount();
  await click("AI에 물어보기");
  await act(async () => { tree.root.findAllByType("input")[1].props.onChange({ target: { checked: false } }); });
  const text = preview();
  await click("복사");
  expect(preview()).toBe(text);
  expect(tree.root.findAllByType("input")[1].props.checked).toBe(false);
  expect(tree.root.findByProps({ role: "status" }).children.join("")).toContain("공유 내용을 선택해 복사");
});

it("retries failed loading and ignores a response arriving after the dialog closes", async () => {
  (historyApi.getWorkoutShareContext as jest.Mock).mockRejectedValueOnce(new Error("offline"));
  await mount();
  await click("AI에 물어보기");
  expect(tree.root.findAllByType("textarea")).toHaveLength(0);
  expect(tree.root.findByProps({ role: "alert" })).toBeDefined();
  await click("다시 시도");
  expect(preview()).toContain("SHARE-PRESCRIPTION");
  await click("닫기");
  let resolve!: (value: WorkoutShareContext) => void;
  (historyApi.getWorkoutShareContext as jest.Mock).mockReturnValueOnce(new Promise(value => { resolve = value; }));
  await click("AI에 물어보기");
  await click("닫기");
  await act(async () => { resolve(snapshot); await tick(); });
  expect(tree.root.findAllByProps({ role: "dialog" })).toHaveLength(0);
  expect(tree.root.findAllByType("textarea")).toHaveLength(0);
});

it("aborts the old selection and never displays its snapshot after the session changes", async () => {
  let resolveFirst!: (value: WorkoutShareContext) => void;
  (historyApi.getWorkoutShareContext as jest.Mock).mockReturnValueOnce(new Promise(resolve => { resolveFirst = resolve; }));
  await mount();
  await click("AI에 물어보기");
  const firstSignal = (historyApi.getWorkoutShareContext as jest.Mock).mock.calls[0][3];
  (historyApi.getWorkoutShareContext as jest.Mock).mockResolvedValueOnce({
    ...snapshot, session: { ...snapshot.session, session_id: "WOD-second", workout_prescription: "SECOND-PRESCRIPTION" },
  });
  await act(async () => {
    tree.update(createElement(WorkoutShareDialog, { sessionId: "WOD-second", profileId: 9 }));
    await tick();
  });
  expect(firstSignal.aborted).toBe(true);
  expect(preview()).toContain("SECOND-PRESCRIPTION");
  await act(async () => { resolveFirst(snapshot); await tick(); });
  expect(preview()).not.toContain("SHARE-PRESCRIPTION");
  expect(preview()).toContain("SECOND-PRESCRIPTION");
});
