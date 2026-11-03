jest.mock("react", () => jest.requireActual("../../node_modules/react"));
let mockInitialSearch = "";
jest.mock("react-router-dom", () => {
  const React = require("react");
  return {
    Link: ({ to, children, ...props }: any) => React.createElement("a", { href: to, ...props }, children),
    useSearchParams: () => {
      const [value, setValue] = React.useState(mockInitialSearch);
      return [new URLSearchParams(value), (next: URLSearchParams) => setValue(next.toString())];
    },
  };
});
jest.mock("../src/auth/useAuth", () => ({ useAuth: () => ({ user: { profiles: [{ id: 1, name: "One" }, { id: 2, name: "Two" }] } }) }));
jest.mock("../src/history/components/HeartRateSummaryPanel", () => ({ HeartRateBadge: () => null }));
jest.mock("../src/history/components/AnalysisOverview", () => ({ AnalysisOverview: (props: any) => require("react").createElement("div", { "data-read-only": props.readOnly }, props.analysis.output) }));
jest.mock("../src/api/history", () => ({ historyApi: { list: jest.fn(), getTotalCost: jest.fn(), archive: jest.fn(), unarchive: jest.fn() } }));

import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { historyApi, type AnalysisResult } from "../src/api/history";
import { HistoryListPage } from "../src/history/HistoryListPage";
import { HistoryArchiveAction } from "../src/history/components/HistoryArchiveAction";
import { ArchivedSessionContent } from "../src/history/components/ArchivedSessionContent";
import { historyLocation, refreshArchiveQueries } from "../src/history/historyArchive";

const { act, create } = require("react-test-renderer");
const record = (id = 42, archived = false, profileId = 2) => ({ id, profile_id: profileId, session_id: `WOD-${id}`, status: "CANCELLED", output: "saved evidence", created_at: "2026-10-29T00:00:00Z", updated_at: "2026-10-29T00:00:00Z", archived_at: archived ? "2026-10-29T01:00:00Z" : null }) as AnalysisResult;
const wait = () => new Promise(resolve => setTimeout(resolve, 15));
let client: QueryClient;
let tree: ReturnType<typeof create>;
beforeEach(() => {
  jest.clearAllMocks();
  mockInitialSearch = "";
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false, gcTime: Infinity } } });
  Object.defineProperty(global, "window", { configurable: true, value: { confirm: jest.fn(() => true) } });
  delete (global as any).IntersectionObserver;
  (historyApi.getTotalCost as jest.Mock).mockResolvedValue(undefined);
  (historyApi.list as jest.Mock).mockResolvedValue([]);
  (historyApi.archive as jest.Mock).mockResolvedValue({ message: "history archived" });
  (historyApi.unarchive as jest.Mock).mockResolvedValue({ message: "history unarchived" });
});
afterEach(async () => { if (tree) await act(async () => tree.unmount()); client.clear(); });
async function mount(element: ReturnType<typeof createElement>) {
  await act(async () => { tree = create(createElement(QueryClientProvider, { client }, element), { createNodeMock: () => ({}) }); });
  await act(async () => { await wait(); });
}
async function click(label: string) {
  const target = tree.root.findAllByType("button").find((button: any) => button.props.children === label);
  expect(target).toBeDefined();
  await act(async () => target.props.onClick());
  await act(async () => { await wait(); });
}

it("keeps profiles and archive pagination in separate caches and returns to the selected view", async () => {
  mockInitialSearch = "profile_id=2&archived=true";
  (historyApi.list as jest.Mock).mockImplementation(async (pid: number, _before: number, _limit: number, archived: boolean) => [record(pid, archived, pid)]);
  await mount(createElement(HistoryListPage));
  expect(historyApi.list).toHaveBeenCalledWith(2, undefined, undefined, true);
  expect(tree.root.findAllByType("a")[0].props.href).toBe("/sessions/WOD-2?profile_id=2&archived=true");
  expect(tree.root.findAllByType("a")[0].findAllByType("button")).toHaveLength(0);
  expect(tree.root.findAllByType("button").some((button: any) => button.props.children === "복원")).toBe(true);
  await click("운동 기록");
  expect(historyApi.list).toHaveBeenCalledWith(2, undefined, undefined, false);
  await act(async () => tree.root.findByType("select").props.onChange({ target: { value: "1" } }));
  await act(async () => { await wait(); });
  expect(historyApi.list).toHaveBeenCalledWith(1, undefined, undefined, false);
  expect(client.getQueryData(["history-infinite", 2, "archived"])).toBeDefined();
  expect(client.getQueryData(["history-infinite", 2, "active"])).toBeDefined();
  expect(client.getQueryData(["history-infinite", 1, "active"])).toBeDefined();
  expect(historyLocation(2, true)).toBe("/?profile_id=2&archived=true");
});

it("loads the next archive page with the same profile and archive filter", async () => {
  mockInitialSearch = "profile_id=2&archived=true";
  let intersect!: (entries: { isIntersecting: boolean }[]) => void;
  Object.defineProperty(global, "IntersectionObserver", { configurable: true, value: class {
    constructor(callback: typeof intersect) { intersect = callback; }
    observe() {}
    disconnect() {}
  } });
  (historyApi.list as jest.Mock).mockImplementation(async (_pid: number, before?: number) => before == null
    ? Array.from({ length: 20 }, (_, index) => record(100 - index, true))
    : [record(80, true)]);
  await mount(createElement(HistoryListPage));
  await act(async () => { intersect([{ isIntersecting: true }]); });
  await act(async () => { await wait(); });
  expect(historyApi.list).toHaveBeenCalledWith(2, 81, undefined, true);
  expect(tree.root.findAllByType("article")).toHaveLength(21);
});

it("confirms archive, changes cache only after success, and retains records on failure", async () => {
  const row = record();
  const key = ["history-infinite", 2, "active"];
  client.setQueryData(key, { pages: [[row]], pageParams: [undefined] });
  (historyApi.archive as jest.Mock).mockRejectedValueOnce(new Error("offline"));
  await mount(createElement(HistoryArchiveAction, { result: row }));
  await click("보관");
  expect(window.confirm).toHaveBeenCalledWith(expect.stringContaining("피로도·훈련 부하·과거 운동 비교에서 제외합니다"));
  expect(window.confirm).not.toHaveBeenCalledWith(expect.stringContaining("진행 중인 분석을 중단"));
  expect((client.getQueryData(key) as any).pages[0]).toHaveLength(1);
  expect(tree.root.findAllByProps({ role: "alert" })).toHaveLength(1);
  await click("보관");
  expect((client.getQueryData(key) as any).pages[0]).toHaveLength(0);
  expect(historyApi.archive).toHaveBeenCalledTimes(2);
});

it("disables archive while processing and surfaces 409 conflict when server work is active", async () => {
  const pendingRow = { ...record(55), status: "PENDING" };
  await mount(createElement(HistoryArchiveAction, { result: pendingRow }));
  const disabledBtn = tree.root.findByType("button");
  expect(disabledBtn.props.disabled).toBe(true);
  expect(disabledBtn.props.children).toBe("분석 중");
  await act(async () => disabledBtn.props.onClick());
  expect(historyApi.archive).not.toHaveBeenCalled();

  const completedRow = { ...record(56), status: "COMPLETED" };
  const key = ["history-infinite", 2, "active"];
  client.setQueryData(key, { pages: [[completedRow]], pageParams: [undefined] });
  (historyApi.archive as jest.Mock).mockRejectedValueOnce(Object.assign(new Error("session is still processing"), { status: 409 }));
  await act(async () => tree.update(createElement(QueryClientProvider, { client }, createElement(HistoryArchiveAction, { result: completedRow }))));
  await click("보관");
  expect((client.getQueryData(key) as any).pages[0]).toHaveLength(1);
  expect(tree.root.findByProps({ role: "alert" }).children).toContain("진행 중인 분석이 완료된 후 보관할 수 있습니다.");
});

it("refetches after an archive refresh fails so a shortened page cannot lose its next cursor", async () => {
  mockInitialSearch = "profile_id=2";
  let intersect!: (entries: { isIntersecting: boolean }[]) => void;
  Object.defineProperty(global, "IntersectionObserver", { configurable: true, value: class {
    constructor(callback: typeof intersect) { intersect = callback; }
    observe() {}
    disconnect() {}
  } });
  let archived = false, unavailable = false;
  (historyApi.list as jest.Mock).mockImplementation(async (_pid: number, before?: number) => {
    if (unavailable) throw new Error("offline");
    if (before != null) return [record(79)];
    return Array.from({ length: 20 }, (_, index) => record((archived ? 99 : 100) - index));
  });
  (historyApi.archive as jest.Mock).mockImplementation(async () => { archived = true; unavailable = true; return { message: "history archived" }; });
  await mount(createElement(HistoryListPage));
  await click("보관");
  expect(tree.root.findAllByType("article")).toHaveLength(19);
  unavailable = false;
  await click("Retry");
  expect(tree.root.findAllByType("article")).toHaveLength(20);
  expect(tree.root.findAllByType("a")[0].props.href).toContain("WOD-99");
  await act(async () => { intersect([{ isIntersecting: true }]); });
  await act(async () => { await wait(); });
  expect(historyApi.list).toHaveBeenCalledWith(2, 80, undefined, false);
  expect(tree.root.findAllByType("article")).toHaveLength(21);
});

it("does not send a canceled confirmation and restores only on the restore endpoint", async () => {
  (window.confirm as jest.Mock).mockReturnValue(false);
  await mount(createElement(HistoryArchiveAction, { result: record() }));
  await click("보관");
  expect(historyApi.archive).not.toHaveBeenCalled();
  await act(async () => tree.update(createElement(QueryClientProvider, { client }, createElement(HistoryArchiveAction, { result: record(42, true) }))));
  await click("복원");
  expect(historyApi.unarchive).toHaveBeenCalledWith(42);
  expect(historyApi.archive).not.toHaveBeenCalled();
});

it("cancels late history and comparison responses before refreshing archive state", async () => {
  const row = record();
  client.setQueryData(["analysis", row.session_id], [row]);
  client.setQueryData(["session-analysis", row.session_id], { session_id: row.session_id, analysis: row });
  let complete!: (value: unknown) => void;
  const late = client.fetchQuery({ queryKey: ["performance-benchmarks", 2, "Squat"], queryFn: () => new Promise(resolve => { complete = resolve; }) }).catch(() => undefined);
  await wait();
  await refreshArchiveQueries(client, row, true);
  complete({ records: [row] });
  await late;
  expect(client.getQueryData(["performance-benchmarks", 2, "Squat"])).toBeUndefined();
  expect((client.getQueryData(["session-analysis", row.session_id]) as any).analysis.archived_at).toBeTruthy();
  await refreshArchiveQueries(client, row, false);
  expect((client.getQueryData(["analysis", row.session_id]) as AnalysisResult[])[0].archived_at).toBeNull();
});

it("shows archived saved results read-only without benchmark, effort or analysis creation controls", async () => {
  await mount(createElement(ArchivedSessionContent, { analysis: { ...record(42, true), status: "COMPLETED" }, chunks: [], seek: jest.fn(), seekHighlight: jest.fn(), canSeek: false }));
  expect(tree.root.findByProps({ "data-read-only": true }).children).toContain("saved evidence");
  expect(tree.root.findAllByType("button")).toHaveLength(0);
});
