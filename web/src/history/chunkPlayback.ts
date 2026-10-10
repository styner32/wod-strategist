/**
 * Interval playback rules for the Chunk Inspector `<video>`.
 *
 * Browsers convert a seek target into the container time base, so reading
 * `currentTime` back after `currentTime = t` can be slightly below `t`
 * (observed: 2280.9 -> 2280.89999). Exact comparisons against the target then
 * stay true forever and every `timeupdate` issues another seek. All boundary
 * checks here therefore use a tolerance, and no correction runs while the
 * element is paused or already seeking.
 */
export const CHUNK_SEEK_EPSILON_SECS = 0.05;

export interface ChunkMediaState {
  currentTime: number;
  /** NaN before metadata is loaded. */
  duration: number;
  paused: boolean;
  seeking: boolean;
}

export type ChunkTimeUpdateAction =
  | { kind: 'none' }
  | { kind: 'pause' }
  | { kind: 'seek'; to: number };

const NONE: ChunkTimeUpdateAction = { kind: 'none' };

function isFiniteNumber(value: number | null | undefined): value is number {
  return value != null && Number.isFinite(value);
}

/** Start position the element can actually reach, or null if missing/out of range. */
export function reachableChunkStart(
  start: number | null | undefined,
  duration: number,
): number | null {
  if (!isFiniteNumber(start) || start < 0) return null;
  // A start at/after the end of the media would be clamped by the browser and
  // never satisfy `currentTime >= start`.
  if (Number.isFinite(duration) && start >= duration) return null;
  return start;
}

/** Keep active playback inside [start, end). Never seeks while paused or seeking. */
export function chunkTimeUpdateAction(
  media: ChunkMediaState,
  start: number | null | undefined,
  end: number | null | undefined,
): ChunkTimeUpdateAction {
  if (media.paused || media.seeking) return NONE;
  if (isFiniteNumber(end) && media.currentTime >= end - CHUNK_SEEK_EPSILON_SECS) {
    // Stop without seeking back to `end`; a seek here would re-trigger timeupdate.
    return { kind: 'pause' };
  }
  const target = reachableChunkStart(start, media.duration);
  if (target != null && media.currentTime < target - CHUNK_SEEK_EPSILON_SECS) {
    return { kind: 'seek', to: target };
  }
  return NONE;
}

/** Seek target after metadata loads, or null when already at the start. */
export function chunkInitialSeek(
  media: ChunkMediaState,
  start: number | null | undefined,
): number | null {
  const target = reachableChunkStart(start, media.duration);
  if (target == null) return null;
  return Math.abs(media.currentTime - target) > CHUNK_SEEK_EPSILON_SECS ? target : null;
}

/** On play: rewind to start when outside the interval (e.g. replay after reaching end). */
export function chunkPlaySeek(
  media: ChunkMediaState,
  start: number | null | undefined,
  end: number | null | undefined,
): number | null {
  const target = reachableChunkStart(start, media.duration);
  if (target == null) return null;
  const pastEnd = isFiniteNumber(end) && media.currentTime >= end - CHUNK_SEEK_EPSILON_SECS;
  const beforeStart = media.currentTime < target - CHUNK_SEEK_EPSILON_SECS;
  return pastEnd || beforeStart ? target : null;
}
