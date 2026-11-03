import { buildWorkoutShareText, type WorkoutShareContext } from "./workoutShare";
import type { PerformanceRecord } from "./workoutProgress";

const options = { includeAIObservations: true, includeSensorSummary: true };
function record(id: number, sessionID = "WOD-current"): PerformanceRecord {
  return {
    id, session_id: sessionID, interval_id: `interval-${id}`, source_version: "source-1", status: "confirmed",
    movement: "Deadlift", outcome: "completed", start_secs: 10, end_secs: 40, elapsed_seconds: 30,
    reps: 10, load: { value: 60, unit: "kg", basis: "total", status: "confirmed" }, load_kg: 60,
    measurement: "set", technique: "singles", workout_prescription: "10 deadlifts at 60 kg",
    reps_per_minute: 20, rate_basis: "normalized_per_minute", median_cycle_seconds: 3,
    performed_at: "2026-11-03T00:00:00Z", performed_at_source: "session_ulid", confirmed_at: "2026-11-03T01:00:00Z",
  };
}
function snapshot(): WorkoutShareContext {
  return {
    schema_version: 1, generated_at: "2026-11-03T01:00:00Z", timezone: "Asia/Seoul",
    session: {
      session_id: "WOD-current", workout_type: "wod", performed_at: "2026-11-03T00:00:00Z",
      performed_at_source: "session_ulid", workout_prescription: "10 deadlifts at 60 kg", source_version: "source-1",
    },
    confirmed_records: [record(2)],
    comparisons: [{
      current_record_id: 2, baseline_record_id: 1, kind: "direct", condition_differences: [],
      rate_change_percent: 12.5, heavier_load_kg: null, heavier_load_baseline_record_id: null,
    }],
    baseline_records: [{ ...record(1, "WOD-earlier"), performed_at: "2026-11-02T00:00:00Z" }],
    observations: [{
      id: "ai-1", start_secs: 10, end_secs: 40, movement: "Deadlift", activity: "exercise", load: null, observed_reps: null,
      form_observations: [{ start_secs: 12, end_secs: 15, evidence: "FORM-EVIDENCE-ONLY" }],
      rom_observations: [], pace_observations: [],
    }],
    unassessed: [{ start_secs: 50, end_secs: 60, clock: "media", reason: "not_analyzed" }],
    sensor_summary: {
      status: "completed", quality_status: "limited", avg_bpm: 143, peak_bpm: 170, coverage: 0.75,
      valid_seconds: 45, unknown_seconds: 10, excluded_seconds: 5, calculation_version: 3,
    },
    effort: 8, warnings: [],
  };
}

it("exports evidence, baseline identity and conditions without merging AI counts into confirmed facts", () => {
  const text = buildWorkoutShareText(snapshot(), options, "en");
  expect(text).toContain("User-confirmed records");
  expect(text).toContain("#2 Deadlift");
  expect(text).toContain("60 kg (total load)");
  expect(text).toContain("Confirmed pace change: 12.5%");
  expect(text).toContain("WOD-earlier");
  expect(text).toContain("AI observations — unconfirmed");
  expect(text).toContain("AI-observed repetitions (unconfirmed): Unknown");
  expect(text).toContain("FORM-EVIDENCE-ONLY");
  expect(text).toContain("Measurement coverage: 75%");
  expect(text).toContain("Measurement quality: Limited");
  expect(text).toContain("Unassessed ranges are not rest");
});

it("never emits improvement percentages for reference-only comparisons", () => {
  const context = snapshot();
  context.comparisons[0] = {
    ...context.comparisons[0], kind: "reference", condition_differences: ["load", "unknown_technique"],
  };
  const text = buildWorkoutShareText(context, options, "ko");
  expect(text).toContain("참고 비교");
  expect(text).toContain("중량, 수행 방식 미확인");
  expect(text).not.toContain("12.5%");
  expect(text).toContain("WOD-earlier");
});

it("removes both excluded sections from the actual exported text while keeping evidence limitations", () => {
  const text = buildWorkoutShareText(snapshot(), { includeAIObservations: false, includeSensorSummary: false }, "en");
  expect(text).not.toContain("FORM-EVIDENCE-ONLY");
  expect(text).not.toContain("AI observations — unconfirmed");
  expect(text).not.toContain("143");
  expect(text).not.toContain("User-reported effort");
  expect(text).toContain("#2 Deadlift");
  expect(text).toContain("not_analyzed");
});

it("preserves unknowns, incomplete/rest outcomes and lower-load baseline dependencies", () => {
  const context = snapshot();
  context.confirmed_records.push({ ...record(3), outcome: "incomplete", reps: null, reps_per_minute: null });
  context.confirmed_records.push({ ...record(4), outcome: "rest", reps: null, load: null, reps_per_minute: null });
  context.baseline_records.push(record(5, "WOD-lower-load"));
  context.comparisons[0].heavier_load_kg = 5;
  context.comparisons[0].heavier_load_baseline_record_id = 5;
  context.session.performed_at = null;
  context.warnings = ["workout_time_unknown"];
  const text = buildWorkoutShareText(context, options, "en");
  expect(text).toContain("Workout time: Unknown");
  expect(text).toContain("Outcome: Incomplete | Repetitions: Unknown");
  expect(text).toContain("Outcome: Rest | Repetitions: Unknown");
  expect(text).toContain("Increase in performed load at equal reps: 5 kg (#5)");
  expect(text).toContain("WOD-lower-load");
  expect(text).not.toContain("Repetitions: 0");
});

it("uses allowlisted fields so future internal fields never leak into the report", () => {
  const context = snapshot();
  Object.assign(context, { jwt: "SECRET-JWT", device_name: "SECRET-DEVICE", video_url: "https://private.example/SECRET-URL" });
  Object.assign(context.sensor_summary!, { device_name: "SECRET-SENSOR" });
  Object.assign(context.confirmed_records[0], { original_candidate: "SECRET-RAW" });
  const text = buildWorkoutShareText(context, options, "en");
  expect(text).not.toContain("SECRET-");
  expect(text).toContain("#2 Deadlift");
});

it("keeps video offsets distinct from verified elapsed duration and normalized pace", () => {
  const context = snapshot();
  context.confirmed_records[0] = { ...context.confirmed_records[0], start_secs: 0.5, end_secs: 59.9999, elapsed_seconds: 75 };
  const text = buildWorkoutShareText(context, options, "en");
  expect(text).toContain("[00:00.5–01:00]");
  expect(text).toContain("Elapsed time: 75 seconds");
  expect(text).toContain("Pace basis: Normalized per minute");
  expect(text).toContain("ranges refer to video playback positions");
});

it("uses the requested language, local occurrence time and explicit missing-data labels", () => {
  const context = snapshot();
  context.confirmed_records = [];
  context.comparisons = [];
  context.observations = [];
  context.sensor_summary = null;
  context.effort = null;
  const korean = buildWorkoutShareText(context, options, "ko");
  const english = buildWorkoutShareText(context, options, "en");
  expect(korean).toContain("운동 비교 자료");
  expect(korean).toContain("09:00:00");
  expect(korean).toContain("사용자 확정 기록이 없어");
  expect(english).toContain("Workout comparison context");
  expect(english).toContain("No user-confirmed records");
  expect(english).toContain("No heart-rate summary available");
  expect(english).not.toContain("undefined");
});
