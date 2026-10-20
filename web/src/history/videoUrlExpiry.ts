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

/**
 * Errors further apart than this are independent (successive URL expiries
 * during a long review), so each starts a fresh recovery budget. A reload loop
 * fails again within seconds and stays capped.
 */
export const VIDEO_ERROR_RECOVERY_WINDOW_MS = 5 * 60 * 1000;

/** `MediaError.MEDIA_ERR_DECODE`: a fresh signed URL serves the same bytes. */
const MEDIA_ERR_DECODE = 3;

export interface VideoRestoreState {
  sessionKey: string;
  currentTime: number;
  playbackRate: number;
  wasPlaying: boolean;
}

export interface VideoErrorRecoveryTracker {
  attempts: number;
  lastFailedUrl?: string;
  lastAttemptAtMs?: number;
}

/**
 * Returns milliseconds until the signed URL should be refreshed, or `null`
 * if neither `expiresAt` nor `fetchedAtMs` is available.
 * Returns `0` when the URL is already expired or within the refresh buffer.
 *
 * `expires_at` comes from the server clock. With `fetchedAtMs` (local clock)
 * the lifetime it implies is measured from the fetch, so a skewed client clock
 * shifts the refresh by at most the skew instead of marking every fresh URL
 * expired. A lifetime that is not longer than the refresh buffer can only be
 * skew and falls back to `DEFAULT_SIGNED_URL_TTL_MS`; a longer one is capped
 * so the URL reads as expiring once `MAX_PROACTIVE_REFRESH_DELAY_MS` passes.
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
  if (
    typeof fetchedAtMs === "number" &&
    Number.isFinite(fetchedAtMs) &&
    fetchedAtMs > 0
  ) {
    let lifetimeMs = expiryMs - fetchedAtMs;
    if (!Number.isFinite(lifetimeMs) || lifetimeMs <= SIGNED_URL_REFRESH_BUFFER_MS) {
      lifetimeMs = DEFAULT_SIGNED_URL_TTL_MS;
    }
    expiryMs =
      fetchedAtMs +
      Math.min(lifetimeMs, MAX_PROACTIVE_REFRESH_DELAY_MS + SIGNED_URL_REFRESH_BUFFER_MS);
  } else if (!Number.isFinite(expiryMs)) {
    return null;
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

function recentAttempts(tracker: VideoErrorRecoveryTracker, nowMs: number): number {
  if (
    tracker.lastAttemptAtMs != null &&
    nowMs - tracker.lastAttemptAtMs >= VIDEO_ERROR_RECOVERY_WINDOW_MS
  ) {
    return 0;
  }
  return tracker.attempts;
}

/**
 * Determines whether a `<video onError>` event should trigger a signed-URL
 * refetch. Prevents infinite reload loops when a media file is genuinely
 * corrupt or unreachable even with a fresh signed URL: decode errors never
 * refetch, and the attempt cap holds across reloads within
 * `VIDEO_ERROR_RECOVERY_WINDOW_MS`.
 */
export function shouldAttemptVideoErrorRecovery(
  tracker: VideoErrorRecoveryTracker,
  currentUrl?: string | null,
  mediaErrorCode?: number | null,
  nowMs: number = Date.now(),
): boolean {
  if (!currentUrl) return false;
  if (mediaErrorCode === MEDIA_ERR_DECODE) return false;
  if (recentAttempts(tracker, nowMs) >= MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS) return false;
  if (tracker.lastFailedUrl === currentUrl) return false;
  return true;
}

/** Counts a recovery attempt for `failedUrl`. */
export function recordVideoErrorRecoveryAttempt(
  tracker: VideoErrorRecoveryTracker,
  failedUrl: string,
  nowMs: number = Date.now(),
): VideoErrorRecoveryTracker {
  return {
    attempts: recentAttempts(tracker, nowMs) + 1,
    lastFailedUrl: failedUrl,
    lastAttemptAtMs: nowMs,
  };
}
