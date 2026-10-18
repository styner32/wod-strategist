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
