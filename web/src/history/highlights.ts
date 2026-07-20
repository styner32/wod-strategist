export interface HighlightSegment {
  startSeconds: number;
  endSeconds: number;
  startLabel: string;
  endLabel: string;
  type: string;
  movement?: string;
  reason?: string;
}

export const HIGHLIGHT_PREROLL_SECONDS = 5;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function parseHighlightTimestamp(value: unknown): number | null {
  if (typeof value === 'number') {
    return Number.isFinite(value) && value >= 0 ? value : null;
  }
  if (typeof value !== 'string') return null;

  const raw = value.trim();
  if (!raw) return null;
  if (/^\d+(?:\.\d+)?$/.test(raw)) {
    const seconds = Number(raw);
    return Number.isFinite(seconds) ? seconds : null;
  }

  const parts = raw.split(':');
  if (parts.length < 2 || parts.length > 3) return null;
  if (!/^\d+(?:\.\d+)?$/.test(parts.at(-1) ?? '')) return null;
  if (!parts.slice(0, -1).every((part) => /^\d+$/.test(part))) return null;

  const seconds = Number(parts.at(-1));
  if (!Number.isFinite(seconds) || seconds >= 60) return null;

  if (parts.length === 2) {
    const minutes = Number(parts[0]);
    return Number.isFinite(minutes) ? minutes * 60 + seconds : null;
  }

  const hours = Number(parts[0]);
  const minutes = Number(parts[1]);
  if (!Number.isFinite(hours) || !Number.isFinite(minutes) || minutes >= 60) return null;
  return hours * 3600 + minutes * 60 + seconds;
}

export function formatHighlightTimestamp(value: number): string {
  const totalMilliseconds = Math.round(value * 1000);
  const hours = Math.floor(totalMilliseconds / 3_600_000);
  const minutes = Math.floor((totalMilliseconds % 3_600_000) / 60_000);
  const seconds = Math.floor((totalMilliseconds % 60_000) / 1000);
  const milliseconds = totalMilliseconds % 1000;
  const fraction = milliseconds > 0
    ? `.${String(milliseconds).padStart(3, '0').replace(/0+$/, '')}`
    : '';
  const clock = `${String(minutes).padStart(hours > 0 ? 2 : 1, '0')}:${String(seconds).padStart(2, '0')}${fraction}`;
  return hours > 0 ? `${hours}:${clock}` : clock;
}

export function parseHighlightSegments(value: unknown): HighlightSegment[] {
  let parsed: unknown = value;
  if (typeof value === 'string') {
    if (!value.trim()) return [];
    try {
      parsed = JSON.parse(value);
    } catch {
      return [];
    }
  }
  if (!Array.isArray(parsed)) return [];

  return parsed.flatMap((entry): HighlightSegment[] => {
    if (!isRecord(entry)) return [];

    const startSeconds = parseHighlightTimestamp(entry.start ?? entry.start_time);
    const endSeconds = parseHighlightTimestamp(entry.end ?? entry.end_time);
    if (startSeconds == null || endSeconds == null || endSeconds <= startSeconds) return [];

    const type = typeof entry.type === 'string' && entry.type.trim()
      ? entry.type.trim()
      : 'highlight';
    const movement = typeof entry.movement === 'string' && entry.movement.trim()
      ? entry.movement.trim()
      : undefined;
    const rawReason = entry.reason ?? entry.description;
    const reason = typeof rawReason === 'string' && rawReason.trim()
      ? rawReason.trim()
      : undefined;

    return [{
      startSeconds,
      endSeconds,
      startLabel: formatHighlightTimestamp(startSeconds),
      endLabel: formatHighlightTimestamp(endSeconds),
      type,
      movement,
      reason,
    }];
  });
}

export function getHighlightSeekTime(startSeconds: number, durationSeconds?: number): number {
  const target = Math.max(0, startSeconds - HIGHLIGHT_PREROLL_SECONDS);
  return durationSeconds != null && Number.isFinite(durationSeconds) && durationSeconds > 0
    ? Math.min(target, durationSeconds)
    : target;
}
