jest.mock("react", () => jest.requireActual("../../node_modules/react"));
jest.mock("../src/history/components/AnalysisMarkdown", () => ({
  AnalysisMarkdown: () => null,
  AnalysisOriginal: () => null,
}));
let mockInitialSearch = "";
jest.mock("react-router-dom", () => {
  const React = require("react");
  return {
    Link: ({ to, children, ...props }: any) => React.createElement("a", { href: to, ...props }, children),
    useParams: () => ({ sessionId: "WOD-1" }),
    useSearchParams: () => {
      const [value, setValue] = React.useState(mockInitialSearch);
      return [new URLSearchParams(value), (next: URLSearchParams) => setValue(next.toString())];
    },
  };
});
jest.mock("../src/auth/useAuth", () => ({
  useAuth: () => ({ user: { profiles: [{ id: 1, name: "One" }] } }),
}));
jest.mock("../src/history/components/HeartRateSummaryPanel", () => ({
  HeartRateBadge: () => null,
  HeartRateSummaryPanel: () => null,
}));
jest.mock("../src/api/history", () => ({
  historyApi: {
    list: jest.fn(),
    getTotalCost: jest.fn(),
    archive: jest.fn(),
    unarchive: jest.fn(),
    updateWODEffort: jest.fn(),
  },
}));

import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { historyApi, type AnalysisResult } from "../src/api/history";
import { HistoryListPage } from "../src/history/HistoryListPage";
import { resolveInitialSessionTab } from "../src/history/SessionDetailPage";

const { act, create } = require("react-test-renderer");
const wait = () => new Promise((resolve) => setTimeout(resolve, 20));

function buildResult(overrides: Partial<AnalysisResult> = {}): AnalysisResult {
  return {
    id: 101,
    profile_id: 1,
    session_id: "WOD-20261104-01TESTSESSION00001",
    status: "COMPLETED",
    workout_type: "wod",
    output: '{"overall_summary":"Solid Fran pacing"}',
    session_score: JSON.stringify({
      evaluation_version: 1,
      intensity: 74,
      overall: 80,
      form: 78,
      consistency: 75,
      wod_start: { seconds: 30, end_seconds: 330, status: "confirmed", source: "video", evidence: "start" },
      evidence_status: "sufficient",
    }),
    created_at: "2026-11-04T06:00:00Z",
    updated_at: "2026-11-04T06:05:00Z",
    archived_at: null,
    ...overrides,
  };
}

let client: QueryClient;
let tree: ReturnType<typeof create>;

beforeEach(() => {
  jest.clearAllMocks();
  mockInitialSearch = "";
  client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false, gcTime: Infinity },
    },
  });
  delete (global as any).IntersectionObserver;
  (historyApi.getTotalCost as jest.Mock).mockResolvedValue(null);
  (historyApi.list as jest.Mock).mockResolvedValue([]);
  (historyApi.updateWODEffort as jest.Mock).mockResolvedValue({
    eligible: true,
    effort: 9,
    wod_start: null,
    session_load: 45,
    score: null,
  });
});

afterEach(async () => {
  if (tree) await act(async () => tree.unmount());
  client.clear();
});

async function mountList() {
  await act(async () => {
    tree = create(
      createElement(QueryClientProvider, { client }, createElement(HistoryListPage)),
      { createNodeMock: () => ({}) },
    );
  });
  await act(async () => {
    await wait();
  });
}

async function clickButton(label: string) {
  const target = tree.root
    .findAllByType("button")
    .find((btn: any) => btn.children.join("") === label);
  expect(target).toBeDefined();
  await act(async () => {
    target.props.onClick();
  });
  await act(async () => {
    await wait();
  });
}

it("switches missing-input filter chips and clears missing filter when opening archive", async () => {
  await mountList();
  expect(historyApi.list).toHaveBeenCalledWith(1, undefined, undefined, false);

  await clickButton("입력 필요");
  expect(historyApi.list).toHaveBeenLastCalledWith(1, undefined, undefined, false, "any");

  await clickButton("Effort 미입력");
  expect(historyApi.list).toHaveBeenLastCalledWith(1, undefined, undefined, false, "effort");

  await clickButton("수행구간 미확정");
  expect(historyApi.list).toHaveBeenLastCalledWith(1, undefined, undefined, false, "performance");

  await clickButton("보관함");
  expect(historyApi.list).toHaveBeenLastCalledWith(1, undefined, undefined, true);
});

it("renders missing badges, deep-links, and inline Effort quick-save without nesting buttons inside links", async () => {
  const row = buildResult({
    heart_rate: {
      status: "completed",
      processing_state: "COMPLETED",
      quality_status: "adequate",
      calculation_version: 3,
      applied: true,
      application_reason: "applied",
      hybrid: {
        hybrid_intensity: 82,
        valid_hybrid_seconds: 180,
        data_coverage: 0.92,
        quality_flags: [],
        calibration_version: "personal_chest_v1",
        algorithm_version: "v1",
      },
    },
    input_status: {
      effort_eligible: true,
      effort: null,
      effort_missing: true,
      exercise_interval_count: 3,
      confirmed_interval_count: 1,
      needs_confirmation_count: 1,
      performance_missing: true,
    },
  });
  (historyApi.list as jest.Mock).mockResolvedValue([row]);

  await mountList();

  const anchors = tree.root.findAllByType("a");
  expect(anchors[0].props.href).toBe(
    `/sessions/${encodeURIComponent(row.session_id)}?profile_id=1`,
  );
  // Primary card link must never contain nested buttons
  expect(anchors[0].findAllByType("button")).toHaveLength(0);

  const hrefs = anchors.map((a: any) => a.props.href);
  expect(hrefs).toContain(
    `/sessions/${encodeURIComponent(row.session_id)}?profile_id=1&tab=performance`,
  );
  expect(hrefs).toContain(
    `/sessions/${encodeURIComponent(row.session_id)}?profile_id=1&tab=details`,
  );

  // Sensor hybrid intensity 82 -> pre-selects Effort 8; user can adjust to 9 or save directly
  const effort8 = tree.root
    .findAllByType("button")
    .find((btn: any) => btn.props["aria-label"] === "Effort 8");
  expect(effort8?.props["aria-pressed"]).toBe(true);

  await clickButton("9");
  await clickButton("Effort 저장");
  expect(historyApi.updateWODEffort).toHaveBeenCalledWith(row.session_id, 9);
});

it("resolves initial SessionDetailPage tab from ?tab= query parameter safely", () => {
  expect(resolveInitialSessionTab("performance")).toBe("performance");
  expect(resolveInitialSessionTab("details")).toBe("details");
  expect(resolveInitialSessionTab("TIMELINE")).toBe("timeline");
  expect(resolveInitialSessionTab("invalid-tab")).toBe("summary");
  expect(resolveInitialSessionTab(null)).toBe("summary");
});
