jest.mock("react", () => jest.requireActual("../../node_modules/react"));
import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WODTrainingPanel } from "../src/history/components/WODTrainingPanel";
import { historyApi } from "../src/api/history";
jest.mock("../src/api/history", () => ({ historyApi: { getWODTraining: jest.fn(), getWODTrainingLoad: jest.fn(), updateWODEffort: jest.fn(), createSessionReanalysis: jest.fn(), getSessionReanalysis: jest.fn(), applySessionReanalysis: jest.fn() } }));
const { act, create } = require("react-test-renderer");
const score = { evaluation_version: 1, intensity: 80, overall: 85, wod_start: { seconds: 120, end_seconds: 1320, status: "confirmed", source: "video", evidence: "First visible burpee" } };
const training = { eligible: true, effort: 8, session_load: 160, score, wod_start: score.wod_start };
const windowData = { daily_average: 10, missing_effort: 0, missing_duration: 0 };
beforeEach(() => {
  jest.clearAllMocks();
  Object.defineProperty(global, "localStorage", { configurable: true, value: { getItem: jest.fn(() => null), setItem: jest.fn(), removeItem: jest.fn() } });
  Object.defineProperty(global, "crypto", { configurable: true, value: { randomUUID: () => "wod-request" } });
  (historyApi.getWODTraining as jest.Mock).mockResolvedValue(training);
  (historyApi.getWODTrainingLoad as jest.Mock).mockResolvedValue({ comparison: { seven: windowData, twenty_eight: windowData, change_percent: null, status: "collecting_baseline" }, missing_workout_time: 0 });
});
async function mount(raw = JSON.stringify(score)) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false, gcTime: Infinity } } });
  let tree: any;
  await act(async () => { tree = create(createElement(QueryClientProvider, { client }, createElement(WODTrainingPanel, { sessionId: "new", profileId: 1, scoreJSON: raw, currentTime: 135, isOriginal: true, seek: jest.fn() }))); });
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); });
  return { tree, client };
}
async function click(tree: any, label: string) {
  const button = tree.root.findAllByType("button").find((b: any) => b.children.join("") === label);
  expect(button).toBeDefined();
  await act(async () => { button.props.onClick(); });
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); });
}
it("hides controls and makes no API calls for historical WODs", async () => {
  const { tree, client } = await mount('{"intensity":80}');
  expect(tree.toJSON()).toBeNull();expect(historyApi.getWODTraining).not.toHaveBeenCalled();
  await act(() => tree.unmount());client.clear();
});
it("saves effort and refreshes the 7/28-day comparison", async () => {
  (historyApi.updateWODEffort as jest.Mock).mockResolvedValue({ ...training, effort: 4, session_load: 80 });
  const { tree, client } = await mount();
  await click(tree, "4");await click(tree, "Effort 저장");
  expect(historyApi.updateWODEffort).toHaveBeenCalledWith("new", 4);
  expect(historyApi.getWODTrainingLoad).toHaveBeenCalledTimes(2);
  await act(() => tree.unmount());client.clear();
});
it("corrects from the original player and automatically applies the completed candidate once", async () => {
  (historyApi.createSessionReanalysis as jest.Mock).mockResolvedValue({ run_id: 17 });
  (historyApi.getSessionReanalysis as jest.Mock).mockResolvedValue({ id: 17, status: "COMPLETED" });
  (historyApi.applySessionReanalysis as jest.Mock).mockResolvedValue({ run_id: 17 });
  const { tree, client } = await mount();
  await click(tree, "현재 영상 위치 지정");await click(tree, "시작 수정 후 재평가");
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 30)); });
  expect(historyApi.createSessionReanalysis).toHaveBeenCalledWith("new", "wod-request", undefined, undefined, undefined, 135);
  expect(historyApi.applySessionReanalysis).toHaveBeenCalledTimes(1);
  expect(historyApi.applySessionReanalysis).toHaveBeenCalledWith("new", 17);
  await act(() => tree.unmount());client.clear();
});
