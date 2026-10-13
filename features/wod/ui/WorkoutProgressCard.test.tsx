import React from "react";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import { WorkoutProgressCard } from "./WorkoutProgressCard";
import { confirmPerformanceRecords, getPerformanceBenchmarks, getWorkoutProgress } from "../api";
import type { WorkoutProgressResponse } from "../../../shared/workoutProgress";
jest.mock("../api", () => ({ confirmPerformanceRecords: jest.fn(), getPerformanceBenchmarks: jest.fn(), getWorkoutProgress: jest.fn() }));
jest.mock("@/features/i18n", () => ({ useLocale: () => "en", t: (key: string, args?: Record<string, unknown>) => key + (args ? JSON.stringify(args) : "") }));
const evidence: WorkoutProgressResponse = { progress: { version: 1, clock: "media", source_version: "source-a", intervals: [{ id: "set-1", start_secs: 10, end_secs: 70, activity: "exercise", movement: "Power Clean", load: { value: 135, unit: "lb", basis: "total", status: "pending_confirmation" }, observation_refs: [], rep_events: [] }], unassessed: [{ start_secs: 70, end_secs: 80, clock: "media", reason: "camera_gap" }] }, records: [] };
beforeEach(() => { jest.clearAllMocks(); (getWorkoutProgress as jest.Mock).mockResolvedValue(evidence); (getPerformanceBenchmarks as jest.Mock).mockResolvedValue({ records: [], comparisons: [], best: [] }); });
async function open(view: ReturnType<typeof render>) { fireEvent.press(view.getByText(/workoutProgress.title/)); await waitFor(() => expect(view.getByText(/Power Clean/)).toBeTruthy()); }
async function review(view: ReturnType<typeof render>) { await act(async () => { fireEvent.press(view.getByText("workoutProgress.edit")); }); await act(async () => { fireEvent.changeText(view.getByLabelText("workoutProgress.reps"), "5"); fireEvent.press(view.getByLabelText("workoutProgress.reviewed")); }); }
it("fetches only on expansion, shows unknown counts and never automatically confirms", async () => {
  const view = render(<WorkoutProgressCard sessionId="new" profileId={1} />);
  expect(getWorkoutProgress).not.toHaveBeenCalled();
  await open(view);
  expect(view.getByText('workoutProgress.observedReps{"count":"workoutProgress.unknown"}')).toBeTruthy();
  expect(view.getByText(/workoutProgress.unassessed/)).toBeTruthy();
  expect(confirmPerformanceRecords).not.toHaveBeenCalled();
});
it("saves only reviewed values and refreshes the movement benchmark", async () => {
  (confirmPerformanceRecords as jest.Mock).mockResolvedValue(evidence);
  const view = render(<WorkoutProgressCard sessionId="new" profileId={1} />); await open(view); await review(view);
  await act(async () => { fireEvent.press(view.getByText("workoutProgress.save")); });
  await waitFor(() => expect(confirmPerformanceRecords).toHaveBeenCalledWith("new", { source_version: "source-a", records: [{ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, measurement: "set", technique: "unspecified" }] }));
  await waitFor(() => expect(view.getByText("workoutProgress.saved")).toBeTruthy());
  expect(getPerformanceBenchmarks).toHaveBeenCalled();
});
it("discards the old editor and requires a new review after a 409", async () => {
  (confirmPerformanceRecords as jest.Mock).mockRejectedValue({ status: 409 });
  const view = render(<WorkoutProgressCard sessionId="new" profileId={1} />); await open(view); await review(view);
  (getWorkoutProgress as jest.Mock).mockResolvedValue({ ...evidence, progress: { ...evidence.progress!, source_version: "source-b" } });
  await act(async () => { fireEvent.press(view.getByText("workoutProgress.save")); });
  await waitFor(() => expect(view.getByText("workoutProgress.stale")).toBeTruthy());
  expect(view.queryByLabelText("workoutProgress.reviewed")).toBeNull();
  expect(confirmPerformanceRecords).toHaveBeenCalledTimes(1);
  await review(view); await act(async () => { fireEvent.press(view.getByText("workoutProgress.save")); });
  await waitFor(() => expect((confirmPerformanceRecords as jest.Mock).mock.calls[1][1].source_version).toBe("source-b"));
});
it("ignores a completed save from a previous session", async () => {
  let finish!: (result: WorkoutProgressResponse) => void;
  (confirmPerformanceRecords as jest.Mock).mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const view = render(<WorkoutProgressCard sessionId="old" profileId={1} />); await open(view); await review(view); await act(async () => { fireEvent.press(view.getByText("workoutProgress.save")); });
  view.rerender(<WorkoutProgressCard sessionId="new" profileId={1} />);
  await waitFor(() => expect(getWorkoutProgress).toHaveBeenCalledWith("new", expect.anything()));
  await act(async () => finish(evidence));
  expect(view.queryByText("workoutProgress.saved")).toBeNull();
});
it("keeps an unsaved draft on ordinary failure", async () => {
  (confirmPerformanceRecords as jest.Mock).mockRejectedValue(new Error("offline"));
  const view = render(<WorkoutProgressCard sessionId="new" profileId={1} />); await open(view); await review(view); await act(async () => { fireEvent.press(view.getByText("workoutProgress.save")); });
  await waitFor(() => expect(view.getByText("workoutProgress.failed")).toBeTruthy());
  expect(view.getByLabelText("workoutProgress.reps").props.value).toBe("5");
});
it("routes a selected original video interval for review", async () => {
  const play = jest.fn(); const view = render(<WorkoutProgressCard sessionId="new" profileId={1} onReviewVideo={play} />); await open(view); fireEvent.press(view.getAllByText("workoutProgress.play")[0]); expect(play).toHaveBeenCalledWith(10, 70);
});
it("offers a retry when timeline loading fails without presenting zero totals", async () => {
  (getWorkoutProgress as jest.Mock).mockRejectedValueOnce(new Error("offline"));
  const view = render(<WorkoutProgressCard sessionId="new" profileId={1} />); fireEvent.press(view.getByText(/workoutProgress.title/));
  await waitFor(() => expect(view.getByText("workoutProgress.failed")).toBeTruthy());
  expect(view.queryByText(/workoutProgress.observedReps/)).toBeNull();
  fireEvent.press(view.getByText("workoutProgress.refresh")); await waitFor(() => expect(view.getByText(/Power Clean/)).toBeTruthy());
});
it("shows benchmark fetch failure separately and lets the user retry it", async () => {
  (getPerformanceBenchmarks as jest.Mock).mockRejectedValueOnce(new Error("offline"));
  const view = render(<WorkoutProgressCard sessionId="new" profileId={1} />); await open(view);
  await act(async () => fireEvent.press(view.getByText("workoutProgress.benchmarks")));
  await waitFor(() => expect(view.getByText("workoutProgress.benchmarkFailed")).toBeTruthy());
  fireEvent.press(view.getByText("workoutProgress.retry")); await waitFor(() => expect(view.getByText("workoutProgress.noRecords")).toBeTruthy());
  expect(confirmPerformanceRecords).not.toHaveBeenCalled();
});
it("identifies best groups by repetition count and actual elapsed window", async () => {
  const template = { session_id: "old", interval_id: "set", source_version: "a", status: "confirmed", movement: "Power Clean", start_secs: 0, end_secs: 60, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, load_kg: 61.2, technique: "singles", workout_prescription: "same WOD", rate_basis: "normalized_per_minute", median_cycle_seconds: null, performed_at: null, confirmed_at: "2026-10-13T00:00:00Z" };
  const best = [
    { ...template, id: 1, measurement: "set", elapsed_seconds: 60, reps_per_minute: 5 },
    { ...template, id: 2, measurement: "set", elapsed_seconds: 60, reps: 8, reps_per_minute: 8 },
    { ...template, id: 3, measurement: "window", elapsed_seconds: 60, reps_per_minute: 5 },
    { ...template, id: 4, measurement: "window", elapsed_seconds: 30, reps_per_minute: 10 },
  ];
  (getPerformanceBenchmarks as jest.Mock).mockResolvedValue({ records: [], comparisons: [], best });
  const view = render(<WorkoutProgressCard sessionId="new" profileId={1} />); await open(view);
  await act(async () => fireEvent.press(view.getByText("workoutProgress.benchmarks")));
  await waitFor(() => expect(view.getAllByText(/workoutProgress.best/)).toHaveLength(4));
  const rows = view.getAllByText(/workoutProgress.best/).map(row => JSON.stringify(row.props.children));
  expect(rows[0]).toContain('\\"count\\":5'); expect(rows[0]).toContain('\\"duration\\":\\"60.0\\"');
  expect(rows[1]).toContain('\\"count\\":8');
  expect(rows[2]).toContain('\\"duration\\":\\"60.0\\"');
  expect(rows[3]).toContain('\\"duration\\":\\"30.0\\"');
});
it("reviews and saves a 60-second same-movement window with observed rest", async () => {
  const first = { ...evidence.progress!.intervals[0], end_secs: 20 };
  const progress = { ...evidence.progress!, intervals: [first, { ...first, id: "rest", start_secs: 20, end_secs: 40, activity: "rest" as const, movement: null, load: null }, { ...first, id: "later", start_secs: 40, end_secs: 70 }], unassessed: [] };
  (getWorkoutProgress as jest.Mock).mockResolvedValue({ progress, records: [] });
  (confirmPerformanceRecords as jest.Mock).mockResolvedValue({ progress, records: [] });
  const play = jest.fn(); const view = render(<WorkoutProgressCard sessionId="new" profileId={1} onReviewVideo={play} />);
  fireEvent.press(view.getByText(/workoutProgress.title/)); await waitFor(() => expect(view.getAllByText(/Power Clean/)).toHaveLength(2));
  await act(async () => fireEvent.press(view.getAllByText("workoutProgress.edit")[0]));
  await act(async () => { fireEvent.changeText(view.getByLabelText("workoutProgress.end"), "70"); fireEvent.changeText(view.getByLabelText("workoutProgress.reps"), "5"); fireEvent.press(view.getByText("workoutProgress.window")); });
  fireEvent.press(view.getByText("workoutProgress.selectedPlay")); expect(play).toHaveBeenCalledWith(10, 70);
  await act(async () => fireEvent.press(view.getByLabelText("workoutProgress.reviewed")));
  await act(async () => fireEvent.press(view.getByText("workoutProgress.save")));
  await waitFor(() => expect(confirmPerformanceRecords).toHaveBeenCalledWith("new", { source_version: "source-a", records: [{ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, measurement: "window", technique: "unspecified" }] }));
});
it("displays observed rest duration and before/after cycle times separately, preserving nulls", async () => {
  const rest_comparisons = [
    { rest_start_secs: 20, rest_end_secs: 40, rest_elapsed_seconds: 20, before_median_cycle_seconds: 4, after_median_cycle_seconds: 6, cycle_change_percent: 50 },
    { rest_start_secs: 45, rest_end_secs: 50, rest_elapsed_seconds: null, before_median_cycle_seconds: null, after_median_cycle_seconds: null, cycle_change_percent: null },
  ];
  const record = { id: 4, session_id: "new", interval_id: "set-1", source_version: "source-a", status: "confirmed", movement: "Power Clean", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, load_kg: 61.2, measurement: "window", technique: "singles", workout_prescription: "same WOD", reps_per_minute: 5, rate_basis: "actual_60_second_window", median_cycle_seconds: 5, performed_at: null, confirmed_at: "2026-10-13T00:00:00Z", rest_comparisons };
  (getWorkoutProgress as jest.Mock).mockResolvedValue({ ...evidence, records: [record] });
  const view = render(<WorkoutProgressCard sessionId="new" profileId={1} />); await open(view);
  const rows = view.getAllByText(/workoutProgress.restCycles/).map(row => JSON.stringify(row.props.children));
  expect(rows).toHaveLength(2);
  expect(rows[0]).toContain('workoutProgress.restSeconds'); expect(rows[0]).toContain('4.00'); expect(rows[0]).toContain('6.00'); expect(rows[0]).toContain('+50.0%');
  expect(rows[1]).toContain('workoutProgress.unknown'); expect(rows[1]).not.toContain('0.00'); expect(rows[1]).not.toContain('%');
  expect(rows.join('')).not.toContain('recovery');
});
