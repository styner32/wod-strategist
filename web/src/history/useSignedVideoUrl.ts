import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import {
  getSignedUrlRefreshDelayMs,
  isSignedUrlExpiredOrExpiring,
  MIN_PROACTIVE_REFRESH_DELAY_MS,
  recordVideoErrorRecoveryAttempt,
  shouldAttemptVideoErrorRecovery,
  type VideoErrorRecoveryTracker,
  type VideoRestoreState,
} from "./videoUrlExpiry";

export interface SignedVideoUrlOptions {
  videoRef: RefObject<HTMLVideoElement | null>;
  /** Identifies the loaded media; a saved position is restored only into the same source. */
  sourceKey: string;
  url?: string | null;
  expiresAt?: string | null;
  /** Local time the URL was fetched (React Query `dataUpdatedAt`). */
  fetchedAtMs?: number;
  fetching: boolean;
  /** Refetches the signed URL; resolves to the new URL and rejects when the refetch fails. */
  refetchUrl: () => Promise<string | null | undefined>;
  /** Position to save when the element reports none (e.g. after a failed load). */
  fallbackTime?: number;
}

export type VideoRestoreOverrides = Partial<Omit<VideoRestoreState, "sessionKey">>;

type RefreshOutcome = "reloaded" | "unchanged" | "failed";

/**
 * Keeps a time-limited signed playback URL usable on a mounted `<video>`:
 * proactive refresh while paused, capped `onError` recovery, and a one-shot
 * position restore on the next `loadedmetadata`. Every returned callback is
 * stable, so `timeupdate`-driven renders do not re-register listeners or
 * break `memo` on children that receive seek handlers.
 */
export function useSignedVideoUrl(options: SignedVideoUrlOptions) {
  const latest = useRef(options);
  useLayoutEffect(() => {
    latest.current = options;
  });
  const pendingRestoreRef = useRef<VideoRestoreState | null>(null);
  const recoveryRef = useRef<VideoErrorRecoveryTracker>({ attempts: 0 });
  const wantPlayingRef = useRef(false);
  const [playbackError, setPlaybackErrorState] = useState(false);
  const playbackErrorRef = useRef(false);

  const setPlaybackError = useCallback((value: boolean) => {
    playbackErrorRef.current = value;
    setPlaybackErrorState(value);
  }, []);

  // Saves the playback state for the next loadedmetadata and refetches. Only a
  // new URL reloads the element; otherwise no media event will follow.
  const refresh = useCallback(async (overrides?: VideoRestoreOverrides): Promise<RefreshOutcome> => {
    const { videoRef, sourceKey, url: previousUrl, fallbackTime, refetchUrl } = latest.current;
    const video = videoRef.current;
    pendingRestoreRef.current = {
      sessionKey: sourceKey,
      currentTime:
        overrides?.currentTime ??
        (video && video.currentTime > 0 ? video.currentTime : fallbackTime ?? 0),
      playbackRate:
        overrides?.playbackRate ?? (video && video.playbackRate > 0 ? video.playbackRate : 1),
      wasPlaying:
        overrides?.wasPlaying ?? (video ? !video.paused && !video.ended : wantPlayingRef.current),
    };
    try {
      const nextUrl = await refetchUrl();
      if (!nextUrl) return "failed";
      return nextUrl === previousUrl ? "unchanged" : "reloaded";
    } catch {
      return "failed";
    }
  }, []);

  const refreshIfExpiringWhilePaused = useCallback(() => {
    const { videoRef, fetching, expiresAt, fetchedAtMs } = latest.current;
    const video = videoRef.current;
    const pausedOrIdle = !video || video.paused || video.ended;
    if (!pausedOrIdle || fetching || !isSignedUrlExpiredOrExpiring(expiresAt, fetchedAtMs)) return;
    void refresh({ wasPlaying: false }).then((outcome) => {
      if (outcome !== "reloaded") pendingRestoreRef.current = null;
    });
  }, [refresh]);

  const { url, expiresAt, fetchedAtMs } = options;
  // Refresh before expiry while paused, or when returning to the tab.
  useEffect(() => {
    if (!url) return;
    const delayMs = getSignedUrlRefreshDelayMs(expiresAt, fetchedAtMs);
    const timerId =
      delayMs != null
        ? window.setTimeout(refreshIfExpiringWhilePaused, Math.max(MIN_PROACTIVE_REFRESH_DELAY_MS, delayMs))
        : undefined;
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") refreshIfExpiringWhilePaused();
    };
    // The web tests run in the React Native Jest environment, which has no DOM events.
    const hasDom = typeof document !== "undefined";
    if (hasDom) {
      document.addEventListener("visibilitychange", onVisibilityChange);
      window.addEventListener("focus", refreshIfExpiringWhilePaused);
    }
    return () => {
      if (timerId != null) window.clearTimeout(timerId);
      if (hasDom) {
        document.removeEventListener("visibilitychange", onVisibilityChange);
        window.removeEventListener("focus", refreshIfExpiringWhilePaused);
      }
    };
  }, [url, expiresAt, fetchedAtMs, refreshIfExpiringWhilePaused]);

  /** True when a seek or play must load a fresh URL instead of using the element. */
  const needsReload = useCallback(() => {
    const { videoRef, expiresAt: expires, fetchedAtMs: fetchedAt } = latest.current;
    return (
      videoRef.current?.error != null ||
      playbackErrorRef.current ||
      isSignedUrlExpiredOrExpiring(expires, fetchedAt)
    );
  }, []);

  /** User-intent reload (seek, play, retry button): starts a fresh recovery budget. */
  const reload = useCallback(
    (overrides?: VideoRestoreOverrides) => {
      recoveryRef.current = { attempts: 0 };
      setPlaybackError(false);
      void refresh(overrides).then((outcome) => {
        if (outcome === "unchanged") {
          // Same URL: reload the element so loadedmetadata applies the restore.
          latest.current.videoRef.current?.load();
        } else if (outcome === "failed") {
          pendingRestoreRef.current = null;
          setPlaybackError(true);
        }
      });
    },
    [refresh, setPlaybackError],
  );

  const handleError = useCallback(() => {
    const { videoRef, url: currentUrl } = latest.current;
    const video = videoRef.current;
    if (
      !currentUrl ||
      !shouldAttemptVideoErrorRecovery(recoveryRef.current, currentUrl, video?.error?.code)
    ) {
      pendingRestoreRef.current = null;
      setPlaybackError(true);
      return;
    }
    recoveryRef.current = recordVideoErrorRecoveryAttempt(recoveryRef.current, currentUrl);
    void refresh({ wasPlaying: wantPlayingRef.current || Boolean(video && !video.paused) }).then((outcome) => {
      if (outcome !== "reloaded") {
        pendingRestoreRef.current = null;
        setPlaybackError(true);
      }
    });
  }, [refresh, setPlaybackError]);

  /**
   * Call from `onLoadedMetadata`; returns the saved state to apply exactly once.
   * The recovery budget is kept: a load that fails again later stays capped.
   */
  const takeRestore = useCallback((): VideoRestoreState | null => {
    setPlaybackError(false);
    const restore = pendingRestoreRef.current;
    pendingRestoreRef.current = null;
    if (!restore || restore.sessionKey !== latest.current.sourceKey) return null;
    if (restore.wasPlaying) wantPlayingRef.current = true;
    return restore;
  }, [setPlaybackError]);

  const handlePlay = useCallback(() => {
    wantPlayingRef.current = true;
    setPlaybackError(false);
  }, [setPlaybackError]);

  const handlePause = useCallback(() => {
    if (latest.current.videoRef.current?.error) return;
    wantPlayingRef.current = false;
    refreshIfExpiringWhilePaused();
  }, [refreshIfExpiringWhilePaused]);

  return {
    playbackError,
    pendingRestoreRef,
    wantPlayingRef,
    needsReload,
    reload,
    handleError,
    handlePlay,
    handlePause,
    takeRestore,
  };
}
