/** Evidence and confirmation contracts. Assessments never modify this timeline. */
export interface ProgressLoad {
  value: number;
  unit: "lb" | "kg";
  basis: "total" | "per_implement";
  status?: "pending_confirmation" | "confirmed";
}
export interface ProgressRepEvent {
  movement: string;
  unit: "reps" | "seconds";
  start_secs: number;
  end_secs: number;
  complete: boolean;
  evidence: string;
}
export interface ProgressObservation {
  start_secs: number;
  end_secs: number;
  evidence: string;
  [key: string]: unknown;
}
export interface WorkoutProgressInterval {
  id: string;
  start_secs: number;
  end_secs: number;
  activity: "exercise" | "rest" | "unknown";
  movement?: string | null;
  load: ProgressLoad | null;
  observation_refs: string[];
  rep_events?: ProgressRepEvent[];
  form_observations?: ProgressObservation[];
  rom_observations?: ProgressObservation[];
  pace_observations?: ProgressObservation[];
  capture_start_secs?: number;
  capture_end_secs?: number;
}
export interface WorkoutProgress {
  version: 1;
  clock: "media";
  media_source_id?: string;
  source_version: string;
  intervals: WorkoutProgressInterval[];
  unassessed: { start_secs: number; end_secs: number; clock: string; reason: string }[];
}
export type PerformanceMeasurement = "set" | "window";
export type PerformanceTechnique = "unspecified" | "singles" | "touch_and_go";
export interface ConfirmPerformanceRecord {
  interval_id: string;
  start_secs: number;
  end_secs: number;
  reps: number;
  load: ProgressLoad | null;
  measurement: PerformanceMeasurement;
  technique: PerformanceTechnique;
}
export interface RestPerformanceComparison {
  rest_start_secs: number;
  rest_end_secs: number;
  rest_elapsed_seconds: number | null;
  before_median_cycle_seconds: number | null;
  after_median_cycle_seconds: number | null;
  cycle_change_percent: number | null;
}
export interface PerformanceRecord extends ConfirmPerformanceRecord {
  id: number;
  session_id: string;
  source_version: string;
  status: "confirmed" | "needs_confirmation";
  movement: string;
  load_kg: number | null;
  workout_prescription: string;
  reps_per_minute: number;
  elapsed_seconds?: number;
  rate_basis: "actual_60_second_window" | "normalized_per_minute";
  median_cycle_seconds: number | null;
  performed_at: string | null;
  performed_at_source?: string | null;
  confirmed_at: string;
  rest_comparisons?: RestPerformanceComparison[];
}
export interface WorkoutProgressResponse {
  progress: WorkoutProgress | null;
  records: PerformanceRecord[];
}
export interface ConfirmPerformanceRecordsRequest {
  source_version: string;
  records: ConfirmPerformanceRecord[];
}
export interface PerformanceComparison {
  current_record_id: number;
  baseline_record_id: number;
  kind: "direct" | "reference";
  condition_differences: string[];
  rate_change_percent: number | null;
  heavier_load_kg: number | null;
  heavier_load_baseline_record_id: number | null;
}
export interface PerformanceBenchmarksResponse {
  records: PerformanceRecord[];
  comparisons: PerformanceComparison[];
  best: PerformanceRecord[];
}

export interface PerformanceDraft {
  start: string;
  end: string;
  reps: string;
  loadMode: "unresolved" | "weighted" | "none";
  weight: string;
  unit: "lb" | "kg";
  basis: "total" | "per_implement";
  measurement: PerformanceMeasurement;
  technique: PerformanceTechnique;
  reviewed: boolean;
}
export function progressTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  return `${Math.floor(seconds / 60).toString().padStart(2, "0")}:${Math.floor(seconds % 60).toString().padStart(2, "0")}`;
}
export function observedReps(interval: WorkoutProgressInterval): number | null {
  const events = interval.rep_events ?? [];
  const complete = events.filter(event => event.complete && event.unit === "reps");
  return complete.length ? complete.length : null;
}
export function performanceDraft(interval: WorkoutProgressInterval, record?: PerformanceRecord): PerformanceDraft {
  // Stale confirmations must be reviewed against current evidence, not silently reused.
  const current = record?.status === "confirmed" ? record : undefined;
  const load = current?.load ?? interval.load;
  const reps = current?.reps ?? observedReps(interval);
  return {
    start: String(current?.start_secs ?? interval.start_secs),
    end: String(current?.end_secs ?? interval.end_secs),
    reps: reps == null ? "" : String(reps),
    loadMode: current && current.load == null ? "none" : load ? "weighted" : "unresolved",
    weight: load == null ? "" : String(load.value),
    unit: load?.unit ?? "lb",
    basis: load?.basis ?? "total",
    measurement: current?.measurement ?? "set",
    technique: current?.technique ?? "unspecified",
    reviewed: false,
  };
}
/** A time window may include observed rests, but never a different movement or missing coverage. */
export function performanceSelectionCovered(interval: WorkoutProgressInterval, start: number, end: number, measurement: PerformanceMeasurement, progress?: WorkoutProgress): boolean {
  if (!Number.isFinite(start) || !Number.isFinite(end) || start < interval.start_secs || start >= interval.end_secs || end <= start || interval.activity !== "exercise" || !interval.movement?.trim()) return false;
  const spans = measurement === "window" && progress
    ? progress.intervals.filter(row => row.start_secs < end && row.end_secs > start).sort((a, b) => a.start_secs - b.start_secs)
    : [interval];
  if (spans.length === 0 || spans[0].id !== interval.id) return false;
  let covered = start;
  let candidate: ProgressLoad | null = null;
  const kg = (load: ProgressLoad) => Math.round(load.value * (load.unit === "lb" ? 0.45359237 : 1) * 10) / 10;
  for (let i = 0; i < spans.length; i++) {
    const row = spans[i];
    if (!Number.isFinite(row.start_secs) || !Number.isFinite(row.end_secs) || row.end_secs <= row.start_secs || row.start_secs > covered + 1e-6 || (i > 0 && row.start_secs < covered - 1e-6)) return false;
    if (row.activity !== "rest" && (row.activity !== "exercise" || row.movement?.trim() !== interval.movement.trim())) return false;
    if (row.activity === "exercise" && row.load) {
      if (candidate && (kg(candidate) !== kg(row.load) || candidate.basis !== row.load.basis)) return false;
      candidate = row.load;
    }
    covered = Math.min(end, row.end_secs);
  }
  if (covered < end - 1e-6) return false;
  if (!progress) return true;
  const countOnly = new Set(["no_countable_evidence", "incomplete_cycle", "unsupported_movement_or_unit", "unit_mismatch"]);
  if (progress.unassessed.some(gap => gap.clock === "media" && start < gap.end_secs && end > gap.start_secs && !countOnly.has(gap.reason))) return false;
  if (spans.some(row => (row.capture_start_secs != null) !== (row.capture_end_secs != null))) return false;
  const mapped = spans.map(row => row.capture_start_secs != null && row.capture_end_secs != null);
  if (mapped.some(Boolean) && !mapped.every(Boolean)) return false;
  if (!mapped.some(Boolean)) return !progress.unassessed.some(gap => gap.clock === "capture");
  let previousCaptureEnd: number | undefined;
  for (const row of spans) {
    const captureStart = row.capture_start_secs!, captureEnd = row.capture_end_secs!;
    if (!Number.isFinite(captureStart) || !Number.isFinite(captureEnd) || captureStart < 0 || captureEnd <= captureStart) return false;
    if (Math.abs((captureEnd - captureStart) - (row.end_secs - row.start_secs)) > 0.01 && (start > row.start_secs || end < row.end_secs)) return false;
    if (progress.unassessed.some(gap => gap.clock === "capture" && captureStart < gap.end_secs && captureEnd > gap.start_secs)) return false;
    const spanStart = Math.max(start, row.start_secs), spanEnd = Math.min(end, row.end_secs);
    const mappedStart = spanStart === row.start_secs ? captureStart : captureStart + spanStart - row.start_secs;
    const mappedEnd = spanEnd === row.end_secs ? captureEnd : captureStart + spanEnd - row.start_secs;
    if (!Number.isFinite(mappedStart) || !Number.isFinite(mappedEnd) || mappedEnd <= mappedStart || (previousCaptureEnd != null && Math.abs(mappedStart - previousCaptureEnd) > 0.01)) return false;
    previousCaptureEnd = mappedEnd;
  }
  return true;
}
export function confirmedPerformanceInput(interval: WorkoutProgressInterval, draft: PerformanceDraft, progress?: WorkoutProgress): ConfirmPerformanceRecord | null {
  if (!draft.reviewed || draft.start.trim() === "" || draft.end.trim() === "" || draft.reps.trim() === "") return null;
  const start = Number(draft.start), end = Number(draft.end), reps = Number(draft.reps);
  if (!performanceSelectionCovered(interval, start, end, draft.measurement, progress) || !Number.isSafeInteger(reps) || reps <= 0) return null;
  if (draft.loadMode === "unresolved") return null;
  let load: ProgressLoad | null = null;
  if (draft.loadMode === "weighted") {
    const value = Number(draft.weight);
    if (draft.weight.trim() === "" || !Number.isFinite(value) || value <= 0) return null;
    load = { value, unit: draft.unit, basis: draft.basis };
  }
  return { interval_id: interval.id, start_secs: start, end_secs: end, reps, load, measurement: draft.measurement, technique: draft.technique };
}
