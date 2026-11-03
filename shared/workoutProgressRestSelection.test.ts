import { confirmedPerformanceInput, performanceDraft, restSelectionCovered, type WorkoutProgress, type WorkoutProgressInterval } from "./workoutProgress";

const interval: WorkoutProgressInterval = { id: "first", activity: "exercise", movement: "Burpee", start_secs: 0, end_secs: 10, load: null, observation_refs: [] };
const second: WorkoutProgressInterval = { ...interval, id: "second", start_secs: 10, end_secs: 20, movement: "DB Lunge" };
const progress = (intervals: WorkoutProgressInterval[] = [interval, second]): WorkoutProgress => ({ version: 1, clock: "media", source_version: "source", intervals, unassessed: [] });
const scaled = progress([{ ...interval, capture_start_secs: 0, capture_end_secs: 20 }, { ...second, capture_start_secs: 20, capture_end_secs: 40 }]);
const identity = progress([{ ...interval, capture_start_secs: 100, capture_end_secs: 110 }, { ...second, capture_start_secs: 110, capture_end_secs: 120 }]);

describe("rest confirmation timing validation", () => {
  it("accepts all selected full mapped spans with different activities, movements and loads", () => {
    expect(restSelectionCovered(scaled.intervals[0], 0, 20, scaled)).toBe(true);
    const draft = { ...performanceDraft(scaled.intervals[0]), outcome: "rest" as const, reviewed: true, end: "20" };
    expect(confirmedPerformanceInput(scaled.intervals[0], draft, scaled)).toMatchObject({ outcome: "rest", start_secs: 0, end_secs: 20, reps: null, load: null });
  });

  it("accepts identity-mapped partial selections but refuses scaled partial timing", () => {
    expect(restSelectionCovered(identity.intervals[0], 5, 15, identity)).toBe(true);
    expect(restSelectionCovered(scaled.intervals[0], 5, 15, scaled)).toBe(false);
  });

  it.each([
    ["discontinuous capture", { ...identity, intervals: [identity.intervals[0], { ...identity.intervals[1], capture_start_secs: 115, capture_end_secs: 125 }] }],
    ["partial capture mapping", { ...identity, intervals: [identity.intervals[0], { ...second, capture_start_secs: 110 }] }],
    ["mixed mapped and unmapped", { ...identity, intervals: [identity.intervals[0], second] }],
    ["explicit capture gap", { ...identity, unassessed: [{ clock: "capture", reason: "camera_gap", start_secs: 105, end_secs: 106 }] }],
    ["unmapped with capture gap", { ...progress(), unassessed: [{ clock: "capture", reason: "camera_gap", start_secs: 30, end_secs: 35 }] }],
    ["unverified mapping", { ...identity, unassessed: [{ clock: "media", reason: "unverified_capture_mapping", start_secs: 5, end_secs: 6 }] }],
    ["uncovered hole", progress([interval, { ...second, start_secs: 12 }])],
  ])("rejects %s", (_reason, invalid) => {
    expect(restSelectionCovered(invalid.intervals[0], 0, 20, invalid)).toBe(false);
  });

  it("allows explicit unmapped unassessed coverage but never invents a neighboring mapping", () => {
    const synthetic = { ...interval, id: "unassessed-10-20", start_secs: 10, end_secs: 20, activity: "unknown" as const, movement: null };
    const source = { ...progress([interval]), unassessed: [{ clock: "media", reason: "not_analyzed", start_secs: 10, end_secs: 20 }] };
    expect(restSelectionCovered(synthetic, 10, 20, source)).toBe(true);
    expect(restSelectionCovered(interval, 0, 20, source)).toBe(true);
    expect(restSelectionCovered(identity.intervals[0], 0, 20, { ...source, intervals: [identity.intervals[0]] })).toBe(false);
    expect(restSelectionCovered(interval, 0, 21, source)).toBe(false);
    expect(restSelectionCovered(interval, 0, 20)).toBe(false);
  });
});
