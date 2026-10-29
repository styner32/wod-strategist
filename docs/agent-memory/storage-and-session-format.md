# Storage and Session Format Memory

## Session ID format
Current format:
`{TYPE}-{YYYYMMDD}-{ULID}` where `TYPE` is `WOD`, `WARMUP`, `ACCESSORY`, or `COOLDOWN`.

Example:
`WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF`

Generation (client-side, in `features/wod/workoutType.ts`):

```ts
import { ulid } from "ulid";
buildWorkoutSessionId("wod"); // → "WOD-20260407-01JQXYZ..."
```

Rules:
- Never embed profile ID in the session ID — use it only as a GCS path prefix.
- The `ulid` npm package is a project dependency. Do not remove it.
- Support both old and new formats when parsing session IDs.

## Legacy format
Old format:
`P{profileId}-WOD-YYYY-MM-DD-HH-MM`

Both formats must remain supported. Legacy-aware parsers:
- `sessionIDFromObjectName()` in `api/internal/controllers/dev_handlers.go` — tries the new `videos/{pid}/{sid}/{file}` regex first, falls back to filename-prefix parsing.
- `buildVideoAssets()` — matches objects by either directory containment or filename prefix.
- `formatSessionLabel()` in `features/wod/sessionLabel.ts` (used by `HistoryList.tsx`) — handles both formats on the client.
- `GetVideoDownloadURL` — searches for both `merged.mp4` (new) and `*_merged_*` (old).

## GCS layout
Current layout:

```
videos/
  {profileId}/
    {sessionId}/
      chunk_001.mp4
      chunk_002.mp4
      merged.mp4
      hardsubbed.mp4
      hl_full_a1b2.mp4
      hl_best_c3d4.mp4
      hl_music_e5f6.mp3
```

Path construction:

| Layer | Function | Pattern |
|---|---|---|
| Upload handler | `buildVideoObjectName(profileID, sessionID, filename)` | `videos/{pid}/{sid}/{filename}` |
| Merge worker | hardcoded `fmt.Sprintf` | `videos/{pid}/{sid}/merged.mp4` |
| Hardsub worker | hardcoded `fmt.Sprintf` | `videos/{pid}/{sid}/hardsubbed.mp4` |
| Highlight worker | hardcoded `fmt.Sprintf` | `videos/{pid}/{sid}/hl_{variant}_{rand}.mp4` |

## API `profile_id` requirements
- `POST /upload-url` — required in JSON body (used to build the GCS path).
- `GET /video-download/:session_id` — required as a query parameter.
- `POST /chunk-complete` and `POST /merge-chunks` — required in JSON body.

## Targeted playback resolution

- History and session-analysis aggregate endpoints must not list GCS objects to
  discover every video variant. Those reads stay database-only.
- `GET /video-download/:session_id?profile_id={id}&kind={kind}` resolves only
  the requested `merged`, `hardsubbed`, or `encoded` kind.
- Resolution checks the exact canonical object
  `videos/{profileId}/{sessionId}/{kind}.mp4` first. Only when it is absent may
  the handler search legacy session-directory and flat-layout prefixes.
- Prefix-list results must be filtered to exact supported filenames or legacy
  kind markers; objects such as `merged.mp4.tmp` are not playable matches.
- Web session playback defaults to `merged` and tries `encoded` only after a
  `404` for backward compatibility. Explicit user selection requests only that
  selected kind.

## Backward compatibility
- Old sessions use a flat layout: `videos/{sessionId}_{filename}` (e.g. `videos/P1-WOD-2026-04-01-14-30_chunk_001.mp4`). These files are **not migrated** — both formats are supported.
- The legacy multipart upload handler (`POST /upload`) keeps the historical
  `videos/0/{sessionId}/...` storage path for compatibility. Its queued
  analysis task must still use a real profile ID resolved from the `sessions`
  row, an optional form `profile_id`, or the exact old `P{id}-WOD-...` format.
  Never insert `analysis_results.profile_id=0`.

Rules:
- Always pass `profile_id` when calling `buildVideoObjectName()` or any upload/download API.
- Place new session-scoped assets under `videos/{pid}/{sid}/`, never in a separate top-level prefix.
- A session folder is a mixed asset container: Apple AI `apple_ai_*.json`/JPEG and sensor NDJSON can coexist with video. `listOriginalChunks` and developer catalog/asset listings must filter `.mp4`/`.mov` (case-insensitive) before treating objects as videos. Non-video assets must not affect chunk counts, video links, session ordering or create video-only catalog entries. Moving evidence to a child folder alone would not replace this filter because GCS prefix listings are recursive.
- Preserve both layouts unless a migration is explicitly requested.

## Capture time versus media time

- `chunk_analysis_results.start_secs` and `end_secs` may be mobile capture-clock
  offsets. They are not authoritative positions in concatenated media.
- `media_start_secs` and `media_end_secs` are media-relative offsets. Server
  splits build a continuous source-relative timeline from cumulative probed
  output-chunk durations; do not calculate later starts as `index * 10`.
- Mobile merge (`mergeDownloadedChunks` in `merge_chunks.go`) probes stream signatures (`video codec`, `pix_fmt`, coded `width × height`, `rotation`, video `time_base` / `VideoTimescale`, and `audio codec / sample_rate / channels`) and leading boundary black flashes across downloaded chunks before `probeConcatMediaIntervals` and concatenation:
  - **Video track timescale (`tbn`) homogeneity invariant:** FFmpeg's `concat` demuxer reuses the first file's stream `time_base` for all subsequent files without rescaling packet PTS when `tbn` changes (e.g., an untrimmed iOS `600`-timescale first chunk followed by a default `libx264` `15360`-timescale trimmed chunk inflates duration 25.6×; an Android `90000`-timescale first chunk collapses trimmed chunks to 0.17×). Therefore:
    1. `chunksNeedNormalization` treats any `VideoTimeBase` / `VideoTimescale` difference across chunks as heterogeneous (`needsNorm = true`).
    2. `canTrimInPlaceForConcat` requires a positive uniform `VideoTimescale` across all chunks, and `trimChunkLeadingBlack` explicitly passes `-video_track_timescale <profile.VideoTimescale>` so partially trimmed chunks match untrimmed siblings.
    3. When `totalChunks >= 4` and at least half require leading-black trimming (`shouldNormalizeForLeadingBlackTrim`), merge routes through single-pass `normalizeChunkForConcat` (`-video_track_timescale 30000`, `canonicalConcatVideoTimescale`) + stream-copy concat (`-c copy`) instead of re-encoding almost every chunk twice.
    4. Both `normalizeChunkForConcat` and `runFFmpegConcat` emit `-video_track_timescale 30000`, and `verifyMergedDurationMatchesIntervals` rejects any `merged.mp4` whose probed duration diverges beyond `max(1.5s, 5%)` from cumulative `mediaIntervals`.
  - **Synthetic leading black-frame stripping (`probeLeadingBlackTrimSecs`):** For chunks with probed duration `≥ 1.0s`, FFmpeg inspects only the first `0.35s` (`blackdetect=d=0.015:pix_th=0.05:pic_th=0.98`) and trims `max(0.01, black_end - 0.001)` seconds when `black_start ≤ 0.005` and `0.015 ≤ black_end ≤ 0.20` (removing periodic 1–6 frame digital black boundary artifacts from `AVAssetExportSession` `600`-tick timescale rounding + `Video.compress`, while never trimming genuinely dark footage where `black_end ≥ 0.35s`). Trim happens in-place (`trimChunkLeadingBlack`) on affected chunks when homogeneous and sparse, or inside `normalizeChunkForConcat` when heterogeneous or bulk-trimmed, **before** `probeConcatMediaIntervals` so persisted `media_start_secs`/`media_end_secs` match `merged.mp4`.
  - **Homogeneous chunks** use single-pass `runFFmpegConcat` (`-f concat` → 30fps H.264 `-video_track_timescale 30000` / AAC).
  - **Heterogeneous chunks** (e.g., mobile `analysis_compression_fallback` switching mid-workout from compressed `H.264 406x720 44.1kHz stereo` to raw iOS `HEVC 1280x720 rot=-90 48kHz mono`, mismatched `time_base`, or chunks missing audio) normalize each chunk first to uniform `H.264 yuv420p 30fps` (`-video_track_timescale 30000`, upright display aspect preserved via `scale+pad`, capped at `1280`px long edge) + `AAC 44100Hz stereo` (filling silent audio via `anullsrc` when needed), probe cumulative `media_start_secs`/`media_end_secs` on the normalized chunks, and concatenate via stream copy (`-c copy`), preventing dropped HEVC frames and video/audio-timebase duration drift.
- Legacy reconstruction must mirror the deterministic concat manifest order
  (`start_secs` ordering may choose order only) and probe retained chunk
  durations. Never copy capture-clock values onto `merged.mp4`.
- If reconstruction is impossible, a retained chunk object may be used as the
  exact whole-video fallback (`0..probed duration`).

- The optional environment journal stores `environment_*` JSON/JPEG/m4a/NDJSON under the same `videos/{pid}/{sid}/` prefix. Upload evidence before manifests, only after recording. `DELETE /sessions/:session_id/environment?profile_id=...` removes only this journal after profile ownership verification; video and sensor files remain. See [environment-observation.md](environment-observation.md).
