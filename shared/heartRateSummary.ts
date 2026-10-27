export interface HybridIntensitySummary {
  heart_intensity?: number | null;
  motion_intensity?: number | null;
  hybrid_intensity?: number | null;
  hybrid_load_au?: number | null;
  valid_hybrid_seconds: number;
  data_coverage: number;
  peak_exceeded_max_hr?: boolean;
  quality_flags: string[];
  calibration_version: string;
  algorithm_version: string;
}

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
  hybrid?: HybridIntensitySummary;
  applied: boolean;
  application_reason: string;
  cardio_before?: number;
  cardio_after?: number;
  cardio_delta?: number;
}
/**
 * Hybrid `quality_flags` that explain a missing or qualified score, in display
 * order. Keys match the backend (`api/internal/sensor/hybrid_metrics.go`) and
 * the `heartRate.*` locale strings.
 */
export const HYBRID_FLAG_KEYS = [
  "missing_resting_hr",
  "missing_max_hr",
  "invalid_hr_reserve_range",
  "estimated_resting_hr",
  "peak_hr_exceeded_max",
  "sensor_data_unreadable",
  "no_valid_hybrid_windows",
  "low_hybrid_coverage",
] as const;
export function hybridFlagsToShow(flags?: string[] | null): string[] {
  const present = new Set(flags ?? []);
  return HYBRID_FLAG_KEYS.filter((key) => present.has(key));
}
export const HEART_RATE_ZONE_COLORS = ["#6699aa", "#3aaf74", "#d7ac32", "#e57e3a", "#cc5265"];
export function hrNumber(value?: number | null, suffix = "") {
  return value === undefined || value === null || !Number.isFinite(value) ? "—" : `${Math.round(value)}${suffix}`;
}
export function hrDecimal(value?: number | null, digits = 1, suffix = "") {
  return value === undefined || value === null || !Number.isFinite(value) ? "—" : `${value.toFixed(digits)}${suffix}`;
}
export function hrPercent(value?: number | null) {
  return value === undefined || value === null ? "—" : hrNumber(value * 100, "%");
}
