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
export interface WorkoutProgressGap {
  start_secs: number;
  end_secs: number;
  clock: string;
  reason: string;
  movement?: string;
}
export interface WorkoutProgress {
  version: 1;
  clock: "media";
  media_source_id?: string;
  source_version: string;
  intervals: WorkoutProgressInterval[];
  unassessed: WorkoutProgressGap[];
}
export type PerformanceMeasurement = "set" | "window";
export type PerformanceTechnique = "unspecified" | "singles" | "touch_and_go";
export interface ConfirmPerformanceRecord {
  interval_id: string;
  movement?: string;
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
  movement?: string;
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
  targetIntervalId?: string;
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
export function incompleteRepCount(interval: WorkoutProgressInterval): number {
  const events = interval.rep_events ?? [];
  return events.filter(event => !event.complete && event.unit === "reps").length;
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

const WEIGHTED_MODIFIER_PATTERN =
  /weighted|vest|dumbbell|kettlebell|barbell|\bdb\b|\bkb\b|goblet|wall\s*ball|wallball|medicine\s*ball|med\s*ball|sandbag|plate|sled|yoke|덤벨|케틀벨|바벨|중량|고블릿|월볼|메디신볼|샌드백|플레이트/i;

const BODYWEIGHT_MOVEMENT_PATTERNS = [
  /pull-?up/i,
  /chin-?up/i,
  /chest[\s-]*to[\s-]*bar/i,
  /\bc2b\b/i,
  /push-?up/i,
  /burpee/i,
  /\bbbjo\b/i,
  /air\s*squat/i,
  /pistol/i,
  /single[\s-]*leg\s*squat/i,
  /sit-?up/i,
  /toes[\s-]*to[\s-]*bar/i,
  /\bt2b\b/i,
  /knees[\s-]*to[\s-]*(?:elbow|chest)/i,
  /\bk2e\b/i,
  /handstand/i,
  /\bhspu\b/i,
  /\bhsw\b/i,
  /wall\s*walk/i,
  /muscle-?up/i,
  /\bbmu\b/i,
  /\brmu\b/i,
  /double-?under/i,
  /single-?under/i,
  /\bdus?\b/i,
  /\bsus?\b/i,
  /box\s*jump/i,
  /box\s*step/i,
  /\bbjo?\b/i,
  /broad\s*jump/i,
  /v-?up/i,
  /l-?sit/i,
  /hollow\s*(?:rock|hold)/i,
  /arch\s*hold/i,
  /plank/i,
  /ring\s*dip/i,
  /\bdips?\b/i,
  /ring\s*row/i,
  /rope\s*climb/i,
  /lunge/i,
  /mountain\s*climber/i,
  /jumping\s*jack/i,
  /bear\s*crawl/i,
  /\brow(?:ing|er)?\b/i,
  /echo\s*bike/i,
  /assault\s*bike/i,
  /air\s*bike/i,
  /airdyne/i,
  /bike\s*erg/i,
  /ski\s*erg/i,
  /\brun(?:ning)?\b/i,
  /\bsprint(?:ing)?\b/i,
  /\bjog(?:ging)?\b/i,
  /에어\s*스쿼트/,
  /맨몸\s*스쿼트/,
  /버피/,
  /풀업/,
  /푸쉬업|푸시업/,
  /싯업/,
  /토투바/,
  /핸드스탠드/,
  /머슬업/,
  /더블\s*언더/,
  /박스\s*점프/,
  /런지/,
  /로잉/,
  /달리기/,
];

const WEIGHTED_MOVEMENT_PATTERNS = [
  WEIGHTED_MODIFIER_PATTERN,
  /snatch/i,
  /clean/i,
  /jerk/i,
  /cluster/i,
  /deadlift/i,
  /\bdl\b/i,
  /\bsdhp\b/i,
  /\brdl\b/i,
  /thruster/i,
  /press/i,
  /(?:back|front|overhead|dumbbell|db|kettlebell|kb|goblet|zercher|barbell|split|weighted)\s*squat/i,
  /\bohs\b/i,
  /\bswing/i,
  /manmaker/i,
  /farmer'?s?\s*(?:carry|walk)/i,
  /turkish\s*get-?up/i,
  /\btgu\b/i,
  /(?:overhead|front\s*rack)\s*lunge/i,
  /(?:pendlay|bent[\s-]*over|renegade)\s*row/i,
  /스내치/,
  /클린/,
  /저크/,
  /클러스터/,
  /데드리프트/,
  /쓰러스터|스러스터/,
  /프레스/,
  /스윙/,
  /맨메이커/,
  /파머스/,
];

export function isLikelyBodyweightMovement(movement?: string | null): boolean {
  if (!movement) return false;
  const trimmed = movement.trim();
  if (
    WEIGHTED_MODIFIER_PATTERN.test(trimmed) ||
    /(?:overhead|front\s*rack)\s*lunge/i.test(trimmed) ||
    /(?:pendlay|bent[\s-]*over|renegade)\s*row/i.test(trimmed)
  ) {
    return false;
  }
  return BODYWEIGHT_MOVEMENT_PATTERNS.some(pattern => pattern.test(trimmed));
}

export function isLikelyWeightedMovement(movement?: string | null): boolean {
  if (!movement) return false;
  const trimmed = movement.trim();
  if (isLikelyBodyweightMovement(trimmed)) return false;
  return WEIGHTED_MOVEMENT_PATTERNS.some(pattern => pattern.test(trimmed));
}

export function inferDefaultLoadMode(movement?: string | null): PerformanceDraft["loadMode"] {
  if (isLikelyBodyweightMovement(movement)) return "none";
  if (isLikelyWeightedMovement(movement)) return "weighted";
  return "unresolved";
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

function recordSupersedes(candidate: PerformanceRecord, target: PerformanceRecord, candidateIdx: number, targetIdx: number): boolean {
  if (candidateIdx === targetIdx) return false;
  if ((candidate.session_id ?? "") !== (target.session_id ?? "")) return false;
  if (!(candidate.start_secs < target.end_secs - 1e-6 && candidate.end_secs > target.start_secs + 1e-6)) {
    return false;
  }
  if (candidate.status === "confirmed" && target.status !== "confirmed") return true;
  if (candidate.status !== "confirmed" && target.status === "confirmed") return false;
  const candidateDur = candidate.end_secs - candidate.start_secs;
  const targetDur = target.end_secs - target.start_secs;
  const candidateContainsTarget =
    candidate.start_secs <= target.start_secs + 1e-6 &&
    candidate.end_secs >= target.end_secs - 1e-6 &&
    candidateDur > targetDur + 1e-6;
  const targetContainsCandidate =
    target.start_secs <= candidate.start_secs + 1e-6 &&
    target.end_secs >= candidate.end_secs - 1e-6 &&
    targetDur > candidateDur + 1e-6;
  if (candidateContainsTarget && !targetContainsCandidate) return true;
  if (targetContainsCandidate && !candidateContainsTarget) return false;
  const candidateTime = candidate.confirmed_at ? Date.parse(candidate.confirmed_at) : NaN;
  const targetTime = target.confirmed_at ? Date.parse(target.confirmed_at) : NaN;
  if (Number.isFinite(candidateTime) && Number.isFinite(targetTime) && candidateTime !== targetTime) {
    return candidateTime > targetTime;
  }
  if (candidate.id !== target.id) {
    return candidate.id > target.id;
  }
  return candidateIdx > targetIdx;
}

export function activePerformanceRecords(records: PerformanceRecord[] = []): PerformanceRecord[] {
  return records.filter(
    (target, targetIdx) =>
      !records.some((candidate, candidateIdx) => recordSupersedes(candidate, target, candidateIdx, targetIdx)),
  );
}

export function findCoveringConfirmedRecord(
  interval: WorkoutProgressInterval,
  records: PerformanceRecord[] = [],
): PerformanceRecord | undefined {
  const active = activePerformanceRecords(records);
  const direct = active.find(r => r.interval_id === interval.id);
  if (direct?.status === "confirmed") return direct;
  if (interval.activity !== "exercise" && !interval.id.startsWith("unassessed-")) return undefined;
  if (interval.activity === "exercise" && !interval.movement?.trim() && !interval.id.startsWith("unassessed-")) return undefined;
  return active.find(
    r =>
      r.status === "confirmed" &&
      r.start_secs <= interval.start_secs + 1e-6 &&
      r.end_secs >= interval.end_secs - 1e-6,
  );
}

export function summarizeWorkoutMovements(
  progress?: WorkoutProgress | null,
  records: PerformanceRecord[] = [],
): WorkoutMovementSummary[] {
  if (!progress) return [];
  const active = activePerformanceRecords(records);
  const map = new Map<string, WorkoutMovementSummary>();

  for (const interval of progress.intervals) {
    if (interval.activity !== "exercise" || !interval.movement?.trim()) continue;
    const covering = findCoveringConfirmedRecord(interval, active);
    const name = covering?.movement?.trim() || interval.movement.trim();
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

  for (const rec of active) {
    if (rec.status !== "confirmed" || !rec.movement?.trim()) continue;
    const name = rec.movement.trim();
    const coversObservedExercise = progress.intervals.some(
      interval =>
        interval.activity === "exercise" &&
        Boolean(interval.movement?.trim()) &&
        (interval.id === rec.interval_id ||
          (rec.start_secs <= interval.start_secs + 1e-6 && rec.end_secs >= interval.end_secs - 1e-6)),
    );
    const existing = map.get(name) ?? {
      movement: name,
      intervalCount: 0,
      observedReps: 0,
      observedSeconds: 0,
      confirmedCount: 0,
      confirmedReps: 0,
      loadLabel: null,
    };
    if (!coversObservedExercise) {
      existing.intervalCount += 1;
    }
    existing.confirmedCount += 1;
    existing.confirmedReps += rec.reps;
    if (rec.load) {
      existing.loadLabel = `${rec.load.value} ${rec.load.unit}`;
    }
    map.set(name, existing);
  }

  return [...map.values()];
}

export interface MovementDraftDefaults {
  loadMode?: PerformanceDraft["loadMode"];
  weight?: string;
  unit?: "lb" | "kg";
  basis?: "total" | "per_implement";
  technique?: PerformanceTechnique;
}

export function rememberedMovementDefaults(
  movement: string | null | undefined,
  records: PerformanceRecord[] = [],
  rememberedByMovement: Record<string, MovementDraftDefaults> = {},
): MovementDraftDefaults | undefined {
  const key = movement?.trim().toLowerCase();
  if (!key) return undefined;
  const inMemory = rememberedByMovement[key];
  const active = activePerformanceRecords(records);
  const confirmedForMovement = [...active]
    .reverse()
    .find(
      r =>
        r.status === "confirmed" &&
        r.movement?.trim().toLowerCase() === key &&
        (r.reps > 0 || r.load != null),
    );
  if (!inMemory && !confirmedForMovement) return undefined;
  const fromRecord: MovementDraftDefaults | undefined = confirmedForMovement
    ? {
        loadMode: confirmedForMovement.load ? "weighted" : "none",
        weight: confirmedForMovement.load ? String(confirmedForMovement.load.value) : "",
        unit: confirmedForMovement.load?.unit ?? "lb",
        basis: confirmedForMovement.load?.basis ?? inferLoadBasis(movement),
        technique:
          confirmedForMovement.technique && confirmedForMovement.technique !== "unspecified"
            ? confirmedForMovement.technique
            : "singles",
      }
    : undefined;
  return {
    ...fromRecord,
    ...inMemory,
  };
}

export function performanceDraft(
  interval: WorkoutProgressInterval,
  record?: PerformanceRecord,
  fallbackLoad?: ProgressLoad | null,
  movementDefaults?: MovementDraftDefaults,
): PerformanceDraft {
  // Stale confirmations must be reviewed against current evidence, not silently reused.
  const current = record?.status === "confirmed" ? record : undefined;
  const effectiveMovement = current?.movement?.trim() || interval.movement?.trim() || "";
  const load = current?.load ?? interval.load ?? fallbackLoad ?? null;
  const reps = current?.reps ?? observedReps(interval);
  const rememberedMode =
    movementDefaults?.loadMode && movementDefaults.loadMode !== "unresolved"
      ? movementDefaults.loadMode
      : undefined;
  const loadMode: PerformanceDraft["loadMode"] =
    current && current.load == null
      ? "none"
      : load
        ? "weighted"
        : rememberedMode ?? inferDefaultLoadMode(effectiveMovement);
  const weight =
    load != null
      ? String(load.value)
      : loadMode === "weighted" && movementDefaults?.weight
        ? movementDefaults.weight
        : "";
  const unit = load?.unit ?? movementDefaults?.unit ?? "lb";
  const basis = load?.basis ?? movementDefaults?.basis ?? inferLoadBasis(effectiveMovement);
  const technique: PerformanceTechnique =
    current?.technique && current.technique !== "unspecified"
      ? current.technique
      : movementDefaults?.technique && movementDefaults.technique !== "unspecified"
        ? movementDefaults.technique
        : "singles";
  return {
    movement: effectiveMovement,
    start: String(current?.start_secs ?? interval.start_secs),
    end: String(current?.end_secs ?? interval.end_secs),
    reps: reps == null ? "" : String(reps),
    loadMode,
    weight,
    unit,
    basis,
    measurement: current?.measurement ?? "set",
    technique,
    reviewed: false,
    ...(current?.interval_id && current.interval_id !== interval.id ? { targetIntervalId: current.interval_id } : {}),
  };
}

const UNASSESSED_REASON_PRIORITY: Record<string, number> = {
  event_movement_conflict: 6,
  incomplete_cycle: 5,
  skipped_non_exercise: 4,
  no_countable_evidence: 3,
  unconfirmed_target_or_activity: 2,
  unverified_capture_mapping: 1,
  not_analyzed: 0,
};

export function describeUnassessedReason(reason: string): string {
  switch (reason) {
    case "skipped_non_exercise":
      return "휴식·전환으로 자동 스킵됨 (청크 스킵)";
    case "incomplete_cycle":
      return "미완료 반복 또는 화면 잘림 (완료 판정 보류)";
    case "event_movement_conflict":
      return "동작 라벨 충돌 (수동 확인 필요)";
    case "no_countable_evidence":
      return "완료 반복 근거 미확인";
    case "unconfirmed_target_or_activity":
      return "타겟 선수/운동 상태 미확정";
    case "unverified_capture_mapping":
      return "원본 영상 구간 매핑 미검증";
    case "not_analyzed":
      return "세그먼트 운동 구간 미감지";
    default:
      return "자동 카운트 미확정 구간";
  }
}

export interface UnassessedTimelineItem {
  id: string;
  start_secs: number;
  end_secs: number;
  reason: string;
  movement?: string;
}

export function buildUnassessedIntervals(
  progress?: WorkoutProgress | null,
  records: PerformanceRecord[] = [],
): UnassessedTimelineItem[] {
  if (!progress) return [];
  const mediaGaps = progress.unassessed.filter(
    g => g.clock === "media" && Number.isFinite(g.start_secs) && Number.isFinite(g.end_secs) && g.end_secs > g.start_secs + 1e-6,
  );
  if (mediaGaps.length === 0) return [];

  const points = new Set<number>();
  for (const g of mediaGaps) {
    points.add(Math.round(g.start_secs * 1000) / 1000);
    points.add(Math.round(g.end_secs * 1000) / 1000);
  }
  for (const inRow of progress.intervals) {
    if (Number.isFinite(inRow.start_secs) && Number.isFinite(inRow.end_secs) && inRow.end_secs > inRow.start_secs) {
      points.add(Math.round(inRow.start_secs * 1000) / 1000);
      points.add(Math.round(inRow.end_secs * 1000) / 1000);
    }
  }
  const sortedPts = [...points].sort((a, b) => a - b);
  const slices: { start_secs: number; end_secs: number; reason: string; movement?: string }[] = [];

  for (let i = 0; i < sortedPts.length - 1; i++) {
    const s = sortedPts[i];
    const e = sortedPts[i + 1];
    if (e - s <= 1e-4) continue;
    const mid = (s + e) / 2;
    if (progress.intervals.some(inRow => inRow.start_secs <= mid && inRow.end_secs >= mid)) {
      continue;
    }
    const covering = mediaGaps.filter(g => g.start_secs <= mid && g.end_secs >= mid);
    if (covering.length === 0) continue;
    let bestReason = covering[0].reason;
    let bestPri = UNASSESSED_REASON_PRIORITY[bestReason] ?? 0;
    let movement: string | undefined;
    for (const g of covering) {
      const pri = UNASSESSED_REASON_PRIORITY[g.reason] ?? 0;
      if (pri > bestPri) {
        bestPri = pri;
        bestReason = g.reason;
      }
      if (!movement && g.movement?.trim()) {
        movement = g.movement.trim();
      }
    }
    slices.push({ start_secs: s, end_secs: e, reason: bestReason, ...(movement ? { movement } : {}) });
  }

  const merged: { start_secs: number; end_secs: number; reason: string; movement?: string }[] = [];
  for (const slice of slices) {
    const prev = merged[merged.length - 1];
    if (prev && slice.start_secs - prev.end_secs <= 0.5 + 1e-6) {
      const prevMov = prev.movement ?? "";
      const currMov = slice.movement ?? "";
      const prevDur = prev.end_secs - prev.start_secs;
      const currDur = slice.end_secs - slice.start_secs;
      const sameMovement = prevMov === currMov;
      const shortBoundaryAbsorption =
        (prevDur < MAX_SEGMENT_BOUNDARY_GAP_SECS && currMov === "" && slice.reason === "not_analyzed") ||
        (currDur < MAX_SEGMENT_BOUNDARY_GAP_SECS && prevMov === "" && prev.reason === "not_analyzed");
      if (sameMovement || shortBoundaryAbsorption) {
        prev.end_secs = slice.end_secs;
        const prevPri = UNASSESSED_REASON_PRIORITY[prev.reason] ?? 0;
        const currPri = UNASSESSED_REASON_PRIORITY[slice.reason] ?? 0;
        if (currPri > prevPri) {
          prev.reason = slice.reason;
        }
        if (!prev.movement && slice.movement) {
          prev.movement = slice.movement;
        }
        continue;
      }
    }
    merged.push({ ...slice });
  }

  const active = activePerformanceRecords(records);
  return merged
    .map(g => ({
      id: `unassessed-${g.start_secs}-${g.end_secs}`,
      start_secs: g.start_secs,
      end_secs: g.end_secs,
      reason: g.reason,
      ...(g.movement ? { movement: g.movement } : {}),
    }))
    .filter(
      item =>
        item.reason !== "not_analyzed" ||
        Boolean(item.movement?.trim()) ||
        item.end_secs - item.start_secs > MAX_SEGMENT_BOUNDARY_GAP_SECS + 1e-6 ||
        active.some(r => r.interval_id === item.id),
    );
}

const WOD_MOVEMENT_PATTERNS: { pattern: RegExp; canonical: string }[] = [
  { pattern: /clean\s*(?:&|and)\s*jerks?|c\s*&\s*j|클린\s*(?:앤|엔)?\s*저크/i, canonical: "Clean & Jerk" },
  { pattern: /power\s*cleans?|파워\s*클린/i, canonical: "Power Clean" },
  { pattern: /squat\s*cleans?|스쿼트\s*클린/i, canonical: "Squat Clean" },
  { pattern: /hang\s*power\s*cleans?|행\s*파워\s*클린/i, canonical: "Hang Power Clean" },
  { pattern: /power\s*snatch(?:es)?|파워\s*스내치/i, canonical: "Power Snatch" },
  { pattern: /squat\s*snatch(?:es)?|스쿼트\s*스내치/i, canonical: "Squat Snatch" },
  { pattern: /push\s*jerks?|푸시\s*저크|푸쉬\s*저크/i, canonical: "Push Jerk" },
  { pattern: /split\s*jerks?|스플릿\s*저크/i, canonical: "Split Jerk" },
  { pattern: /push\s*press(?:es)?|푸시\s*프레스|푸쉬\s*프레스/i, canonical: "Push Press" },
  { pattern: /thrusters?|쓰러스터|스러스터/i, canonical: "Thruster" },
  { pattern: /front\s*squats?|프론트\s*스쿼트|프런트\s*스쿼트/i, canonical: "Front Squat" },
  { pattern: /back\s*squats?|백\s*스쿼트/i, canonical: "Back Squat" },
  { pattern: /overhead\s*squats?|\bohs\b|오버헤드\s*스쿼트/i, canonical: "Overhead Squat" },
  { pattern: /deadlifts?|데드리프트/i, canonical: "Deadlift" },
  { pattern: /wall\s*balls?(?:\s*shots?)?|월볼(?:\s*샷)?/i, canonical: "Wallball Shot" },
  { pattern: /chest[\s-]*to[\s-]*bar|\bc2b\b|체스트\s*투\s*바/i, canonical: "Chest to Bar" },
  { pattern: /bar\s*muscle[\s-]*ups?|\bbmu\b|바\s*머슬업/i, canonical: "Bar Muscle-up" },
  { pattern: /ring\s*muscle[\s-]*ups?|\brmu\b|링\s*머슬업/i, canonical: "Ring Muscle-up" },
  { pattern: /toes[\s-]*to[\s-]*bar|\bt2b\b|토\s*투\s*바|토투바/i, canonical: "Toes to Bar" },
  { pattern: /pull[\s-]*ups?|풀업|턱걸이/i, canonical: "Pull-up" },
  { pattern: /handstand\s*push[\s-]*ups?|\bhspu\b|핸드스탠드\s*푸시업|핸드스탠드\s*푸쉬업/i, canonical: "Handstand Push-up" },
  { pattern: /burpees?|버피/i, canonical: "Burpee" },
  { pattern: /box\s*jumps?|박스\s*점프/i, canonical: "Box Jump" },
  { pattern: /double[\s-]*unders?|\bdus\b|더블\s*언더|이단뛰기/i, canonical: "Double-under" },
  { pattern: /shuttle\s*runs?|셔틀\s*런|왕복\s*달리기/i, canonical: "Shuttle Run" },
  { pattern: /plate\s*ground[\s-]*to[\s-]*overhead|플레이트\s*그라운드\s*투\s*오버헤드/i, canonical: "Plate Ground-to-Overhead" },
];

export function normalizeMovementName(raw?: string | null): string {
  const trimmed = raw?.trim() ?? "";
  if (!trimmed) return "";
  for (const { pattern, canonical } of WOD_MOVEMENT_PATTERNS) {
    const anchored = new RegExp(`^(?:${pattern.source})$`, "i");
    if (anchored.test(trimmed)) {
      return canonical;
    }
  }
  return trimmed;
}

export function extractSuggestedMovements(
  wodDescription?: string | null,
  progress?: WorkoutProgress | null,
  records: PerformanceRecord[] = [],
): string[] {
  const seen = new Set<string>();
  const add = (mov?: string | null) => {
    const trimmed = mov?.trim();
    if (!trimmed) return;
    if (!seen.has(trimmed)) seen.add(trimmed);
  };
  if (wodDescription?.trim()) {
    for (const { pattern, canonical } of WOD_MOVEMENT_PATTERNS) {
      if (pattern.test(wodDescription)) {
        add(canonical);
      }
    }
  }
  for (const inRow of progress?.intervals ?? []) {
    if (inRow.activity === "exercise") add(inRow.movement);
  }
  for (const g of progress?.unassessed ?? []) {
    add(g.movement);
  }
  for (const r of records) {
    add(r.movement);
  }
  if (seen.has("Power Clean") && !seen.has("Clean & Jerk")) {
    add("Clean & Jerk");
  }
  return [...seen];
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

const MAX_SEGMENT_BOUNDARY_GAP_SECS = 5;

function canBridgeContiguousSetGap(
  unassessed: WorkoutProgress["unassessed"],
  from: number,
  to: number,
): boolean {
  if (to <= from + 1e-6) return true;
  if (coveredByCountOnlyGaps(unassessed, from, to)) return true;
  if (to - from > MAX_SEGMENT_BOUNDARY_GAP_SECS + 1e-6) return false;
  return !unassessed.some(
    g =>
      g.clock === "media" &&
      g.reason === "unverified_capture_mapping" &&
      g.end_secs > from + 1e-6 &&
      g.start_secs < to - 1e-6,
  );
}

function countsAsTargetSquatCycle(fromMovement: string, targetMovement: string): boolean {
  const from = fromMovement.trim().toLowerCase();
  const target = targetMovement.trim().toLowerCase();
  if (from === target) return true;
  if (from.includes("squat clean") && (target.includes("front squat") || target.includes("thruster") || target.includes("cluster"))) {
    return true;
  }
  if (from.includes("squat snatch") && (target.includes("overhead squat") || /\bohs\b/.test(target))) {
    return true;
  }
  return false;
}

export interface ContiguousMovementOption {
  movement: string;
  intervalId: string;
  observedReps: number | null;
}

export interface ContiguousExerciseSpan {
  startSecs: number;
  endSecs: number;
  intervalCount: number;
  observedReps: number | null;
  movementOptions?: ContiguousMovementOption[];
}

/**
 * Finds contiguous exercise intervals (including across 10s segment boundaries,
 * short boundary cuts, incomplete_cycle boundary gaps, or back-to-back setup/transition movements)
 * so users can merge a split set and choose the target movement when multiple movements are present.
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
  const beforeChain: WorkoutProgressInterval[] = [];
  let startSecs = sorted[idx].start_secs;
  for (let i = idx - 1; i >= 0; i--) {
    const prev = sorted[i];
    if (prev.activity === "rest") break;
    if (prev.activity !== "exercise" || !prev.movement?.trim()) continue;
    if (!canBridgeContiguousSetGap(progress.unassessed, prev.end_secs, startSecs)) {
      break;
    }
    if (!performanceSelectionCovered(interval, prev.start_secs, interval.end_secs, "set", progress)) {
      break;
    }
    beforeChain.unshift(prev);
    startSecs = prev.start_secs;
  }
  const chain: WorkoutProgressInterval[] = [...beforeChain, sorted[idx]];
  let endSecs = sorted[idx].end_secs;
  for (let i = idx + 1; i < sorted.length; i++) {
    const next = sorted[i];
    if (next.activity === "rest") break;
    if (next.activity !== "exercise" || !next.movement?.trim()) continue;
    if (!canBridgeContiguousSetGap(progress.unassessed, endSecs, next.start_secs)) {
      break;
    }
    if (!performanceSelectionCovered(interval, startSecs, next.end_secs, "set", progress)) {
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
      if (performanceSelectionCovered(interval, startSecs, g.end_secs, "set", progress)) {
        endSecs = g.end_secs;
      }
    }
  }
  let totalReps = 0;
  let hasReps = false;
  const distinctMovements = new Map<string, string>();
  for (const item of chain) {
    const r = observedReps(item);
    if (r != null) {
      totalReps += r;
      hasReps = true;
    }
    const mov = item.movement?.trim();
    if (mov && !distinctMovements.has(mov)) {
      distinctMovements.set(mov, item.id);
    }
  }
  const movementOptions: ContiguousMovementOption[] = [];
  if (distinctMovements.size > 1) {
    for (const [mov, repIntervalId] of distinctMovements.entries()) {
      let optionReps = 0;
      let optionHasReps = false;
      for (const item of chain) {
        const itemMov = item.movement?.trim() ?? "";
        if (countsAsTargetSquatCycle(itemMov, mov)) {
          const r = observedReps(item);
          if (r != null) {
            optionReps += r;
            optionHasReps = true;
          }
        }
      }
      movementOptions.push({
        movement: mov,
        intervalId: repIntervalId,
        observedReps: optionHasReps ? optionReps : null,
      });
    }
  }
  return {
    startSecs,
    endSecs,
    intervalCount: chain.length,
    observedReps: hasReps ? totalReps : null,
    ...(movementOptions.length > 1 ? { movementOptions } : {}),
  };
}

/** A time window may include observed rests; a set may span contiguous exercise intervals across 10s segment boundaries. */
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
    const isSyntheticOrUnassessed = !progress.intervals.some(
      row => row.id === interval.id && row.activity === "exercise" && Boolean(row.movement?.trim()),
    );
    if (isSyntheticOrUnassessed) {
      if (
        progress.intervals.some(
          row => row.activity === "rest" && row.end_secs > start + 1e-6 && row.start_secs < end - 1e-6,
        )
      ) {
        return false;
      }
      return true;
    }
    const movement = interval.movement.trim();
    const exSpans = progress.intervals
      .filter(
        row =>
          row.activity === "exercise" &&
          Boolean(row.movement?.trim()) &&
          row.start_secs < end &&
          row.end_secs > start,
      )
      .sort((a, b) => a.start_secs - b.start_secs);
    if (exSpans.length === 0 || !exSpans.some(row => row.id === interval.id)) return false;
    for (let i = 1; i < exSpans.length; i++) {
      if (!canBridgeContiguousSetGap(progress.unassessed, exSpans[i - 1].end_secs, exSpans[i].start_secs)) {
        return false;
      }
    }
    let candidate: ProgressLoad | null = null;
    for (const row of exSpans) {
      if (row.load && row.movement?.trim() === movement) {
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

function resolveTargetInterval(
  interval: WorkoutProgressInterval,
  draft: PerformanceDraft,
  progress?: WorkoutProgress,
): WorkoutProgressInterval {
  let target = interval;
  if (draft.targetIntervalId && progress) {
    const found = progress.intervals.find(
      row => row.id === draft.targetIntervalId && row.activity === "exercise" && Boolean(row.movement?.trim()),
    );
    if (found) target = found;
  }
  if (draft.movement !== undefined) {
    return {
      ...target,
      activity: "exercise",
      movement: draft.movement.trim(),
    };
  }
  return target;
}

export function diagnosePerformanceDraft(
  interval: WorkoutProgressInterval,
  draft: PerformanceDraft,
  progress?: WorkoutProgress,
): string | null {
  const effectiveInterval = resolveTargetInterval(interval, draft, progress);
  if (!effectiveInterval.movement?.trim()) {
    return "운동 종목을 입력하거나 선택해주세요.";
  }
  if (draft.start.trim() === "" || draft.end.trim() === "") {
    return "시작(초)과 종료(초)를 입력해주세요.";
  }
  const start = Number(draft.start), end = Number(draft.end);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
    return "종료(초)는 시작(초)보다 커야 합니다.";
  }
  if (start < 0 || start >= effectiveInterval.end_secs || end <= effectiveInterval.start_secs) {
    return `시작~종료 범위가 현재 선택한 운동 구간(${progressTime(effectiveInterval.start_secs)}–${progressTime(effectiveInterval.end_secs)})과 겹쳐야 합니다.`;
  }
  const reps = Number(draft.reps);
  if (draft.reps.trim() === "" || !Number.isSafeInteger(reps) || reps < 0) {
    return "완료 반복수에 0 이상의 정수를 입력해주세요.";
  }
  // A zero-rep attempt still records its actual load; null load means no external load.
  if (draft.loadMode === "unresolved") {
    return "수행 중량 구분이 '미확인' 상태입니다. '맨몸 수행' 또는 '중량 기구 사용'을 선택해주세요.";
  }
  if (draft.loadMode === "weighted") {
    const val = Number(draft.weight);
    if (draft.weight.trim() === "" || !Number.isFinite(val) || val <= 0) {
      return "중량 기구 사용 시 0보다 큰 중량(무게)을 입력해주세요.";
    }
  }
  if (!performanceSelectionCovered(effectiveInterval, start, end, draft.measurement, progress)) {
    if (
      draft.measurement === "set" &&
      progress &&
      performanceSelectionCovered(effectiveInterval, start, end, "window", progress)
    ) {
      return "선택한 범위에 휴식(rest) 구간이 포함되어 있습니다. 휴식을 포함해 여러 세트를 묶으려면 측정 방식을 '시간 구간'으로 변경해주세요.";
    }
    return "선택한 시작~종료 범위에 미관찰/녹화 공백이나 다른 동작 구간이 포함되어 있습니다.";
  }
  return null;
}

export function confirmedPerformanceInput(interval: WorkoutProgressInterval, draft: PerformanceDraft, progress?: WorkoutProgress): ConfirmPerformanceRecord | null {
  if (!draft.reviewed || draft.start.trim() === "" || draft.end.trim() === "" || draft.reps.trim() === "") return null;
  const effectiveInterval = resolveTargetInterval(interval, draft, progress);
  const start = Number(draft.start), end = Number(draft.end), reps = Number(draft.reps);
  if (!performanceSelectionCovered(effectiveInterval, start, end, draft.measurement, progress) || !Number.isSafeInteger(reps) || reps < 0) return null;
  // Zero reps still need the actual load: null load explicitly means no external load.
  if (draft.loadMode === "unresolved") return null;
  let load: ProgressLoad | null = null;
  if (draft.loadMode === "weighted") {
    const value = Number(draft.weight);
    if (draft.weight.trim() === "" || !Number.isFinite(value) || value <= 0) return null;
    load = { value, unit: draft.unit, basis: draft.basis };
  }
  const originalRow = progress?.intervals.find(row => row.id === effectiveInterval.id);
  const originalMovement = originalRow?.movement?.trim() ?? interval.movement?.trim() ?? "";
  const effectiveMovement = normalizeMovementName(effectiveInterval.movement);
  const isSyntheticOrUnassessed =
    !originalRow || originalRow.activity !== "exercise" || !originalRow.movement?.trim();
  const shouldIncludeMovement =
    Boolean(effectiveMovement) && (isSyntheticOrUnassessed || effectiveMovement !== originalMovement);
  return {
    interval_id: effectiveInterval.id,
    ...(shouldIncludeMovement ? { movement: effectiveMovement } : {}),
    start_secs: start,
    end_secs: end,
    reps,
    load,
    measurement: draft.measurement,
    technique: draft.technique,
  };
}

