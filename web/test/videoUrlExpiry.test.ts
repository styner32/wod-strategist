import {
  DEFAULT_SIGNED_URL_TTL_MS,
  getSignedUrlRefreshDelayMs,
  isSignedUrlExpiredOrExpiring,
  MAX_PROACTIVE_REFRESH_DELAY_MS,
  MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS,
  shouldAttemptVideoErrorRecovery,
  SIGNED_URL_REFRESH_BUFFER_MS,
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
    const expiresIn90Sec = new Date(nowMs + 90 * 1000).toISOString();
    expect(isSignedUrlExpiredOrExpiring(expiresIn90Sec, nowMs, nowMs)).toBe(true);

    const expired1HourAgo = new Date(nowMs - 60 * 60 * 1000).toISOString();
    expect(isSignedUrlExpiredOrExpiring(expired1HourAgo, nowMs, nowMs)).toBe(true);

    const expiresIn30Min = new Date(nowMs + 30 * 60 * 1000).toISOString();
    expect(isSignedUrlExpiredOrExpiring(expiresIn30Min, nowMs, nowMs)).toBe(false);
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
});
