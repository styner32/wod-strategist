import React from "react";
import { act, cleanup, render } from "@testing-library/react-native";
import { ActivitySummaryCard } from "../ui/ActivitySummaryCard";
import type { ActivitySummary } from "../../../shared/activity";

const mockFetchActivitySummary = jest.fn();

jest.mock("@/features/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params?.count !== undefined ? `${key}:${params.count}` : key,
  useLocale: () => "en",
}));

jest.mock("../api", () => ({
  fetchActivitySummary: (...args: unknown[]) => mockFetchActivitySummary(...args),
}));

function makeSummary(
  overrides: Partial<ActivitySummary> = {},
): ActivitySummary {
  return {
    version: 1,
    available: true,
    source_version: "src-1",
    review_version: 1,
    review_state: "completed",
    coverage_scope: "recorded_chunks",
    movements: [{ movement: "Burpee", unit: "reps", count: 15, seconds: 0 }],
    unassessed: [],
    reviews: [],
    ...overrides,
  };
}

describe("ActivitySummaryCard", () => {
  beforeEach(() => {
    jest.useFakeTimers();
    jest.setSystemTime(new Date("2026-11-05T01:34:00.000Z"));
    mockFetchActivitySummary.mockReset();
  });

  afterEach(() => {
    cleanup();
    jest.clearAllTimers();
    jest.useRealTimers();
  });

  it("renders completed initialSummary immediately without calling fetchActivitySummary", async () => {
    const summary = makeSummary({ review_state: "completed" });
    const view = render(
      <ActivitySummaryCard
        sessionId="WOD-20261105-01TEST"
        profileId={1}
        initialSummary={summary}
      />,
    );
    await act(async () => {
      await jest.advanceTimersByTimeAsync(15_000);
    });

    expect(mockFetchActivitySummary).not.toHaveBeenCalled();
    expect(view.getByText("Burpee: activity.reps:15")).toBeTruthy();
  });

  it("skips fetchActivitySummary when initialSummary is unavailable", async () => {
    const summary = makeSummary({
      available: false,
      review_state: "unavailable",
      movements: [],
    });
    render(
      <ActivitySummaryCard
        sessionId="WOD-20261105-01UNAVAIL"
        profileId={1}
        initialSummary={summary}
      />,
    );
    await act(async () => {
      await jest.advanceTimersByTimeAsync(15_000);
    });

    expect(mockFetchActivitySummary).not.toHaveBeenCalled();
  });

  it("defers polling by 5s when initialSummary is provisional and stops once completed", async () => {
    const provisional = makeSummary({
      review_state: "provisional",
      movements: [{ movement: "Burpee", unit: "reps", count: 10, seconds: 0 }],
    });
    const completed = makeSummary({
      review_state: "completed",
      movements: [{ movement: "Burpee", unit: "reps", count: 12, seconds: 0 }],
    });
    mockFetchActivitySummary.mockResolvedValue(completed);

    const view = render(
      <ActivitySummaryCard
        sessionId="WOD-20261105-01PROV"
        profileId={1}
        initialSummary={provisional}
      />,
    );

    expect(mockFetchActivitySummary).not.toHaveBeenCalled();
    expect(view.getByText("Burpee: activity.reps:10")).toBeTruthy();

    await act(async () => {
      await jest.advanceTimersByTimeAsync(4_999);
    });
    expect(mockFetchActivitySummary).not.toHaveBeenCalled();

    await act(async () => {
      await jest.advanceTimersByTimeAsync(1);
    });
    expect(mockFetchActivitySummary).toHaveBeenCalledTimes(1);
    expect(view.getByText("Burpee: activity.reps:12")).toBeTruthy();

    await act(async () => {
      await jest.advanceTimersByTimeAsync(15_000);
    });
    expect(mockFetchActivitySummary).toHaveBeenCalledTimes(1);
  });

  it("fetches immediately on mount when initialSummary is omitted", async () => {
    mockFetchActivitySummary.mockResolvedValue(
      makeSummary({ review_state: "completed" }),
    );
    const view = render(
      <ActivitySummaryCard sessionId="WOD-20261105-01FALLBACK" profileId={1} />,
    );
    await act(async () => {});

    expect(mockFetchActivitySummary).toHaveBeenCalledTimes(1);
    expect(view.getByText("Burpee: activity.reps:15")).toBeTruthy();
  });
});
