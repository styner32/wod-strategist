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

export function inferLoadBasis(movement?: string | null, contextText?: string): "total" | "per_implement" {
  const combined = `${movement ?? ""} ${contextText ?? ""}`.toLowerCase();
  if (
    combined.includes("dumbbell") ||
    combined.includes("kettlebell") ||
    /\bdb\b/.test(combined) ||
    /\bkb\b/.test(combined) ||
    combined.includes("덤벨") ||
    combined.includes("케틀벨")
  ) {
    return "per_implement";
  }
  return "total";
}

const BODYWEIGHT_MOVEMENT_PATTERNS = [
  /pull-?up/i,
  /push-?up/i,
  /burpee/i,
  /air\s*squat/i,
  /sit-?up/i,
  /toes-?to-?bar/i,
  /\bt2b\b/i,
  /handstand/i,
  /\bhspu\b/i,
  /muscle-?up/i,
  /double-?under/i,
  /single-?under/i,
  /box\s*jump/i,
  /v-?up/i,
  /ring\s*dip/i,
  /rope\s*climb/i,
];

export function isLikelyBodyweightMovement(movement?: string | null): boolean {
  if (!movement) return false;
  const trimmed = movement.trim();
  if (/weighted|vest|dumbbell|kettlebell|barbell|\bdb\b|\bkb\b/i.test(trimmed)) {
    return false;
  }
  return BODYWEIGHT_MOVEMENT_PATTERNS.some(pattern => pattern.test(trimmed));
}

export function extractWodLoads(wodDescription?: string | null, movement?: string | null): ProgressLoad[] {
  if (!wodDescription || !wodDescription.trim()) return [];
  if (isLikelyBodyweightMovement(movement)) return [];

  const parseSegmentLoads = (segment: string): ProgressLoad[] => {
    const results: ProgressLoad[] = [];
    const seen = new Set<string>();
    const addLoad = (val: number, rawUnit: string, context: string) => {
      if (!Number.isFinite(val) || val <= 0) return;
      const unit: "lb" | "kg" = rawUnit.toLowerCase().startsWith("kg") ? "kg" : "lb";
      const basis = inferLoadBasis(movement, context);
      const key = `${val}:${unit}:${basis}`;
      if (!seen.has(key)) {
        seen.add(key);
        results.push({ value: val, unit, basis, status: "pending_confirmation" });
      }
    };

    const slashRegex = /(\d+(?:\.\d+)?)\s*\/\s*(\d+(?:\.\d+)?)\s*(lbs?|kg)\b/gi;
    let match: RegExpExecArray | null;
    while ((match = slashRegex.exec(segment)) !== null) {
      addLoad(Number(match[1]), match[3], segment);
      addLoad(Number(match[2]), match[3], segment);
    }

    const cleaned = segment.replace(slashRegex, " ");
    const singleRegex = /(\d+(?:\.\d+)?)\s*(lbs?|kg)\b/gi;
    while ((match = singleRegex.exec(cleaned)) !== null) {
      addLoad(Number(match[1]), match[2], segment);
    }
    return results;
  };

  if (movement && movement.trim()) {
    const words = movement
      .toLowerCase()
      .split(/[\s\-_/]+/)
      .filter(w => w.length >= 3 && w !== "barbell" && w !== "dumbbell" && w !== "kettlebell");
    if (words.length > 0) {
      const lines = wodDescription.split(/[\n,;]+/);
      const matchedLines = lines.filter(line => {
        const lower = line.toLowerCase();
        return words.some(w => lower.includes(w));
      });
      if (matchedLines.length > 0) {
        const lineLoads = parseSegmentLoads(matchedLines.join(" "));
        if (lineLoads.length > 0) return lineLoads;
      }
    }
  }

  return parseSegmentLoads(wodDescription);
}

export interface WorkoutMovementSummary {
  movement: string;
  intervalCount: number;
  observedReps: number;
  observedSeconds: number;
  confirmedCount: number;
  confirmedReps: number;
  loadLabel: string | null;
}

export function summarizeWorkoutMovements(
  progress?: WorkoutProgress | null,
  records: PerformanceRecord[] = [],
): WorkoutMovementSummary[] {
  if (!progress) return [];
  const map = new Map<string, WorkoutMovementSummary>();

  for (const interval of progress.intervals) {
    if (interval.activity !== "exercise" || !interval.movement?.trim()) continue;
    const name = interval.movement.trim();
    const existing = map.get(name) ?? {
      movement: name,
      intervalCount: 0,
      observedReps: 0,
      observedSeconds: 0,
      confirmedCount: 0,
      confirmedReps: 0,
      loadLabel: null,
    };
    existing.intervalCount += 1;
    for (const ev of interval.rep_events ?? []) {
      if (!ev.complete) continue;
      if (ev.unit === "reps") {
        existing.observedReps += 1;
      } else if (ev.unit === "seconds") {
        existing.observedSeconds += Math.max(0, ev.end_secs - ev.start_secs);
      }
    }
    if (!existing.loadLabel && interval.load) {
      existing.loadLabel = `${interval.load.value} ${interval.load.unit}`;
    }
    map.set(name, existing);
  }

  for (const rec of records) {
    if (rec.status !== "confirmed" || !rec.movement?.trim()) continue;
    const name = rec.movement.trim();
    const existing = map.get(name);
    if (!existing) continue;
    existing.confirmedCount += 1;
    existing.confirmedReps += rec.reps;
    if (rec.load) {
      existing.loadLabel = `${rec.load.value} ${rec.load.unit}`;
    }
  }

  return [...map.values()];
}

export function findCoveringConfirmedRecord(
  interval: WorkoutProgressInterval,
  records: PerformanceRecord[] = [],
): PerformanceRecord | undefined {
  const direct = records.find(r => r.interval_id === interval.id);
  if (direct?.status === "confirmed") return direct;
  if (interval.activity !== "exercise" || !interval.movement?.trim()) return undefined;
  const movement = interval.movement.trim().toLowerCase();
  return records.find(
    r =>
      r.status === "confirmed" &&
      r.movement?.trim().toLowerCase() === movement &&
      r.start_secs <= interval.start_secs + 1e-6 &&
      r.end_secs >= interval.end_secs - 1e-6,
  );
}

export function performanceDraft(
  interval: WorkoutProgressInterval,
  record?: PerformanceRecord,
  fallbackLoad?: ProgressLoad | null,
): PerformanceDraft {
  // Stale confirmations must be reviewed against current evidence, not silently reused.
  const current = record?.status === "confirmed" ? record : undefined;
  const load = current?.load ?? interval.load ?? fallbackLoad ?? null;
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
    technique: current?.technique && current.technique !== "unspecified" ? current.technique : "singles",
    reviewed: false,
  };
}
const COUNT_ONLY_REASONS = new Set([
  "no_countable_evidence",
  "incomplete_cycle",
  "unsupported_movement_or_unit",
  "unit_mismatch",
]);

function coveredByCountOnlyGaps(
  unassessed: WorkoutProgress["unassessed"],
  from: number,
  to: number,
): boolean {
  if (to <= from + 1e-6) return true;
  const gaps = unassessed
    .filter(g => g.clock === "media" && COUNT_ONLY_REASONS.has(g.reason) && g.end_secs > from && g.start_secs < to)
    .sort((a, b) => a.start_secs - b.start_secs);
  let cursor = from;
  for (const gap of gaps) {
    if (gap.start_secs > cursor + 1e-6) return false;
    if (gap.end_secs > cursor) cursor = gap.end_secs;
    if (cursor >= to - 1e-6) return true;
  }
  return cursor >= to - 1e-6;
}

export interface ContiguousExerciseSpan {
  startSecs: number;
  endSecs: number;
  intervalCount: number;
  observedReps: number | null;
}

/**
 * Finds contiguous same-movement exercise intervals (including across 10s segment boundaries
 * or incomplete_cycle boundary gaps) so users can merge a split set with one click.
 */
export function getContiguousExerciseSpan(
  interval: WorkoutProgressInterval,
  progress?: WorkoutProgress | null,
): ContiguousExerciseSpan {
  const baseReps = observedReps(interval);
  if (!progress || interval.activity !== "exercise" || !interval.movement?.trim()) {
    return {
      startSecs: interval.start_secs,
      endSecs: interval.end_secs,
      intervalCount: 1,
      observedReps: baseReps,
    };
  }
  const sorted = [...progress.intervals].sort((a, b) => a.start_secs - b.start_secs);
  const idx = sorted.findIndex(row => row.id === interval.id);
  if (idx < 0) {
    return {
      startSecs: interval.start_secs,
      endSecs: interval.end_secs,
      intervalCount: 1,
      observedReps: baseReps,
    };
  }
  const movement = interval.movement.trim();
  const chain: WorkoutProgressInterval[] = [sorted[idx]];
  let endSecs = sorted[idx].end_secs;
  for (let i = idx + 1; i < sorted.length; i++) {
    const next = sorted[i];
    if (next.activity !== "exercise" || next.movement?.trim() !== movement) break;
    if (next.start_secs > endSecs + 1e-6 && !coveredByCountOnlyGaps(progress.unassessed, endSecs, next.start_secs)) {
      break;
    }
    if (!performanceSelectionCovered(interval, interval.start_secs, next.end_secs, "set", progress)) {
      break;
    }
    chain.push(next);
    endSecs = next.end_secs;
  }
  if (endSecs === chain[chain.length - 1].end_secs) {
    const trailingGaps = progress.unassessed
      .filter(g => g.clock === "media" && COUNT_ONLY_REASONS.has(g.reason) && Math.abs(g.start_secs - endSecs) <= 1e-6)
      .sort((a, b) => a.start_secs - b.start_secs);
    for (const g of trailingGaps) {
      if (performanceSelectionCovered(interval, interval.start_secs, g.end_secs, "set", progress)) {
        endSecs = g.end_secs;
      }
    }
  }
  let totalReps = 0;
  let hasReps = false;
  for (const item of chain) {
    const r = observedReps(item);
    if (r != null) {
      totalReps += r;
      hasReps = true;
    }
  }
  return {
    startSecs: interval.start_secs,
    endSecs,
    intervalCount: chain.length,
    observedReps: hasReps ? totalReps : null,
  };
}

/** A time window may include observed rests; a set may span contiguous same-movement exercise intervals across 10s segment boundaries. */
export function performanceSelectionCovered(interval: WorkoutProgressInterval, start: number, end: number, measurement: PerformanceMeasurement, progress?: WorkoutProgress): boolean {
  if (!Number.isFinite(start) || !Number.isFinite(end) || start < 0 || start >= interval.end_secs || end <= interval.start_secs || end <= start || interval.activity !== "exercise" || !interval.movement?.trim()) return false;
  if (!progress && (start < interval.start_secs || end > interval.end_secs)) return false;
  const kg = (load: ProgressLoad) => Math.round(load.value * (load.unit === "lb" ? 0.45359237 : 1) * 10) / 10;
  if (measurement === "set") {
    if (!progress) return true;
    if (
      progress.unassessed.some(
        gap =>
          gap.clock === "media" &&
          gap.reason === "unverified_capture_mapping" &&
          start < gap.end_secs &&
          end > gap.start_secs,
      )
    ) {
      return false;
    }
    const movement = interval.movement.trim();
    const exSpans = progress.intervals
      .filter(
        row =>
          row.activity === "exercise" &&
          row.movement?.trim() === movement &&
          row.start_secs < end &&
          row.end_secs > start,
      )
      .sort((a, b) => a.start_secs - b.start_secs);
    if (exSpans.length === 0 || !exSpans.some(row => row.id === interval.id)) return false;
    let candidate: ProgressLoad | null = null;
    for (const row of exSpans) {
      if (row.load) {
        if (candidate && (kg(candidate) !== kg(row.load) || candidate.basis !== row.load.basis)) return false;
        candidate = row.load;
      }
    }
    if (exSpans.length >= 2) {
      const firstExEnd = exSpans[0].end_secs;
      const lastExStart = exSpans[exSpans.length - 1].start_secs;
      if (
        progress.intervals.some(
          row =>
            row.activity === "rest" &&
            row.end_secs > firstExEnd - 1e-6 &&
            row.start_secs < lastExStart + 1e-6,
        )
      ) {
        return false;
      }
    }
    if ((interval.capture_start_secs != null) !== (interval.capture_end_secs != null)) return false;
    if (interval.capture_start_secs == null || interval.capture_end_secs == null) {
      return true;
    }
    const captureStart = interval.capture_start_secs;
    const captureEnd = interval.capture_end_secs;
    if (!Number.isFinite(captureStart) || !Number.isFinite(captureEnd) || captureStart < 0 || captureEnd <= captureStart) {
      return false;
    }
    const mappedStart = start === interval.start_secs ? captureStart : captureStart + (start - interval.start_secs);
    const mappedEnd = end === interval.end_secs ? captureEnd : captureStart + (end - interval.start_secs);
    if (!Number.isFinite(mappedStart) || !Number.isFinite(mappedEnd) || mappedEnd <= mappedStart) return false;
    return true;
  }
  const spans = progress && (measurement === "window" || start < interval.start_secs || end > interval.end_secs)
    ? progress.intervals.filter(row => row.start_secs < end && row.end_secs > start).sort((a, b) => a.start_secs - b.start_secs)
    : [interval];
  if (spans.length === 0 || !spans.some(row => row.id === interval.id)) return false;
  let covered = start;
  let candidate: ProgressLoad | null = null;
  for (let i = 0; i < spans.length; i++) {
    const row = spans[i];
    if (!Number.isFinite(row.start_secs) || !Number.isFinite(row.end_secs) || row.end_secs <= row.start_secs || (i > 0 && row.start_secs < covered - 1e-6)) return false;
    if (row.start_secs > covered + 1e-6) {
      const canBridge =
        progress != null &&
        row.capture_start_secs == null &&
        row.capture_end_secs == null &&
        (i === 0 || (spans[i - 1].capture_start_secs == null && spans[i - 1].capture_end_secs == null)) &&
        coveredByCountOnlyGaps(progress.unassessed, covered, row.start_secs);
      if (!canBridge) return false;
      covered = row.start_secs;
    }
    if (row.activity !== "rest" && (row.activity !== "exercise" || row.movement?.trim() !== interval.movement.trim())) return false;
    if (row.activity === "exercise" && row.load) {
      if (candidate && (kg(candidate) !== kg(row.load) || candidate.basis !== row.load.basis)) return false;
      candidate = row.load;
    }
    covered = Math.min(end, row.end_secs);
  }
  if (covered < end - 1e-6) {
    const last = spans[spans.length - 1];
    const canBridgeEnd =
      progress != null &&
      last.capture_start_secs == null &&
      last.capture_end_secs == null &&
      coveredByCountOnlyGaps(progress.unassessed, covered, end);
    if (!canBridgeEnd) return false;
    covered = end;
  }
  if (!progress) return true;
  if (progress.unassessed.some(gap => gap.clock === "media" && start < gap.end_secs && end > gap.start_secs && !COUNT_ONLY_REASONS.has(gap.reason))) return false;
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

export function diagnosePerformanceDraft(
  interval: WorkoutProgressInterval,
  draft: PerformanceDraft,
  progress?: WorkoutProgress,
): string | null {
  if (draft.start.trim() === "" || draft.end.trim() === "") {
    return "시작(초)과 종료(초)를 입력해주세요.";
  }
  const start = Number(draft.start), end = Number(draft.end);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
    return "종료(초)는 시작(초)보다 커야 합니다.";
  }
  if (start < 0 || start >= interval.end_secs || end <= interval.start_secs) {
    return `시작~종료 범위가 현재 선택한 운동 구간(${progressTime(interval.start_secs)}–${progressTime(interval.end_secs)})과 겹쳐야 합니다.`;
  }
  const reps = Number(draft.reps);
  if (draft.reps.trim() === "" || !Number.isSafeInteger(reps) || reps <= 0) {
    return "완료 반복수에 1 이상의 정수를 입력해주세요.";
  }
  if (draft.loadMode === "unresolved") {
    return "수행 중량 구분이 '미확인' 상태입니다. '맨몸 수행' 또는 '중량 기구 사용'을 선택해주세요.";
  }
  if (draft.loadMode === "weighted") {
    const val = Number(draft.weight);
    if (draft.weight.trim() === "" || !Number.isFinite(val) || val <= 0) {
      return "중량 기구 사용 시 0보다 큰 중량(무게)을 입력해주세요.";
    }
  }
  if (!performanceSelectionCovered(interval, start, end, draft.measurement, progress)) {
    if (
      draft.measurement === "set" &&
      progress &&
      performanceSelectionCovered(interval, start, end, "window", progress)
    ) {
      return "선택한 범위에 휴식(rest) 구간이 포함되어 있습니다. 휴식을 포함해 여러 세트를 묶으려면 측정 방식을 '시간 구간'으로 변경해주세요.";
    }
    return "선택한 시작~종료 범위에 미관찰/녹화 공백이나 다른 동작 구간이 포함되어 있습니다.";
  }
  return null;
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

