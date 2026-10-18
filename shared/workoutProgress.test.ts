import { confirmedPerformanceInput, diagnosePerformanceDraft, extractWodLoads, findCoveringConfirmedRecord, getContiguousExerciseSpan, observedReps, performanceDraft, summarizeWorkoutMovements, type WorkoutProgressInterval, type PerformanceRecord } from "./workoutProgress";
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
  expect(confirmedPerformanceInput(interval, { ...draft, reviewed: true, reps: "5" })).toEqual({ interval_id: "set-1", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, measurement: "set", technique: "singles" });
  expect(JSON.stringify(interval)).toBe(before);
});
it.each([{ start: "9" }, { end: "71" }, { end: "10" }, { reps: "2.5" }, { reps: "0" }, { reps: "" }, { weight: "" }, { weight: "0" }, { loadMode: "unresolved" as const }])("rejects invalid reviewed values %j", values => {
  expect(confirmedPerformanceInput(interval, { ...performanceDraft(interval), reviewed: true, ...values })).toBeNull();
});
it("defaults loadMode and basis from movement type while leaving ambiguous movements unresolved", () => {
  const ambiguous = { ...interval, movement: "Squat", load: null };
  expect(performanceDraft(ambiguous).loadMode).toBe("unresolved");

  const airSquat = { ...interval, movement: "Air Squat", load: null };
  const burpee = { ...interval, movement: "Burpee", load: null };
  expect(performanceDraft(airSquat).loadMode).toBe("none");
  expect(performanceDraft(burpee).loadMode).toBe("none");
  expect(confirmedPerformanceInput(airSquat, { ...performanceDraft(airSquat), reviewed: true })?.load).toBeNull();

  const dbSquat = { ...interval, movement: "Dumbbell Squat", load: null };
  const powerClean = { ...interval, movement: "Power Clean", load: null };
  expect(performanceDraft(dbSquat)).toMatchObject({ loadMode: "weighted", weight: "", basis: "per_implement" });
  expect(performanceDraft(powerClean)).toMatchObject({ loadMode: "weighted", weight: "", basis: "total" });
  expect(confirmedPerformanceInput(powerClean, { ...performanceDraft(powerClean), reviewed: true })).toBeNull();
});
it("resets stale confirmed values to current candidate evidence", () => {
  const stale = { status: "needs_confirmation", start_secs: 20, end_secs: 40, reps: 99, load: { value: 225, unit: "lb", basis: "total" } } as PerformanceRecord;
  expect(performanceDraft(interval, stale)).toMatchObject({ start: "10", end: "70", reps: "1", weight: "135", reviewed: false });
});
it("normalizes legacy unspecified technique to singles and finds covering confirmed records across merged spans", () => {
  const confirmedWithUnspecified = { id: 1, session_id: "s1", interval_id: "seg-0", source_version: "a", status: "confirmed", movement: "Power Clean", start_secs: 0, end_secs: 20, reps: 10, load: { value: 135, unit: "lb", basis: "total" }, load_kg: 61.2, measurement: "set", technique: "unspecified", workout_prescription: "test", reps_per_minute: 30, rate_basis: "normalized_per_minute", median_cycle_seconds: null, performed_at: "2026-10-12T00:00:00Z", confirmed_at: "2026-10-12T00:00:00Z" } as PerformanceRecord;
  expect(performanceDraft(interval, confirmedWithUnspecified).technique).toBe("singles");
  const seg0: WorkoutProgressInterval = { ...interval, id: "seg-0", start_secs: 0, end_secs: 10 };
  const seg1: WorkoutProgressInterval = { ...interval, id: "seg-1", start_secs: 10, end_secs: 20 };
  const seg2: WorkoutProgressInterval = { ...interval, id: "seg-2", start_secs: 20, end_secs: 30 };
  expect(findCoveringConfirmedRecord(seg0, [confirmedWithUnspecified])?.id).toBe(1);
  expect(findCoveringConfirmedRecord(seg1, [confirmedWithUnspecified])?.id).toBe(1);
  expect(findCoveringConfirmedRecord(seg2, [confirmedWithUnspecified])).toBeUndefined();
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

describe("extractWodLoads and summarizeWorkoutMovements", () => {
  it("extracts single and slash-separated weights from WOD description and infers load basis", () => {
    expect(extractWodLoads("21-15-9\nThrusters (95/65 lb)\nPull-ups", "Thruster")).toEqual([
      { value: 95, unit: "lb", basis: "total", status: "pending_confirmation" },
      { value: 65, unit: "lb", basis: "total", status: "pending_confirmation" },
    ]);
    expect(extractWodLoads("21-15-9\nThrusters (95/65 lb)\nPull-ups", "Pull-up")).toEqual([]);
    expect(extractWodLoads("AMRAP 12:\n10 Dumbbell Snatches 22.5/15kg", "Dumbbell Snatch")).toEqual([
      { value: 22.5, unit: "kg", basis: "per_implement", status: "pending_confirmation" },
      { value: 15, unit: "kg", basis: "per_implement", status: "pending_confirmation" },
    ]);
    const unweighted = { ...interval, load: null };
    const singleLoad = extractWodLoads("30 Clean and Jerks 135lb", "Clean and Jerk");
    expect(singleLoad).toEqual([{ value: 135, unit: "lb", basis: "total", status: "pending_confirmation" }]);
    expect(performanceDraft(unweighted, undefined, singleLoad[0])).toMatchObject({
      loadMode: "weighted",
      weight: "135",
      unit: "lb",
      basis: "total",
    });
  });

  it("summarizes performed movements with interval counts, observed complete reps, and confirmed reps", () => {
    const intervals: WorkoutProgressInterval[] = [
      interval,
      { ...interval, id: "set-2", start_secs: 80, end_secs: 120, rep_events: [{ movement: "Power Clean", unit: "reps", start_secs: 82, end_secs: 86, complete: true, evidence: "Rep 1" }, { movement: "Power Clean", unit: "reps", start_secs: 88, end_secs: 92, complete: true, evidence: "Rep 2" }] },
      { id: "rest-1", start_secs: 70, end_secs: 80, activity: "rest", movement: null, load: null, observation_refs: [] },
      { id: "hold-1", start_secs: 120, end_secs: 150, activity: "exercise", movement: "L-Sit Hold", load: null, observation_refs: [], rep_events: [{ movement: "L-Sit Hold", unit: "seconds", start_secs: 120, end_secs: 145, complete: true, evidence: "Hold" }] },
    ];
    const records: PerformanceRecord[] = [
      { id: 1, session_id: "s1", interval_id: "set-1", source_version: "a", status: "confirmed", movement: "Power Clean", start_secs: 10, end_secs: 70, reps: 5, load: { value: 135, unit: "lb", basis: "total" }, load_kg: 61.2, measurement: "set", technique: "singles", workout_prescription: "test", reps_per_minute: 5, rate_basis: "normalized_per_minute", median_cycle_seconds: null, performed_at: "2026-10-12T00:00:00Z", confirmed_at: "2026-10-12T00:00:00Z" },
    ];
    const progress = { version: 1 as const, clock: "media" as const, source_version: "a", intervals, unassessed: [] };
    expect(summarizeWorkoutMovements(progress, records)).toEqual([
      { movement: "Power Clean", intervalCount: 2, observedReps: 3, observedSeconds: 0, confirmedCount: 1, confirmedReps: 5, loadLabel: "135 lb" },
      { movement: "L-Sit Hold", intervalCount: 1, observedReps: 0, observedSeconds: 25, confirmedCount: 0, confirmedReps: 0, loadLabel: null },
    ]);
  });

  it("permits a continuous set split across 10-second segment boundaries and incomplete_cycle gaps", () => {
    const seg1: WorkoutProgressInterval = { ...interval, id: "seg-0", start_secs: 0, end_secs: 9 };
    const seg2: WorkoutProgressInterval = { ...interval, id: "seg-1", start_secs: 11, end_secs: 20 };
    const splitProgress = {
      version: 1 as const,
      clock: "media" as const,
      source_version: "split",
      intervals: [seg1, seg2],
      unassessed: [{ start_secs: 9, end_secs: 11, clock: "media", reason: "incomplete_cycle" }],
    };
    expect(getContiguousExerciseSpan(seg1, splitProgress)).toEqual({
      startSecs: 0,
      endSecs: 20,
      intervalCount: 2,
      observedReps: 2,
    });
    const draft = { ...performanceDraft(seg1), end: "20", reps: "10", measurement: "set" as const, reviewed: true };
    expect(confirmedPerformanceInput(seg1, draft, splitProgress)).toEqual({
      interval_id: "seg-0",
      start_secs: 0,
      end_secs: 20,
      reps: 10,
      load: { value: 135, unit: "lb", basis: "total" },
      measurement: "set",
      technique: "singles",
    });
    expect(diagnosePerformanceDraft(seg1, { ...draft, loadMode: "unresolved" }, splitProgress)).toContain("미확인");

    const seg113: WorkoutProgressInterval = {
      ...interval,
      id: "seg-11",
      start_secs: 113,
      end_secs: 120,
      capture_start_secs: 113,
      capture_end_secs: 120,
    };
    const tailProgress = {
      version: 1 as const,
      clock: "media" as const,
      source_version: "tail",
      intervals: [
        seg113,
        { id: "rest-12", start_secs: 125, end_secs: 150, activity: "rest" as const, movement: null, load: null, observation_refs: ["chunk:2"] },
      ],
      unassessed: [{ start_secs: 120, end_secs: 125, clock: "media" as const, reason: "not_analyzed" }],
    };
    const tailDraft = {
      ...performanceDraft(seg113),
      start: "113",
      end: "125",
      reps: "10",
      measurement: "set" as const,
      reviewed: true,
    };
    expect(diagnosePerformanceDraft(seg113, tailDraft, tailProgress)).toBeNull();
    expect(confirmedPerformanceInput(seg113, tailDraft, tailProgress)).toEqual({
      interval_id: "seg-11",
      start_secs: 113,
      end_secs: 125,
      reps: 10,
      load: { value: 135, unit: "lb", basis: "total" },
      measurement: "set",
      technique: "singles",
    });

    const seg167: WorkoutProgressInterval = { ...interval, id: "seg-16", start_secs: 167, end_secs: 169 };
    const seg170: WorkoutProgressInterval = { ...interval, id: "seg-17", start_secs: 170, end_secs: 177 };
    const boundaryCutProgress = {
      version: 1 as const,
      clock: "media" as const,
      source_version: "boundary-cut",
      intervals: [seg167, seg170],
      unassessed: [{ start_secs: 169, end_secs: 170, clock: "media" as const, reason: "not_analyzed" }],
    };
    expect(getContiguousExerciseSpan(seg167, boundaryCutProgress)).toEqual({
      startSecs: 167,
      endSecs: 177,
      intervalCount: 2,
      observedReps: 2,
    });
    expect(getContiguousExerciseSpan(seg170, boundaryCutProgress)).toEqual({
      startSecs: 167,
      endSecs: 177,
      intervalCount: 2,
      observedReps: 2,
    });
  });

  it("supports merging contiguous intervals of different movements and selecting the target movement", () => {
    const squatClean: WorkoutProgressInterval = {
      id: "seg-sc",
      start_secs: 10,
      end_secs: 13,
      activity: "exercise",
      movement: "Squat Clean",
      load: { value: 135, unit: "lb", basis: "total" },
      observation_refs: ["chunk:1"],
      rep_events: [{ movement: "Squat Clean", unit: "reps", start_secs: 10, end_secs: 13, complete: true, evidence: "clean" }],
    };
    const frontSquat: WorkoutProgressInterval = {
      id: "seg-fs",
      start_secs: 13,
      end_secs: 25,
      activity: "exercise",
      movement: "Front Squat",
      load: { value: 135, unit: "lb", basis: "total" },
      observation_refs: ["chunk:1", "chunk:2"],
      rep_events: [
        { movement: "Front Squat", unit: "reps", start_secs: 13, end_secs: 16, complete: true, evidence: "squat 1" },
        { movement: "Front Squat", unit: "reps", start_secs: 16, end_secs: 19, complete: true, evidence: "squat 2" },
        { movement: "Front Squat", unit: "reps", start_secs: 19, end_secs: 22, complete: true, evidence: "squat 3" },
        { movement: "Front Squat", unit: "reps", start_secs: 22, end_secs: 25, complete: true, evidence: "squat 4" },
      ],
    };
    const multiMovementProgress = {
      version: 1 as const,
      clock: "media" as const,
      source_version: "sc-fs",
      intervals: [squatClean, frontSquat],
      unassessed: [],
    };

    const spanFromSc = getContiguousExerciseSpan(squatClean, multiMovementProgress);
    expect(spanFromSc).toEqual({
      startSecs: 10,
      endSecs: 25,
      intervalCount: 2,
      observedReps: 5,
      movementOptions: [
        { movement: "Squat Clean", intervalId: "seg-sc", observedReps: 1 },
        { movement: "Front Squat", intervalId: "seg-fs", observedReps: 5 },
      ],
    });

    // Confirming from Squat Clean card while selecting Front Squat as the target movement
    const mergedAsFrontSquatDraft = {
      ...performanceDraft(squatClean),
      start: "10",
      end: "25",
      reps: "5",
      measurement: "set" as const,
      targetIntervalId: "seg-fs",
      reviewed: true,
    };
    expect(diagnosePerformanceDraft(squatClean, mergedAsFrontSquatDraft, multiMovementProgress)).toBeNull();
    expect(confirmedPerformanceInput(squatClean, mergedAsFrontSquatDraft, multiMovementProgress)).toEqual({
      interval_id: "seg-fs",
      start_secs: 10,
      end_secs: 25,
      reps: 5,
      load: { value: 135, unit: "lb", basis: "total" },
      measurement: "set",
      technique: "singles",
    });

    // Once confirmed as Front Squat, both intervals are covered and top summary consolidates under Front Squat
    const confirmedFsRecord: PerformanceRecord = {
      id: 1,
      session_id: "s1",
      interval_id: "seg-fs",
      source_version: "sc-fs",
      status: "confirmed",
      movement: "Front Squat",
      start_secs: 10,
      end_secs: 25,
      elapsed_seconds: 15,
      reps: 5,
      load: { value: 135, unit: "lb", basis: "total" },
      load_kg: 61.2,
      measurement: "set",
      technique: "singles",
      workout_prescription: "test",
      reps_per_minute: 20,
      rate_basis: "normalized_per_minute",
      median_cycle_seconds: null,
      performed_at: "2026-10-12T00:00:00Z",
      confirmed_at: "2026-10-12T00:00:00Z",
    };
    const summaries = summarizeWorkoutMovements(multiMovementProgress, [confirmedFsRecord]);
    expect(summaries).toEqual([
      {
        movement: "Front Squat",
        intervalCount: 2,
        observedReps: 5,
        observedSeconds: 0,
        confirmedCount: 1,
        confirmedReps: 5,
        loadLabel: "135 lb",
      },
    ]);
  });
});

