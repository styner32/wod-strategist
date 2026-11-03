import { workoutShareReport as enMessages } from "../features/i18n/locales/en.json";
import { workoutShareReport as koMessages } from "../features/i18n/locales/ko.json";
import type {
  PerformanceComparison, PerformanceRecord, ProgressLoad, ProgressObservation, WorkoutProgressGap,
} from "./workoutProgress";

export interface WorkoutShareObservation {
  id: string;
  start_secs: number;
  end_secs: number;
  activity: string;
  movement: string;
  load: ProgressLoad | null;
  observed_reps: number | null;
  form_observations: ProgressObservation[];
  rom_observations: ProgressObservation[];
  pace_observations: ProgressObservation[];
}

export interface WorkoutShareSensorSummary {
  status: string;
  quality_status: string;
  avg_bpm: number | null;
  peak_bpm: number | null;
  coverage: number | null;
  valid_seconds: number | null;
  unknown_seconds: number | null;
  excluded_seconds: number | null;
  calculation_version: number | null;
}

/** An owned, point-in-time projection; no media URLs, profile attributes or credentials. */
export interface WorkoutShareContext {
  schema_version: 1;
  generated_at: string;
  timezone: string;
  session: {
    session_id: string;
    workout_type: string;
    performed_at: string | null;
    performed_at_source: string | null;
    workout_prescription: string;
    source_version: string;
  };
  confirmed_records: PerformanceRecord[];
  comparisons: PerformanceComparison[];
  baseline_records: PerformanceRecord[];
  observations: WorkoutShareObservation[];
  unassessed: WorkoutProgressGap[];
  sensor_summary: WorkoutShareSensorSummary | null;
  effort: number | null;
  warnings: string[];
}

export interface WorkoutShareOptions {
  includeAIObservations: boolean;
  includeSensorSummary: boolean;
}

type Messages = Record<string, string>;

/** Both platforms use the same translation resources, without importing Expo into the web app. */
export function buildWorkoutShareText(
  context: WorkoutShareContext,
  options: WorkoutShareOptions,
  locale: "ko" | "en" = "ko",
): string {
  const messages: Messages = locale === "ko" ? koMessages : enMessages;
  const label = (key: string) => messages[key] ?? key;
  const number = (value: number | null | undefined) => value == null || !Number.isFinite(value)
    ? label("unknown") : new Intl.NumberFormat(locale, { maximumFractionDigits: 3 }).format(value);
  const date = (value: string | null) => {
    if (!value || !Number.isFinite(Date.parse(value))) return label("unknown");
    try {
      return new Intl.DateTimeFormat(locale, {
        timeZone: context.timezone, year: "numeric", month: "2-digit", day: "2-digit",
        hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
      }).format(new Date(value));
    } catch {
      return value;
    }
  };
  const time = (value: number) => {
    if (!Number.isFinite(value) || value < 0) return label("unknown");
    const milliseconds = Math.round(value * 1000);
    const [seconds, fraction] = ((milliseconds % 60000) / 1000).toFixed(3).replace(/\.?0+$/, "").split(".");
    return `${Math.floor(milliseconds / 60000).toString().padStart(2, "0")}:${seconds.padStart(2, "0")}${fraction ? `.${fraction}` : ""}`;
  };
  const range = (start: number, end: number) => `${time(start)}–${time(end)}`;
  const load = (value: ProgressLoad | null, confirmed: boolean) => {
    if (!value) return label(confirmed ? "noExternalLoad" : "unknown");
    return `${number(value.value)} ${value.unit} (${label(value.basis)})`;
  };
  const lines = [
    `# ${label("title")}`, "", label("request"), "", label("snapshotNotice"),
    `${label("generatedAt")}: ${date(context.generated_at)} (${context.timezone})`,
    `${label("session")}: ${context.session.session_id}`,
    `${label("workoutType")}: ${label(context.session.workout_type)}`,
    `${label("performedAt")}: ${date(context.session.performed_at)}`,
    `${label("timeSource")}: ${context.session.performed_at_source ?? label("unknown")}`,
    `${label("prescription")}: ${context.session.workout_prescription || label("unknown")}`,
    `${label("evidenceVersion")}: ${context.session.source_version || label("unknown")}`,
    label("mediaTimeNotice"),
    "", `## ${label("confirmedRecords")}`,
  ];
  const confirmed = context.confirmed_records.filter(record => record.status === "confirmed");
  const recordText = (record: PerformanceRecord) => {
    const duration = record.elapsed_seconds != null && record.elapsed_seconds > 0
      ? record.elapsed_seconds : record.end_secs - record.start_secs;
    return [
      `#${record.id} ${record.movement || label("unknown")} [${range(record.start_secs, record.end_secs)}]`,
      `${label("outcome")}: ${label(record.outcome || "completed")}`,
      `${label("reps")}: ${number(record.reps)}`,
      `${label("load")}: ${load(record.load, true)}`,
      `${label("duration")}: ${number(duration)} ${label("seconds")}`,
      `${label("measurement")}: ${label(record.measurement)}`,
      `${label("technique")}: ${label(record.technique)}`,
      `${label("pace")}: ${number(record.reps_per_minute)} ${label("repsPerMinute")}`,
      `${label("rateBasis")}: ${record.rate_basis ? label(record.rate_basis) : label("unknown")}`,
    ].join(" | ");
  };
  if (!confirmed.length) lines.push(label("noConfirmedRecords"));
  for (const record of confirmed) lines.push(`- ${recordText(record)}`);

  lines.push("", `## ${label("comparisons")}`, label("comparisonNotice"));
  const currentIDs = new Set(confirmed.map(record => record.id));
  const baselines = new Map(context.baseline_records.map(record => [record.id, record]));
  const comparisons = context.comparisons.filter(comparison => currentIDs.has(comparison.current_record_id)
    && baselines.has(comparison.baseline_record_id));
  if (!comparisons.length) lines.push(label("noPriorComparison"));
  const includedBaselines = new Set<number>();
  for (const comparison of comparisons) {
    const differences = comparison.condition_differences.map(difference => label(`condition_${difference}`));
    lines.push(`- #${comparison.current_record_id} → #${comparison.baseline_record_id}: ${label(comparison.kind)}`);
    if (differences.length) lines.push(`  ${label("conditionDifferences")}: ${differences.join(", ")}`);
    // A reference is context, never an improvement percentage.
    if (comparison.kind === "direct" && comparison.rate_change_percent != null) {
      lines.push(`  ${label("rateChange")}: ${number(comparison.rate_change_percent)}%`);
    }
    includedBaselines.add(comparison.baseline_record_id);
    if (comparison.heavier_load_kg != null && comparison.heavier_load_baseline_record_id != null
      && baselines.has(comparison.heavier_load_baseline_record_id)) {
      lines.push(`  ${label("heavierLoad")}: ${number(comparison.heavier_load_kg)} kg (#${comparison.heavier_load_baseline_record_id})`);
      includedBaselines.add(comparison.heavier_load_baseline_record_id);
    }
  }
  for (const id of includedBaselines) {
    const record = baselines.get(id)!;
    lines.push(`- ${label("baseline")}: ${record.session_id}, ${date(record.performed_at)}`,
      `  ${recordText(record)}`, `  ${label("prescription")}: ${record.workout_prescription || label("unknown")}`);
  }

  if (options.includeAIObservations) {
    lines.push("", `## ${label("aiObservations")}`, label("aiNotice"));
    if (!context.observations.length) lines.push(label("noObservations"));
    for (const observation of context.observations) {
      lines.push(`- ${observation.id} [${range(observation.start_secs, observation.end_secs)}] ${observation.movement || label("unknown")} (${label(observation.activity)})`,
        `  ${label("observedReps")}: ${number(observation.observed_reps)} | ${label("observedLoad")}: ${load(observation.load, false)}`);
      for (const [kind, evidence] of [
        ["form", observation.form_observations], ["rom", observation.rom_observations], ["pace", observation.pace_observations],
      ] as const) {
        for (const entry of evidence) {
          lines.push(`  ${label(kind)} [${range(entry.start_secs, entry.end_secs)}]: ${entry.evidence}`);
        }
      }
    }
  }

  if (options.includeSensorSummary) {
    lines.push("", `## ${label("sensorSummary")}`,
      `${label("effort")}: ${context.effort == null ? label("unknown") : `${number(context.effort)}/10`}`);
    const sensor = context.sensor_summary;
    if (!sensor) lines.push(label("noSensorData"));
    else {
      lines.push(`${label("status")}: ${label(`sensor_${sensor.status}`)} | ${label("quality")}: ${label(`quality_${sensor.quality_status}`)}`,
        `${label("averageHR")}: ${number(sensor.avg_bpm)} bpm | ${label("peakHR")}: ${number(sensor.peak_bpm)} bpm`,
        `${label("coverage")}: ${sensor.coverage == null ? label("unknown") : `${number(sensor.coverage * 100)}%`}`,
        `${label("validSeconds")}: ${number(sensor.valid_seconds)} | ${label("excludedSeconds")}: ${number(sensor.excluded_seconds)} | ${label("unknownSeconds")}: ${number(sensor.unknown_seconds)} (${label("seconds")})`,
        `${label("calculationVersion")}: ${number(sensor.calculation_version)}`);
    }
  }

  lines.push("", `## ${label("limitations")}`, label("limitationsNotice"));
  for (const gap of context.unassessed) {
    lines.push(`- [${range(gap.start_secs, gap.end_secs)}; ${gap.clock}] ${gap.movement || ""} ${gap.reason}`.trim());
  }
  for (const warning of context.warnings) lines.push(`- ${label(`warning_${warning}`)}`);
  return `${lines.join("\n")}\n`;
}
