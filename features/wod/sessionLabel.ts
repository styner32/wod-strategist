import { t } from "@/features/i18n";

/**
 * Extracts a human-readable label from session_id.
 * New format: "WOD-20260401-01JQXYZ..." → "WOD"
 * Old format: "P1-WOD-2026-04-01-14-30" → "WOD"
 */
export function formatSessionLabel(sessionId: string): string {
  const parts = sessionId.split("-");
  if (parts.length === 0) return t("common.workout");
  // First segment is the workout type (WOD, WARMUP, ACCESSORY, COOLDOWN, etc.)
  const type = parts[0].toUpperCase();
  switch (type) {
    case "WOD":
      return "WOD";
    case "WARMUP":
      return "Warm-up";
    case "ACCESSORY":
      return "Accessory";
    case "COOLDOWN":
      return "Cooldown";
    case "STRENGTH":
      return "Strength";
    case "CARDIO":
      return "Cardio";
    case "FLEXIBILITY":
      return "Flexibility";
    case "HIIT":
      return "HIIT";
    default:
      // Old format with P{id} prefix: skip to second part
      if (type.startsWith("P") && parts.length > 1) {
        return parts[1].toUpperCase();
      }
      return type;
  }
}
