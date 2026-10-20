export interface WODStart {
  elapsed_seconds?: number;
  seconds: number | null;
  end_seconds: number | null;
  status: "confirmed" | "unresolved";
  source: "video" | "manual";
  evidence: string;
}
export interface WODScore {
  evaluation_version: number;
  overall: number | null;
  form: number | null;
  intensity: number | null;
  consistency: number | null;
  wod_start: WODStart;
  evidence_status: string;
}
export interface WODTraining {
  eligible: boolean;
  effort: number | null;
  wod_start: WODStart | null;
  session_load: number | null;
  score: WODScore | null;
}
export interface WODLoadWindow {
  days: number;
  total: number;
  daily_average: number;
  included: number;
  missing_effort: number;
  missing_duration: number;
}
export interface WODTrainingLoad {
  comparison: {
    seven: WODLoadWindow;
    twenty_eight: WODLoadWindow;
    change_percent: number | null;
    status: "available" | "collecting_baseline" | "incomplete_records" | "no_baseline";
    tracking_started_at: string;
  };
  missing_workout_time: number;
  /** Legacy WODs in the tracking period whose version-1 reanalysis was not applied. */
  unevaluated_legacy_workouts?: number;
  timezone: string;
}
export function newWODScore(raw?: string): WODScore | undefined {
  try { const s = JSON.parse(raw || "{}"); return s.evaluation_version === 1 ? s : undefined; }
  catch { return undefined; }
}
export function wodSeconds(seconds: number | null | undefined): string {
  if (seconds == null) return "—";
  return `${Math.floor(seconds / 60)}:${Math.floor(seconds % 60).toString().padStart(2, "0")}`;
}

/**
 * Parses a start timestamp in `MM:SS`, `HH:MM:SS` (with optional fractional seconds),
 * or plain non-negative seconds (e.g. `"135"`, `"135.5"`, `"135s"`).
 * Returns `null` if empty or invalid.
 */
export function parseStartTimestamp(raw: string): number | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;

  if (trimmed.includes(":")) {
    const parts = trimmed.split(":");
    if (parts.length === 2) {
      const [minsStr, secsStr] = parts;
      if (!/^\d+$/.test(minsStr) || !/^\d+(?:\.\d+)?$/.test(secsStr)) return null;
      const mins = Number(minsStr);
      const secs = Number(secsStr);
      if (!Number.isFinite(mins) || !Number.isFinite(secs) || secs >= 60) return null;
      return Math.round((mins * 60 + secs) * 1000) / 1000;
    }
    if (parts.length === 3) {
      const [hoursStr, minsStr, secsStr] = parts;
      if (!/^\d+$/.test(hoursStr) || !/^\d+$/.test(minsStr) || !/^\d+(?:\.\d+)?$/.test(secsStr)) return null;
      const hours = Number(hoursStr);
      const mins = Number(minsStr);
      const secs = Number(secsStr);
      if (!Number.isFinite(hours) || !Number.isFinite(mins) || !Number.isFinite(secs) || mins >= 60 || secs >= 60) {
        return null;
      }
      return Math.round((hours * 3600 + mins * 60 + secs) * 1000) / 1000;
    }
    return null;
  }

  const clean = trimmed.replace(/s$/i, "");
  if (!/^\d+(?:\.\d+)?$/.test(clean)) return null;
  const val = Number(clean);
  if (!Number.isFinite(val) || val < 0) return null;
  return Math.round(val * 1000) / 1000;
}

/**
 * Formats seconds as `MM:SS` (or `MM:SS.d` when fractional) for timestamp input fields.
 */
export function formatStartTimestamp(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "00:00";
  const rounded = Math.round(seconds * 10) / 10;
  const wholeMinutes = Math.floor(rounded / 60);
  const remSeconds = Math.round((rounded - wholeMinutes * 60) * 10) / 10;
  const wholeSecs = Math.floor(remSeconds);
  const tenths = Math.round((remSeconds - wholeSecs) * 10);
  const mm = String(wholeMinutes).padStart(2, "0");
  const ss = String(wholeSecs).padStart(2, "0");
  return tenths > 0 ? `${mm}:${ss}.${tenths}` : `${mm}:${ss}`;
}
