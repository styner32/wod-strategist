# Web media playback (HTML5 `<video>`)

Incident baseline: 2026-10-10. The Chunk Inspector pinned the renderer main thread at ~95% CPU
(trace: ~10,700 each of `seeking`/`seeked`/`canplay`/`canplaythrough`/`timeupdate` in 1.1 s) on a
paused, never-played video.

## Root cause

- A seek handler compared positions exactly: `if (video.currentTime < start) video.currentTime = start`.
- Browsers convert the seek target into the container time base; reading `currentTime` back can be
  slightly below the target (observed: `2280.9` → `2280.89999`). The comparison stays true, and each
  seek fires `timeupdate` again → self-sustaining seek loop, no user action required
  (`onLoadedMetadata` started it).
- The old end clamp (`currentTime = end; pause()`) had the same shape: a seek inside `timeupdate`
  re-fires `timeupdate`.
- History: the handler was unchanged from `26f8b4a` (chunk re-analysis) until this fix. The earlier
  CPU work in `302725a` (polling caps, memo, `preload="metadata"`) did not touch it, so the symptom
  returned.

## Why the rate reaches tens of thousands per second

- One cycle = `seeking` → `canplay` → `canplaythrough` → `seeked` → `timeupdate` → handler seeks again.
  Trace: ~10,690 cycles in 1.136 s (~106 µs each) ≈ 53k media events; React dispatcher ~85k calls
  (React attaches every media event listener directly on `<video>`, even without a handler).
- Cycles are near-instant: the target equals the current position, so the frame is already decoded
  (no network, no decode), and a paused element does not wait for vsync.
- No rate limit applies: the spec's 15–250 ms cadence covers only periodic playback `timeupdate`.
  Seek-completion `timeupdate` is queued immediately, and media-element tasks have no `setTimeout`-style 4 ms clamp.

## `timeupdate` cadence and throttling

| Situation | Rate |
|---|---|
| Normal playback (Chrome/Safari) | ~4/s (≈250 ms); Firefox may be higher |
| Scrubbing | once per seek (tens/s) |
| Seek feedback loop | unbounded (~9,400/s observed) |

- Never throttle a handler that seeks to fix a loop: it only slows an infinite loop and delays end detection. Fix convergence instead.
- Handlers that only store state (`SessionDetailPage` playhead) may be throttled (time gate ~250 ms, or coalesce per `requestAnimationFrame`) but must also emit on `seeked`/`pause` so the final position is not dropped. At 4/s the bigger win is render cost: stable callbacks so `memo` holds (e.g. `SensorTimelinePanel` `onReprocess`), memoized chunk sorting, isolated playhead rendering.

## Standard fix patterns (in order of preference)

1. Seek only on one-shot events (`loadedmetadata`, `play`, button clicks); `timeupdate` may only `pause()` at the end.
2. Compare media times with a tolerance (≤ one frame, 0.01–0.05 s), never exactly.
3. Guard programmatic seeks: skip while `video.seeking` (or a ref set before the seek and cleared on `seeked`).
4. Let the platform handle the interval: Media Fragments ``src={`${url}#t=${start},${end}`}`` (fragment is not sent to the server, so signed GCS URLs stay valid; replay-after-end behavior varies by browser), or serve a real clip (`source_kind: "chunk"` already needs no JS boundaries).
5. For frame-accurate end detection use `requestVideoFrameCallback`; `timeupdate` can overshoot by up to ~250 ms.

## Rules

- Any handler for `timeupdate`/`seeked`/`seeking`/`canplay`/`progress` that writes `currentTime`
  is a feedback loop. It must be provably convergent:
  - compare with a tolerance (`CHUNK_SEEK_EPSILON_SECS = 0.05` in `web/src/history/chunkPlayback.ts`), never `<`/`>=` against the exact target;
  - do nothing while `video.paused` or `video.seeking`;
  - at an interval end, `pause()` only — do not seek back to `end`;
  - ignore targets the media cannot reach (`start >= duration`); the browser clamps them and the condition never clears.
- Keep boundary decisions in pure functions (`chunkTimeUpdateAction`, `chunkInitialSeek`, `chunkPlaySeek`, `reachableChunkStart`) and cover them in `web/test/chunkPlayback.test.ts`, including a read-back-below-target case and a "seek action converges" case.
- User-intent seeks (`onPlay`, buttons, timeline clicks) may seek unconditionally; they fire once per action.
- `ChunkInspector` mounts for `chunks[0]` by default, so its `<video>` loads metadata whenever a session detail page opens. Treat its handlers as always active.
- `SessionDetailPage` `onTimeUpdate` only stores `currentTime` in state (no seek); keep it that way.
- `TrackingBoxOverlay` (toggle "추적 박스 표시/끄기", default off, `localStorage` key
  `wod.trackingBoxOverlay`) only reads `currentTime` via
  `requestVideoFrameCallback` (rAF fallback) plus read-only
  `seeked`/`loadedmetadata`/`loadeddata`/`canplay`/`timeupdate`/resize
  repaints; it never seeks or changes playback. Both the `TARGET` box and
  controls render at `z-10` and hide when the docked mini-player is minimized
  (`data-tracking-overlay`). When toggled on for a legacy session without stored
  `boxes` (`저장된 추적 박스 없음`) or at an untracked timestamp (`현재 구간 박스 없음`),
  it displays a status pill (`describeTrackingOverlayState`) rather than leaving
  a silently unresponsive button. Pure rules live in
  `web/src/history/trackingBoxes.ts` (`web/test/trackingBoxes.test.ts`): boxes
  come from `target_cues.boxes` of COMPLETED chunks with confidence ≥ 0.7 and
  verified `media_start_secs`/`media_end_secs`; chunk-local `t` is scaled by
  media/capture duration; boxes interpolate linearly inside one chunk and hide
  outside it; normalized 0..1000 coordinates map into the `object-contain`
  letterboxed rect (`containedVideoRect`). At most three boxes per chunk, so the
  box can lag fast movement; it is a tracking check, not a continuous tracker.
  If a portrait chunk and the merged file disagree in rotation metadata the box
  would be rotated; verify with ffprobe when that is suspected.

## Session review layout

- `SessionReviewWorkspace` keeps a single main player beside 6 workspace tabs:
  `summary` (영상 요약), `performance` (수행 이력), `timeline` (운동 분석 + `H10MemoryPanel`),
  `guidance` (세그먼트·가이드 + `ChunkInspector`), `details` (상세 정보 + `SessionReanalysisPanel`),
  and `device` (온디바이스 AI + `OnDeviceAiPanel`). At `lg` widths the player stays sticky below
  the app header; analysis content scrolls in the right column. Narrow screens stack
  the player above the tabs. Preserve portrait video proportions and cap its height
  to the viewport rather than forcing a landscape player.
- When the original player slot scrolls out of the viewport (observed via `IntersectionObserver`),
  `SessionReviewWorkspace` docks the single `<video>` wrapper into a floating mini-player card
  (`fixed bottom-4 right-4 z-50`) while holding a height-matched placeholder in the document flow
  to prevent scroll-height oscillation. Never mount a second main `<video>` element when docking.
- `VideoTransportBar` renders directly beneath the player with a mini timeline scrubber
  (chunk interval bands + highlight markers), step seek buttons (`±1s`, `±5s`, `±10s`),
  play/pause, playback rate (`0.5x`–`2x`), and keyboard shortcuts (`←/→` ±5s, `Shift+←/→` ±1s,
  `J/L` ±10s, `K` play/pause, suppressed when typing inside interactive controls).
- Tab panels remain mounted with `hidden`, preserving performance editor drafts,
  query polling budgets and the player's playback position. Keyboard arrows/Home/End
  select and focus tabs. Do not place a second main player in each tab.
- Analysis, highlight, workout progress, chunk inspector, and on-device AI seeks must not scroll
  the document away from the selected evidence (`scrollIntoView` is reserved solely for the
  explicit "원래 위치로" button on the docked mini-player). `seeked`, `pause`, `loadedmetadata`,
  `play`, and `ratechange` only read media state without writing a new seek from those events.

## Diagnosing a suspected loop in production

```js
const v = document.querySelectorAll('video')[1];
let n = 0; const h = () => n++;
v.addEventListener('seeked', h);
setTimeout(() => { v.removeEventListener('seeked', h); console.log('seeked/s', n); }, 1000);
```

Thousands per second confirms a loop; compare `v.currentTime`, `v.duration`, `v.paused` with the
`/sessions/:id/chunks/:chunk_id/play-url` `media_start_secs`/`media_end_secs`.

## Signed URL expiry & recovery (`videoUrlExpiry.ts`, `useSignedVideoUrl.ts`)

- Backend playback signed URLs (`GET /video-download/:session_id`, `GET /sessions/:id/chunks/:chunk_id/play-url`, `GET /dev/sessions/:id/play-url`) use `playURLExpiry = time.Hour` (1 hour), and Cloud Run IAM-signed GCS V4 URLs also rotate with the ~1-hour service-account token lifetime.
- Because `web/src/App.tsx` sets `refetchOnWindowFocus: false`, `staleTime` alone never refreshes signed URLs on a mounted `SessionDetailPage` or `ChunkInspector`.
- **One implementation**: both players use `useSignedVideoUrl({ videoRef, sourceKey, url, expiresAt, fetchedAtMs: dataUpdatedAt, fetching, refetchUrl, fallbackTime })`. `refetchUrl` resolves to the new URL and throws when `refetch()` returns `isError`. Do not copy the timer/listener/error logic into a page again.
- **Stable callbacks**: the hook reads its options from a ref (written in `useLayoutEffect`), so `needsReload`, `reload`, `handleError`, `handlePlay`, `handlePause` and `takeRestore` never change identity. Page seek handlers built on them must read play state from `videoRef.current` (e.g. `!video.paused`), not from `currentTime`/`isPaused` state, or every `timeupdate` re-creates them and breaks `memo` on `AnalysisOverview`.
- **Expiry clock**: `getSignedUrlRefreshDelayMs(expiresAt, fetchedAtMs)` measures the lifetime `expires_at - fetchedAtMs` from the local fetch time, never `expires_at` vs `Date.now()` directly. A lifetime `<= SIGNED_URL_REFRESH_BUFFER_MS` (2m) can only be clock skew and falls back to `DEFAULT_SIGNED_URL_TTL_MS` (15m); longer lifetimes are capped at `MAX_PROACTIVE_REFRESH_DELAY_MS + buffer` (52m). Only without `fetchedAtMs` is the absolute `expires_at` used.
- **Proactive refresh**: timer at the refresh delay (min `MIN_PROACTIVE_REFRESH_DELAY_MS = 30s`), plus checks on `visibilitychange`/`focus`/`onPause`. Refresh only while the `<video>` is paused/idle so active streaming is never interrupted.
- **Reactive recovery (`onError`)**: `shouldAttemptVideoErrorRecovery(tracker, url, video.error?.code)` refuses `MEDIA_ERR_DECODE` (3), a URL that already failed, and more than `MAX_VIDEO_ERROR_RECOVERY_ATTEMPTS = 2` attempts within `VIDEO_ERROR_RECOVERY_WINDOW_MS = 5m` (`recordVideoErrorRecoveryAttempt`). Never reset the tracker in `loadedmetadata` or `play`: a file that fails again after each reload would loop forever. Only user-intent `reload()` (seek/play after an error or expiry, the retry button) starts a fresh budget.
- **Failed refresh**: a recovery refetch that rejects, returns no URL, or returns the failing URL produces no media event, so the hook sets `playbackError` (both players render a retry banner) and clears the pending restore. A user `reload()` that gets the same URL calls `video.load()` so the restore still applies.
- **State restoration**: `takeRestore()` in `onLoadedMetadata` returns `{ sessionKey, currentTime, playbackRate, wasPlaying }` once (cleared before the page sets `video.currentTime`) and only for the current `sourceKey` (`${sessionId}:${kind}` / `chunk:${id}`). Never restore inside `timeupdate` or `seeked`.
- **Tests**: web tests run in the React Native Jest environment (root `jest-expo` preset): there is no `document` and `window.addEventListener` is missing, so the hook registers DOM listeners only when `document` exists. Cover the hook in `web/test/useSignedVideoUrl.test.tsx` and the pure rules in `web/test/videoUrlExpiry.test.ts`.
