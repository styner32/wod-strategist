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

## Diagnosing a suspected loop in production

```js
const v = document.querySelectorAll('video')[1];
let n = 0; const h = () => n++;
v.addEventListener('seeked', h);
setTimeout(() => { v.removeEventListener('seeked', h); console.log('seeked/s', n); }, 1000);
```

Thousands per second confirms a loop; compare `v.currentTime`, `v.duration`, `v.paused` with the
`/sessions/:id/chunks/:chunk_id/play-url` `media_start_secs`/`media_end_secs`.
