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
