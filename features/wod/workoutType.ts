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

const TIME_HEADER_METER_COLLISION_RE =
  /\b(?:amrap|emom|e\d+mom|otm|every|for\s*time|time\s*cap|cap|rest)\s*:?\s*\d+(?:\.\d+)?\s*m(?:in(?:ute)?s?)?\b/gi;
const NON_LUNGE_DISTANCE_BEFORE_RE =
  /(?:^|[^a-z0-9])\d+(?:\/\d+)?(?:\.\d+)?\s*(?:ft|feet|foot|yd|yds|yards?|m|meters?|metres?)\s*(?:shuttle\s+runs?|runs?|running|sprints?|jogs?|rows?|rowing|ski\s*ergs?|skis?|bike\s*ergs?|echo\s*bikes?|assault\s*bikes?|air\s*bikes?|bikes?|swims?|swimming|handstand\s+walks?|hsw\b|farmer'?s?\s+carr(?:y|ies)|carr(?:y|ies)|sled\s+(?:push(?:es)?|pulls?)|broad\s+jumps?|달리기|런닝|셔틀\s*런|런|로잉|스키|바이크|파머스\s*캐리|물구나무\s*걷기)/gi;
const NON_LUNGE_DISTANCE_AFTER_RE =
  /(?:\b(?:shuttle\s+runs?|runs?|running|sprints?|jogs?|rows?|rowing|ski\s*ergs?|skis?|bike\s*ergs?|echo\s*bikes?|assault\s*bikes?|air\s*bikes?|bikes?|swims?|swimming|handstand\s+walks?|hsw|farmer'?s?\s+carr(?:y|ies)|carr(?:y|ies)|sled\s+(?:push(?:es)?|pulls?)|broad\s+jumps?)|(?:달리기|런닝|셔틀\s*런|로잉|스키|바이크|파머스\s*캐리|물구나무\s*걷기))\s*[:\-]?\s*\d+(?:\/\d+)?(?:\.\d+)?\s*(?:ft|feet|foot|yd|yds|yards?|m|meters?|metres?|미터|피트|야드)/gi;
const DISTANCE_PRESCRIPTION_RE =
  /(?:^|[^a-z0-9])\d+(?:\/\d+)?(?:\.\d+)?\s*(?:ft|feet|foot|yd|yds|yards?|m|meters?|metres?)(?=$|[^a-z0-9])|\d+(?:\.\d+)?\s*(?:미터|피트|야드)/i;
const STATIONARY_ONLY_LUNGE_RE = /\b(?:walking|reverse|step[\s-]*back|jumping)\b|워킹|리버스|점핑/i;

function splitWorkoutClausesPreservingDelimiters(text: string): string[] {
  const parts: string[] = [];
  let start = 0;
  let parenDepth = 0;

  const isDigit = (ch: string | undefined) => ch !== undefined && ch >= "0" && ch <= "9";

  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch === "\r" || ch === "\n") {
      if (i > start) parts.push(text.slice(start, i));
      parts.push(ch);
      start = i + 1;
      parenDepth = 0;
      continue;
    }
    if (ch === "(" || ch === "[") {
      parenDepth++;
      continue;
    }
    if (ch === ")" || ch === "]") {
      if (parenDepth > 0) parenDepth--;
      continue;
    }
    if (parenDepth > 0) continue;

    if (
      ch === ";" ||
      ch === ":" ||
      ch === "+" ||
      ch === "," ||
      ch === "|" ||
      ch === "·" ||
      ch === "•" ||
      ch === "→"
    ) {
      if (i > start) parts.push(text.slice(start, i));
      parts.push(ch);
      start = i + 1;
      continue;
    }
    if ((ch === "-" || ch === "=") && text[i + 1] === ">") {
      if (i > start) parts.push(text.slice(start, i));
      parts.push(text.slice(i, i + 2));
      i++;
      start = i + 1;
      continue;
    }
    if (ch === "/" && (!isDigit(text[i - 1]) || !isDigit(text[i + 1]))) {
      if (i > start) parts.push(text.slice(start, i));
      parts.push(ch);
      start = i + 1;
    }
  }
  if (start < text.length) {
    parts.push(text.slice(start));
  }
  return parts;
}

function promoteDistancePrescribedLunges(text: string): string {
  return splitWorkoutClausesPreservingDelimiters(text)
    .map((part) => {
      if (!/lunge|런지/i.test(part) || STATIONARY_ONLY_LUNGE_RE.test(part)) {
        return part;
      }
      const scrubbed = part
        .replace(TIME_HEADER_METER_COLLISION_RE, " ")
        .replace(NON_LUNGE_DISTANCE_BEFORE_RE, " ")
        .replace(NON_LUNGE_DISTANCE_AFTER_RE, " ");
      if (!DISTANCE_PRESCRIPTION_RE.test(scrubbed)) {
        return part;
      }
      return part
        .replace(/\b(lunges?)\b/gi, "walking $1")
        .replace(/런지/g, "워킹 런지");
    })
    .join("");
}

function tokenRegexPattern(token: string): string {
  if (token === "db") return "(?:db|dumbbells?)";
  if (token === "kb") return "(?:kb|kettlebells?)";
  if (token === "&") return "(?:&|and)";
  return escapeRegexLiteral(token);
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
  ).sort((a, b) => {
    const aTokens = a.split(/[\s-]+/).filter(Boolean).length;
    const bTokens = b.split(/[\s-]+/).filter(Boolean).length;
    return bTokens - aTokens || b.length - a.length;
  });

  const hasWalkingLungeCatalog = uniqueMovements.some((m) => /walking\s+lunge/i.test(m));
  const prepared = hasWalkingLungeCatalog ? promoteDistancePrescribedLunges(trimmed) : trimmed;

  let remaining = prepared.toLowerCase();
  const matched: { movement: string; index: number }[] = [];

  for (const movement of uniqueMovements) {
    const tokens = movement
      .toLowerCase()
      .split(/[\s-]+/)
      .filter(Boolean)
      .map(tokenRegexPattern);
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
