import {
  DEFAULT_SIGNED_URL_TTL_MS,
  getSignedUrlRefreshDelayMs,
  isSignedUrlExpiredOrExpiring,
  MAX_PROACTIVE_REFRESH_DELAY_MS,
  MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS,
  recordVideoErrorRecoveryAttempt,
  shouldAttemptVideoErrorRecovery,
  SIGNED_URL_REFRESH_BUFFER_MS,
  VIDEO_ERROR_RECOVERY_WINDOW_MS,
} from "../src/history/videoUrlExpiry";

describe("videoUrlExpiry", () => {
  const nowMs = Date.parse("2026-10-20T08:00:00Z");

  it("computes refresh delay 2 minutes before expires_at and caps at 50 minutes", () => {
    const expiresIn15Min = new Date(nowMs + 15 * 60 * 1000).toISOString();
    expect(getSignedUrlRefreshDelayMs(expiresIn15Min, nowMs, nowMs)).toBe(
      15 * 60 * 1000 - SIGNED_URL_REFRESH_BUFFER_MS,
    );

    const expiresIn2Hours = new Date(nowMs + 2 * 60 * 60 * 1000).toISOString();
    expect(getSignedUrlRefreshDelayMs(expiresIn2Hours, nowMs, nowMs)).toBe(
      MAX_PROACTIVE_REFRESH_DELAY_MS,
    );
  });

  it("falls back to fetchedAtMs + 15m when expires_at is missing or invalid", () => {
    expect(getSignedUrlRefreshDelayMs(undefined, nowMs, nowMs)).toBe(
      DEFAULT_SIGNED_URL_TTL_MS - SIGNED_URL_REFRESH_BUFFER_MS,
    );
    expect(getSignedUrlRefreshDelayMs("not-a-date", nowMs, nowMs)).toBe(
      DEFAULT_SIGNED_URL_TTL_MS - SIGNED_URL_REFRESH_BUFFER_MS,
    );
    expect(getSignedUrlRefreshDelayMs(undefined, undefined, nowMs)).toBeNull();
  });

  it("reports expired or expiring when within the 2-minute buffer or past expires_at", () => {
    const fetchedHourAgo = nowMs - 60 * 60 * 1000;
    const expiresIn90Sec = new Date(nowMs + 90 * 1000).toISOString();
    expect(isSignedUrlExpiredOrExpiring(expiresIn90Sec, nowMs - 15 * 60 * 1000, nowMs)).toBe(true);
    expect(isSignedUrlExpiredOrExpiring(expiresIn90Sec, undefined, nowMs)).toBe(true);

    const expiredMinuteAgo = new Date(nowMs - 60 * 1000).toISOString();
    expect(isSignedUrlExpiredOrExpiring(expiredMinuteAgo, fetchedHourAgo, nowMs)).toBe(true);

    const expiresIn30Min = new Date(nowMs + 30 * 60 * 1000).toISOString();
    expect(isSignedUrlExpiredOrExpiring(expiresIn30Min, nowMs, nowMs)).toBe(false);

    // A 1-hour URL reads as expiring once the 50-minute proactive cap passes.
    const expiresIn1Hour = new Date(nowMs + 60 * 60 * 1000).toISOString();
    expect(isSignedUrlExpiredOrExpiring(expiresIn1Hour, nowMs, nowMs + 49 * 60 * 1000)).toBe(false);
    expect(isSignedUrlExpiredOrExpiring(expiresIn1Hour, nowMs, nowMs + 50 * 60 * 1000)).toBe(true);
  });

  it("measures the lifetime from the local fetch time so client clock skew cannot expire a fresh URL", () => {
    const serverExpiresAt = new Date(nowMs + 60 * 60 * 1000).toISOString();
    // Client clock 1 hour fast: the fresh URL's expires_at reads as now.
    const fastNow = nowMs + 60 * 60 * 1000;
    expect(isSignedUrlExpiredOrExpiring(serverExpiresAt, fastNow, fastNow)).toBe(false);
    expect(getSignedUrlRefreshDelayMs(serverExpiresAt, fastNow, fastNow)).toBe(
      DEFAULT_SIGNED_URL_TTL_MS - SIGNED_URL_REFRESH_BUFFER_MS,
    );
    // Client clock 2 hours slow: the URL still refreshes within its real lifetime.
    const slowNow = nowMs - 2 * 60 * 60 * 1000;
    expect(getSignedUrlRefreshDelayMs(serverExpiresAt, slowNow, slowNow)).toBe(MAX_PROACTIVE_REFRESH_DELAY_MS);
    expect(isSignedUrlExpiredOrExpiring(serverExpiresAt, slowNow, slowNow + MAX_PROACTIVE_REFRESH_DELAY_MS)).toBe(true);
  });

  it("guards <video onError> recovery against duplicate URLs and caps consecutive retries", () => {
    const url1 = "https://storage.googleapis.com/bucket/merged.mp4?X-Goog-Signature=sig1";
    const url2 = "https://storage.googleapis.com/bucket/merged.mp4?X-Goog-Signature=sig2";
    const url3 = "https://storage.googleapis.com/bucket/merged.mp4?X-Goog-Signature=sig3";

    expect(shouldAttemptVideoErrorRecovery({ attempts: 0 }, url1)).toBe(true);
    // Same URL failing again before refetch resolves should not re-trigger
    expect(
      shouldAttemptVideoErrorRecovery({ attempts: 1, lastFailedUrl: url1 }, url1),
    ).toBe(false);
    // Fresh signed URL gets one more chance
    expect(
      shouldAttemptVideoErrorRecovery({ attempts: 1, lastFailedUrl: url1 }, url2),
    ).toBe(true);
    // Stops after MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS
    expect(
      shouldAttemptVideoErrorRecovery(
        { attempts: MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS, lastFailedUrl: url2 },
        url3,
      ),
    ).toBe(false);
  });

  it("never refetches for a decode error", () => {
    const url = "https://storage.googleapis.com/bucket/merged.mp4?X-Goog-Signature=sig1";
    expect(shouldAttemptVideoErrorRecovery({ attempts: 0 }, url, 3)).toBe(false);
    expect(shouldAttemptVideoErrorRecovery({ attempts: 0 }, url, 2)).toBe(true);
    expect(shouldAttemptVideoErrorRecovery({ attempts: 0 }, url, 4)).toBe(true);
  });

  it("keeps the attempt cap across reloads and restores the budget for a later, independent failure", () => {
    let tracker = recordVideoErrorRecoveryAttempt({ attempts: 0 }, "u1", nowMs);
    tracker = recordVideoErrorRecoveryAttempt(tracker, "u2", nowMs + 5_000);
    expect(tracker.attempts).toBe(MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS);
    expect(shouldAttemptVideoErrorRecovery(tracker, "u3", 2, nowMs + 10_000)).toBe(false);

    const later = nowMs + 5_000 + VIDEO_ERROR_RECOVERY_WINDOW_MS;
    expect(shouldAttemptVideoErrorRecovery(tracker, "u3", 2, later)).toBe(true);
    expect(recordVideoErrorRecoveryAttempt(tracker, "u3", later).attempts).toBe(1);
  });
});
