/** Response-only sensor summary. Times are seconds; coverage and zone ratios are 0..1. */
export interface HeartRateSummary {
  status: "none" | "pending" | "failed" | "completed" | "limited" | "unavailable";
  processing_state: string;
  quality_status: "unknown" | "adequate" | "limited" | "legacy";
  calculation_version?: number;
  device_name?: string;
  avg_bpm?: number;
  peak_bpm?: number;
  min_bpm?: number;
  coverage?: number;
  valid_seconds?: number;
  excluded_seconds?: number;
  unknown_seconds?: number;
  excluded_by_reason?: Record<string, number>;
  low_bpm_seconds?: number;
  contact_coverage?: number;
  zones?: { zone: number; seconds: number; ratio: number }[];
  max_bpm?: number;
  max_bpm_source?: string;
  applied: boolean;
  application_reason: string;
  cardio_before?: number;
  cardio_after?: number;
  cardio_delta?: number;
}
export const HEART_RATE_ZONE_COLORS = ["#6699aa", "#3aaf74", "#d7ac32", "#e57e3a", "#cc5265"];
export function hrNumber(value?: number, suffix = "") {
  return value === undefined || !Number.isFinite(value) ? "—" : `${Math.round(value)}${suffix}`;
}
export function hrPercent(value?: number) {
  return value === undefined ? "—" : hrNumber(value * 100, "%");
}
