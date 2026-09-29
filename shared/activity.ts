export interface LocalizedActivityText { ko: string; en: string }
export interface CaptureAssessment {
  state: "good" | "needs_adjustment" | "unknown";
  issue?: string;
  evidence?: string;
  advice: LocalizedActivityText;
}
export interface MovementObservation {
  movement: string;
  unit: "reps" | "seconds";
  start_secs: number;
  end_secs: number;
  complete: boolean;
  evidence: string;
}
export interface MovementObservations {
  duration_secs: number;
  version: number;
  target_state: "identified" | "ambiguous" | "not_visible" | "unknown";
  activity_state: "exercise" | "rest" | "unknown";
  events: MovementObservation[];
  unassessed: { start_secs: number; end_secs: number; reason: string }[];
}
export interface ContextualCoaching {
  text: LocalizedActivityText;
  current_chunk_id: number;
  source_chunk_ids: number[];
  sources: { gap_after_secs: number; chunk_id: number; start_secs: number; end_secs: number; movement: string; form_issues_seen: string[]; basic_coaching: string }[];
}
export interface ActivitySummary {
  version: number;
  available: boolean;
  source_version: string;
  media_generation?: string;
  review_version: number;
  review_state: "unavailable" | "provisional" | "queued" | "running" | "completed" | "partial" | "failed" | "disabled";
  coverage_scope: "recorded_chunks";
  movements: { movement: string; unit: "reps" | "seconds"; count: number; seconds: number }[];
  unassessed: { chunk_id?: number; clock: "capture" | "media" | "chunk"; start_secs: number | null; end_secs: number | null; reason: string }[];
  reviews: { chunk_id: number; state: string; observations: MovementObservations }[];
}
export function activitySummaryPath(sessionId: string, profileId: number): string {
  return `/sessions/${encodeURIComponent(sessionId)}/activity-summary?profile_id=${profileId}`;
}
export function activityReviewPending(summary?: ActivitySummary | null): boolean {
  return !!summary?.available && ["provisional", "queued", "running"].includes(summary.review_state);
}
