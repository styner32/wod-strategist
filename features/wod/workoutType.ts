import { ulid } from "ulid";

export type WorkoutType = "wod" | "warmup" | "accessory" | "cooldown";

export const WORKOUT_TYPES: WorkoutType[] = ["wod", "warmup", "accessory", "cooldown"];

export const DEFAULT_WORKOUT_TYPE: WorkoutType = "wod";

export function parseWorkoutType(value?: string | null): WorkoutType {
  const lower = value?.toLowerCase()?.trim();
  if (lower === "warmup" || lower === "accessory" || lower === "cooldown") {
    return lower;
  }
  return DEFAULT_WORKOUT_TYPE;
}

export function formatWorkoutTypeLabel(value: WorkoutType): string {
  const labels: Record<WorkoutType, string> = {
    wod: "WOD",
    warmup: "Warm-up",
    accessory: "Accessory",
    cooldown: "Cooldown",
  };
  return labels[value] ?? "WOD";
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
