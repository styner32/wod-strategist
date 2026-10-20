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

export const RECENT_WORKOUT_SETUP_KEY = "wod_recent_workout_setup_v1";
export const RECENT_WORKOUT_TTL_MS = 60 * 60 * 1000; // 1 hour

export interface RecentWorkoutSetup {
  savedAt: number;
  workoutType: WorkoutType;
  wodDescription: string;
  rawText: string;
  selectedMovements: string[];
  customMovements: string[];
  sessionAppearance: string;
}

export function parseRecentWorkoutSetup(
  raw: string | null | undefined,
  now: number = Date.now(),
): RecentWorkoutSetup | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<RecentWorkoutSetup>;
    if (
      !parsed ||
      typeof parsed.savedAt !== "number" ||
      !Number.isFinite(parsed.savedAt) ||
      now - parsed.savedAt > RECENT_WORKOUT_TTL_MS ||
      now < parsed.savedAt - 60_000
    ) {
      return null;
    }
    const wodDescription =
      typeof parsed.wodDescription === "string" ? parsed.wodDescription : "";
    const rawText = typeof parsed.rawText === "string" ? parsed.rawText : "";
    const selectedMovements = Array.isArray(parsed.selectedMovements)
      ? parsed.selectedMovements.filter((m): m is string => typeof m === "string")
      : [];
    const customMovements = Array.isArray(parsed.customMovements)
      ? parsed.customMovements.filter((m): m is string => typeof m === "string")
      : [];
    const sessionAppearance =
      typeof parsed.sessionAppearance === "string" ? parsed.sessionAppearance : "";
    const workoutType: WorkoutType = WORKOUT_TYPES.includes(
      parsed.workoutType as WorkoutType,
    )
      ? (parsed.workoutType as WorkoutType)
      : "wod";

    if (
      !wodDescription.trim() &&
      !rawText.trim() &&
      selectedMovements.length === 0
    ) {
      return null;
    }

    return {
      savedAt: parsed.savedAt,
      workoutType,
      wodDescription,
      rawText,
      selectedMovements,
      customMovements,
      sessionAppearance,
    };
  } catch {
    return null;
  }
}
