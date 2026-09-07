/**
 * Converts a raw stretch or alias string into a normalized matching key
 * (lowercased, trimmed, hyphens converted to spaces, consecutive whitespace collapsed).
 * Direct match for backend db.NormalizeStretchKey.
 */
export function normalizeStretchKey(raw: string): string {
  if (!raw) return "";
  const trimmed = raw.trim().toLowerCase();
  if (!trimmed) return "";
  return trimmed.replace(/[\s-]+/g, " ");
}
