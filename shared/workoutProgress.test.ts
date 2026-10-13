import { confirmedPerformanceInput, observedReps, performanceDraft, type WorkoutProgressInterval, type PerformanceRecord } from "./workoutProgress";
import { withoutWorkoutEvidence } from "./analysisEvidence";
const interval: WorkoutProgressInterval = { id: "set-1", start_secs: 10, end_secs: 70, activity: "exercise", movement: "Power Clean", load: { value: 135, unit: "lb", basis: "total", status: "pending_confirmation" }, observation_refs: [], rep_events: [{ movement: "Power Clean", unit: "reps", start_secs: 12, end_secs: 16, complete: true, evidence: "Complete cycle" }, { movement: "Power Clean", unit: "reps", start_secs: 17, end_secs: 20, complete: false, evidence: "Occluded lockout" }] };
it("uses complete repetition evidence but preserves missing counts rather than zero", () => {
  expect(observedReps(interval)).toBe(1);
  expect(observedReps({ ...interval, rep_events: [] })).toBeNull();
  expect(observedReps({ ...interval, rep_events: [{ ...interval.rep_events![0], unit: "seconds" }] })).toBeNull();
  expect(performanceDraft({ ...interval, rep_events: [] }).reps).toBe("");
});
it("requires explicit review and actual load, retaining original units without changing evidence", () => {
  const before = JSON.stringify(interval);
  const draft = performanceDraft(interval);
  expect(confirmedPerformanceInput(interval, draft)).toBeNull();
  expect(confirmedPerformanceInput(interval, { ...draft, reviewed: true, reps: "5" })).toEqual({ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, measurement: "set", technique: "unspecified" });
  expect(JSON.stringify(interval)).toBe(before);
});
it.each([{ start: "9" }, { end: "71" }, { end: "10" }, { reps: "2.5" }, { reps: "0" }, { reps: "" }, { weight: "" }, { weight: "0" }, { loadMode: "unresolved" as const }])("rejects invalid reviewed values %j", values => {
  expect(confirmedPerformanceInput(interval, { ...performanceDraft(interval), reviewed: true, ...values })).toBeNull();
});
it("distinguishes unknown load from an explicitly confirmed unweighted movement", () => {
  const unweighted = { ...interval, load: null };
  expect(performanceDraft(unweighted).loadMode).toBe("unresolved");
  expect(confirmedPerformanceInput(unweighted, { ...performanceDraft(unweighted), reviewed: true, loadMode: "none" })?.load).toBeNull();
});
it("resets stale confirmed values to current candidate evidence", () => {
  const stale = { status: "needs_confirmation", start_secs: 20, end_secs: 40, reps: 99, load: { value: 225, unit: "lb", basis: "total" } } as PerformanceRecord;
  expect(performanceDraft(interval, stale)).toMatchObject({ start: "10", end: "70", reps: "1", weight: "135", reviewed: false });
});
it("hides machine evidence only in display text while retaining ordinary JSON and later prose", () => {
  const raw = 'Summary\n```workout_observations\n{"secret":"observations"}\n```\n```wod_evidence\n{"cycles":3}\n```\n```json\n{"workout_progress":{"version":1}}\n```\nAfter';
  expect(withoutWorkoutEvidence(raw)).toBe("Summary\nAfter");
  expect(raw).toContain('"cycles":3');
  expect(withoutWorkoutEvidence('```json\n{"example":1}\n```')).toContain('"example":1');
});

describe("observed multi-interval performance windows", () => {
  const anchor: WorkoutProgressInterval = { ...interval, end_secs: 20 };
  const rest: WorkoutProgressInterval = { ...interval, id: "rest", start_secs: 20, end_secs: 40, activity: "rest", movement: null, load: null, rep_events: [] };
  const later: WorkoutProgressInterval = { ...interval, id: "later", start_secs: 40, end_secs: 70 };
  const progress = { version: 1 as const, clock: "media" as const, source_version: "a", intervals: [anchor, rest, later], unassessed: [] };
  const draft = { ...performanceDraft(anchor), reviewed: true, reps: "5", end: "70", measurement: "window" as const };
  it("permits a 60-second Power Clean window including observed rest without changing observations", () => {
    const before = JSON.stringify(progress);
    expect(confirmedPerformanceInput(anchor, draft, progress)).toMatchObject({ start_secs: 10, end_secs: 70, reps: 5, measurement: "window" });
    expect(JSON.stringify(progress)).toBe(before);
    expect(confirmedPerformanceInput(anchor, { ...draft, measurement: "set" }, progress)).toBeNull();
    expect(confirmedPerformanceInput(anchor, draft)).toBeNull();
  });
  it.each([
    { ...progress, intervals: [anchor, { ...rest, activity: "unknown" as const }, later] },
    { ...progress, intervals: [anchor, rest, { ...later, movement: "Double-under" }] },
    { ...progress, intervals: [anchor, { ...rest, start_secs: 21 }, later] },
    { ...progress, intervals: [anchor, rest, { ...later, load: { value: 155, unit: "lb" as const, basis: "total" as const } }] },
    { ...progress, unassessed: [{ start_secs: 30, end_secs: 35, clock: "media", reason: "camera_gap" }] },
    { ...progress, unassessed: [{ start_secs: 30, end_secs: 35, clock: "media", reason: "analysis_failed" }] },
    { ...progress, unassessed: [{ start_secs: 30, end_secs: 35, clock: "capture", reason: "camera_gap" }] },
  ])("rejects missing coverage, a different movement/load or a recording gap", invalid => {
    expect(confirmedPerformanceInput(anchor, draft, invalid)).toBeNull();
  });
  it("lets users confirm repetitions when count-only evidence is incomplete", () => {
    expect(confirmedPerformanceInput(anchor, draft, { ...progress, unassessed: [{ start_secs: 12, end_secs: 16, clock: "media", reason: "incomplete_cycle" }] })).not.toBeNull();
  });
  it("requires contiguous capture mappings across the full selected range", () => {
    const mapped = { ...progress, intervals: progress.intervals.map(row => ({ ...row, capture_start_secs: row.start_secs + 100, capture_end_secs: row.end_secs + 100 })) };
    expect(confirmedPerformanceInput(mapped.intervals[0], draft, mapped)).not.toBeNull();
    expect(confirmedPerformanceInput(mapped.intervals[0], draft, { ...mapped, intervals: mapped.intervals.map((row, i) => i === 2 ? { ...row, capture_start_secs: 150, capture_end_secs: 180 } : row) })).toBeNull();
    expect(confirmedPerformanceInput(mapped.intervals[0], draft, { ...mapped, unassessed: [{ start_secs: 125, end_secs: 130, clock: "capture", reason: "camera_gap" }] })).toBeNull();
    expect(confirmedPerformanceInput(mapped.intervals[0], draft, { ...mapped, intervals: [mapped.intervals[0], rest, mapped.intervals[2]] })).toBeNull();
  });
  it("permits a whole interval with verified different-clock duration but rejects its unverified subwindow", () => {
    const mappedAnchor = { ...anchor, capture_start_secs: 110, capture_end_secs: 125 };
    const p = { ...progress, intervals: [mappedAnchor] };
    expect(confirmedPerformanceInput(mappedAnchor, { ...draft, end: "20" }, p)).not.toBeNull();
    expect(confirmedPerformanceInput(mappedAnchor, { ...draft, start: "12", end: "20" }, p)).toBeNull();
  });
});
it("rejects a selected mapped span whose rounded endpoint makes elapsed time nonpositive", () => {
  const mapped = { ...interval, start_secs: 0, end_secs: 20, capture_start_secs: 0, capture_end_secs: 19.995 };
  const progress = { version: 1 as const, clock: "media" as const, source_version: "mapped", intervals: [mapped], unassessed: [] };
  expect(confirmedPerformanceInput(mapped, { ...performanceDraft(mapped), start: "19.999", end: "20", reps: "1", reviewed: true }, progress)).toBeNull();
});
