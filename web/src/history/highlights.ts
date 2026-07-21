export type HighlightObservationType =
  | 'positive_form'
  | 'form_issue'
  | 'fatigue_onset'
  | 'technique_event';

export interface HighlightObservation {
  startSeconds: number;
  endSeconds: number;
  startLabel: string;
  endLabel: string;
  type: HighlightObservationType;
  reason?: string;
  confidence?: number;
  verified?: boolean;
}

export interface HighlightSegment {
  startSeconds: number;
  endSeconds: number;
  startLabel: string;
  endLabel: string;
  type: string;
  movement?: string;
  reason?: string;
  version?: 2;
  tags?: 'key_moment'[];
  observations?: HighlightObservation[];
}

export const HIGHLIGHT_PREROLL_SECONDS = 5;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

const HIGHLIGHT_OBSERVATION_TYPES = new Set<HighlightObservationType>([
  'positive_form',
  'form_issue',
  'fatigue_onset',
  'technique_event',
]);

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

function parseObservation(
  value: unknown,
  parentStartSeconds: number,
  parentEndSeconds: number,
): HighlightObservation | null {
  if (!isRecord(value)) return null;

  const startSeconds = parseHighlightTimestamp(
    value.start ?? value.start_time ?? value.start_secs ?? value.startSeconds,
  );
  const endSeconds = parseHighlightTimestamp(
    value.end ?? value.end_time ?? value.end_secs ?? value.endSeconds,
  );
  const rawType = value.type ?? value.observation_type ?? value.category;
  if (
    startSeconds == null
    || endSeconds == null
    || endSeconds <= startSeconds
    || startSeconds < parentStartSeconds
    || endSeconds > parentEndSeconds
    || typeof rawType !== 'string'
    || !HIGHLIGHT_OBSERVATION_TYPES.has(rawType as HighlightObservationType)
  ) {
    return null;
  }

  const rawReason = value.reason ?? value.description ?? value.claim;
  const reason = typeof rawReason === 'string' && rawReason.trim()
    ? rawReason.trim()
    : undefined;
  const confidence = typeof value.confidence === 'number'
    && Number.isFinite(value.confidence)
    && value.confidence >= 0
    && value.confidence <= 1
    ? value.confidence
    : undefined;
  const verified = typeof value.verified === 'boolean' ? value.verified : undefined;

  return {
    startSeconds,
    endSeconds,
    startLabel: formatHighlightTimestamp(startSeconds),
    endLabel: formatHighlightTimestamp(endSeconds),
    type: rawType as HighlightObservationType,
    ...(reason ? { reason } : {}),
    ...(confidence != null ? { confidence } : {}),
    ...(verified != null ? { verified } : {}),
  };
}

function selectRepresentativeObservations(
  observations: HighlightObservation[],
): HighlightObservation[] {
  const ordered = [...observations].sort(
    (a, b) => a.startSeconds - b.startSeconds || a.endSeconds - b.endSeconds,
  );
  const selected: HighlightObservation[] = [];
  const priorities: HighlightObservationType[] = [
    'positive_form',
    'form_issue',
    'fatigue_onset',
    'technique_event',
  ];

  for (const type of priorities) {
    const observation = ordered.find((candidate) => candidate.type === type);
    if (observation && !selected.includes(observation)) selected.push(observation);
    if (selected.length === 3) break;
  }
  for (const observation of ordered) {
    if (selected.length === 3) break;
    if (!selected.includes(observation)) selected.push(observation);
  }
  return selected.sort(
    (a, b) => a.startSeconds - b.startSeconds || a.endSeconds - b.endSeconds,
  );
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
  if (isRecord(parsed) && Array.isArray(parsed.highlight_segments)) {
    parsed = parsed.highlight_segments;
  }
  if (!Array.isArray(parsed)) return [];

  return parsed.flatMap((entry): HighlightSegment[] => {
    if (!isRecord(entry)) return [];

    const startSeconds = parseHighlightTimestamp(
      entry.start ?? entry.start_time ?? entry.start_secs ?? entry.startSeconds,
    );
    const endSeconds = parseHighlightTimestamp(
      entry.end ?? entry.end_time ?? entry.end_secs ?? entry.endSeconds,
    );
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

    const rawVersion = entry.version ?? entry.schema_version;
    const isV2 = rawVersion === 2 || rawVersion === '2' || Array.isArray(entry.observations);
    const observations = Array.isArray(entry.observations)
      ? selectRepresentativeObservations(entry.observations
        .map((observation) => parseObservation(observation, startSeconds, endSeconds))
        .filter((observation): observation is HighlightObservation => observation != null)
      )
      : [];
    const tags = Array.isArray(entry.tags) && entry.tags.includes('key_moment')
      ? ['key_moment'] as 'key_moment'[]
      : [];

    return [{
      startSeconds,
      endSeconds,
      startLabel: formatHighlightTimestamp(startSeconds),
      endLabel: formatHighlightTimestamp(endSeconds),
      type,
      movement,
      reason,
      ...(isV2 ? { version: 2 as const, observations } : {}),
      ...(tags.length > 0 ? { tags } : {}),
    }];
  });
}

export function getHighlightSeekTime(
  startSeconds: number,
  durationSeconds?: number,
  version?: number,
): number {
  const leadInSeconds = version === 2 ? 0 : HIGHLIGHT_PREROLL_SECONDS;
  const target = Math.max(0, startSeconds - leadInSeconds);
  return durationSeconds != null && Number.isFinite(durationSeconds) && durationSeconds > 0
    ? Math.min(target, durationSeconds)
    : target;
}
