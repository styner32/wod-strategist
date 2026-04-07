import { ulid } from "ulid";

export type WorkoutType = "wod";

export const DEFAULT_WORKOUT_TYPE: WorkoutType = "wod";

export function parseWorkoutType(value?: string | null): WorkoutType {
  return DEFAULT_WORKOUT_TYPE;
}

export function formatWorkoutTypeLabel(value: WorkoutType): string {
  return "WOD";
}

/**
 * Builds a globally unique, human-readable session ID.
 *
 * Format: `WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF`
 *         {type}-{YYYYMMDD}-{ULID}
 *
 * - ULID provides collision-free uniqueness (128-bit: 48-bit timestamp + 80-bit random)
 * - Date is embedded for human readability when browsing GCS / logs
 * - Profile ID is NOT part of the session ID — it's used as a GCS directory prefix instead
 */
export function buildWorkoutSessionId(
  workoutType: WorkoutType,
  now: Date = new Date()
): string {
  const year = now.getFullYear();
  const month = String(now.getMonth() + 1).padStart(2, "0");
  const day = String(now.getDate()).padStart(2, "0");

  return `${workoutType.toUpperCase()}-${year}${month}${day}-${ulid(now.getTime())}`;
}
