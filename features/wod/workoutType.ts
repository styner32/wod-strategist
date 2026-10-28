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
export const RECENT_WORKOUT_TTL_MS = 24 * 60 * 60 * 1000; // 24 hours (same-day pre-workout persistence)

export interface RecentWorkoutSetup {
  savedAt: number;
  /** Only this profile is offered the snapshot; it carries appearance hints. */
  profileId: number;
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
      typeof parsed.profileId !== "number" ||
      !Number.isFinite(parsed.profileId) ||
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
      profileId: parsed.profileId,
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

function escapeRegexLiteral(input: string): string {
  return input.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Extracts known catalog movements mentioned in free-form WOD text.
 * Matches longest movement names first (e.g. "Power Snatch" before "Snatch")
 * and handles common plural/hyphen variations without false substring hits (e.g. "Row" in "narrow").
 */
export function extractCatalogMovementsFromText(
  text: string,
  groups: { movements: string[] }[],
): string[] {
  const trimmed = text.trim();
  if (!trimmed || groups.length === 0) return [];

  const uniqueMovements = Array.from(
    new Set(
      groups
        .flatMap((g) => g.movements)
        .map((m) => m.trim())
        .filter(Boolean),
    ),
  ).sort((a, b) => b.length - a.length);

  let remaining = trimmed.toLowerCase();
  const matched: { movement: string; index: number }[] = [];

  for (const movement of uniqueMovements) {
    const tokens = movement
      .toLowerCase()
      .split(/[\s-]+/)
      .filter(Boolean)
      .map(escapeRegexLiteral);
    if (tokens.length === 0) continue;

    const bodyPattern = tokens.join("[\\s-]*");
    const regex = new RegExp(
      `(^|[^a-z0-9])(${bodyPattern})(?:e?s)?(?=$|[^a-z0-9])`,
      "gi",
    );

    let firstIdx = -1;
    remaining = remaining.replace(
      regex,
      (fullMatch, prefix: string, coreMatch: string, offset: number) => {
        if (firstIdx < 0) {
          firstIdx = offset + prefix.length;
        }
        const maskLen = fullMatch.length - prefix.length;
        return prefix + " ".repeat(Math.max(1, maskLen));
      },
    );

    if (firstIdx >= 0) {
      matched.push({ movement, index: firstIdx });
    }
  }

  matched.sort((a, b) => a.index - b.index);
  return matched.map((m) => m.movement);
}
