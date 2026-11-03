import {
  findCoveringConfirmedRecord,
  summarizeWorkoutMovements,
  type PerformanceRecord,
  type ProgressRepEvent,
  type WorkoutProgress,
  type WorkoutProgressInterval,
} from "./workoutProgress";

function rep(start: number, end: number): ProgressRepEvent {
  return { movement: "Power Clean", unit: "reps", start_secs: start, end_secs: end, complete: true, evidence: "visible completion" };
}

function exercise(overrides: Partial<WorkoutProgressInterval> = {}): WorkoutProgressInterval {
  return { id: "exercise", start_secs: 0, end_secs: 30, activity: "exercise", movement: "Power Clean", load: null, observation_refs: [], rep_events: [rep(2, 5), rep(12, 15), rep(22, 25)], ...overrides };
}

function progress(intervals: WorkoutProgressInterval[]): WorkoutProgress {
  return { version: 1, clock: "media", source_version: "source", intervals, unassessed: [] };
}

function rest(id: number, start: number, end: number, overrides: Partial<PerformanceRecord> = {}): PerformanceRecord {
  return {
    id, session_id: "session", interval_id: "exercise", source_version: "source", status: "confirmed", outcome: "rest", movement: "",
    start_secs: start, end_secs: end, reps: null, load: null, load_kg: null, measurement: "set", technique: "unspecified",
    workout_prescription: "", reps_per_minute: null, rate_basis: null, median_cycle_seconds: null, performed_at: null,
    confirmed_at: "2026-10-31T00:00:00Z", ...overrides,
  };
}

describe("movement summaries after rest confirmation", () => {
  it("keeps both sides of a partial rest confirmation as one original exercise interval", () => {
    const interval = exercise();
    const confirmation = rest(1, 10, 20);
    expect(findCoveringConfirmedRecord(interval, [confirmation])).toBe(confirmation);
    expect(summarizeWorkoutMovements(progress([interval]), [confirmation])).toEqual([
      { movement: "Power Clean", intervalCount: 1, observedReps: 2, observedSeconds: 0, confirmedCount: 0, confirmedReps: 0, loadLabel: null },
    ]);
  });

  it.each([
    ["leading", 0, 10],
    ["trailing", 20, 30],
  ])("removes only repetitions inside a %s rest confirmation", (_label, start, end) => {
    expect(summarizeWorkoutMovements(progress([exercise()]), [rest(1, start as number, end as number)])[0]).toMatchObject({ intervalCount: 1, observedReps: 2 });
  });

  it("removes the original exercise only when confirmed rest covers its entire duration", () => {
    expect(summarizeWorkoutMovements(progress([exercise()]), [rest(1, 0, 30)])).toEqual([]);
  });

  it("owns repetitions by their completion in (rest start, rest end]", () => {
    const interval = exercise({ rep_events: [rep(8, 10), rep(9, 11), rep(19, 20), rep(19, 21)] });
    expect(summarizeWorkoutMovements(progress([interval]), [rest(1, 10, 20)])[0].observedReps).toBe(2);
  });

  it("clips seconds to surviving exercise and avoids subtracting overlapping rest twice", () => {
    const interval = exercise({ movement: "Walking Lunge", rep_events: [{ movement: "Walking Lunge", unit: "seconds", start_secs: 2, end_secs: 28, complete: true, evidence: "continuous visible movement" }] });
    const records = [rest(1, 8, 12), rest(2, 5, 15), rest(3, 15, 20), rest(4, 25, 30)]
      .map(record => ({ ...record, interval_id: `rest-${record.id}` }));
    expect(summarizeWorkoutMovements(progress([interval]), records)[0]).toMatchObject({ intervalCount: 1, observedReps: 0, observedSeconds: 8 });
  });

  it("ignores pending and superseded rest records and preserves confirmed movement corrections and counts", () => {
    const first = exercise({ id: "first", end_secs: 10, rep_events: [rep(2, 5)] });
    const second = exercise({ id: "second", start_secs: 10, end_secs: 30, rep_events: [rep(12, 15), rep(22, 25)] });
    const completed = rest(2, 0, 10, {
      interval_id: "first", outcome: "completed", movement: "Clean and Jerk", reps: 7, reps_per_minute: 42, rate_basis: "normalized_per_minute",
    });
    const records = [rest(1, 2, 8, { interval_id: "first" }), completed, rest(3, 10, 30, { interval_id: "second", status: "needs_confirmation" })];
    expect(summarizeWorkoutMovements(progress([first, second]), records)).toEqual([
      { movement: "Clean and Jerk", intervalCount: 1, observedReps: 1, observedSeconds: 0, confirmedCount: 1, confirmedReps: 7, loadLabel: null },
      { movement: "Power Clean", intervalCount: 1, observedReps: 2, observedSeconds: 0, confirmedCount: 0, confirmedReps: 0, loadLabel: null },
    ]);
  });

  it("does not change source intervals, event bounds, or confirmation records", () => {
    const source = progress([exercise()]);
    const records = [rest(1, 10, 20)];
    const before = JSON.stringify({ source, records });
    summarizeWorkoutMovements(source, records);
    expect(JSON.stringify({ source, records })).toBe(before);
  });
});
