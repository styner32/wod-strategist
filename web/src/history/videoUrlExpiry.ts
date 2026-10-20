/**
 * Helpers for proactive refresh and reactive `<video onError>` recovery
 * when time-limited GCS signed playback URLs expire on SessionDetailPage
 * and ChunkInspector.
 */

/** Refresh signed URLs 2 minutes before their reported `expires_at`. */
export const SIGNED_URL_REFRESH_BUFFER_MS = 2 * 60 * 1000;

/**
 * Cap proactive refresh delay at 50 minutes even if `expires_at` is further in
 * the future, because Cloud Run IAM-signed GCS URLs rotate with the ~1-hour
 * service-account token lifetime.
 */
export const MAX_PROACTIVE_REFRESH_DELAY_MS = 50 * 60 * 1000;

/** Minimum timer interval to avoid tight refetch loops on clock skew. */
export const MIN_PROACTIVE_REFRESH_DELAY_MS = 30 * 1000;

/** Fallback TTL when `expires_at` is omitted or unparseable (15 minutes). */
export const DEFAULT_SIGNED_URL_TTL_MS = 15 * 60 * 1000;

/** Maximum consecutive `<video onError>` signed-URL recovery attempts per source. */
export const MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS = 2;

export interface VideoRestoreState {
  sessionKey: string;
  currentTime: number;
  playbackRate: number;
  wasPlaying: boolean;
}

export interface VideoErrorRecoveryTracker {
  attempts: number;
  lastFailedUrl?: string;
}

/**
 * Returns milliseconds until the signed URL should be refreshed, or `null`
 * if neither `expiresAt` nor `fetchedAtMs` is available.
 * Returns `0` when the URL is already expired or within the refresh buffer.
 */
export function getSignedUrlRefreshDelayMs(
  expiresAt?: string | null,
  fetchedAtMs?: number,
  nowMs: number = Date.now(),
): number | null {
  let expiryMs = NaN;
  if (typeof expiresAt === "string" && expiresAt.trim() !== "") {
    expiryMs = Date.parse(expiresAt);
  }
  if (!Number.isFinite(expiryMs)) {
    if (
      typeof fetchedAtMs === "number" &&
      Number.isFinite(fetchedAtMs) &&
      fetchedAtMs > 0
    ) {
      expiryMs = fetchedAtMs + DEFAULT_SIGNED_URL_TTL_MS;
    } else {
      return null;
    }
  }

  const rawDelay = expiryMs - SIGNED_URL_REFRESH_BUFFER_MS - nowMs;
  if (rawDelay <= 0) {
    return 0;
  }
  return Math.min(rawDelay, MAX_PROACTIVE_REFRESH_DELAY_MS);
}

/**
 * Returns true if the signed URL has already expired or entered its pre-expiry
 * refresh buffer.
 */
export function isSignedUrlExpiredOrExpiring(
  expiresAt?: string | null,
  fetchedAtMs?: number,
  nowMs: number = Date.now(),
): boolean {
  const delay = getSignedUrlRefreshDelayMs(expiresAt, fetchedAtMs, nowMs);
  return delay !== null && delay <= 0;
}

/**
 * Determines whether a `<video onError>` event should trigger a signed-URL
 * refetch. Prevents infinite reload loops when a media file is genuinely
 * corrupt or unreachable even with a fresh signed URL.
 */
export function shouldAttemptVideoErrorRecovery(
  tracker: VideoErrorRecoveryTracker,
  currentUrl?: string | null,
): boolean {
  if (!currentUrl) return false;
  if (tracker.attempts >= MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS) return false;
  if (tracker.lastFailedUrl === currentUrl) return false;
  return true;
}
