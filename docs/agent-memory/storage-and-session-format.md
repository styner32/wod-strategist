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
- Mobile merge (`mergeDownloadedChunks` in `merge_chunks.go`) fails closed unless every original has one readable presented video stream and zero or one readable audio stream. Multiple real A/V tracks are ambiguous; attached pictures and non-A/V metadata are excluded. Profiles retain actual stream indexes/order/counts, video and audio `time_base`, codec, pixel format, display geometry/rotation, audio sample rate/channel count/layout. Every time base must be a positive rational; a codec sample rate does not prove its time base.
  - **Video owns the timeline:** decode presented video PTS and complete frame durations before transforming any chunk. `format.duration` and capture-clock `start_secs`/`end_secs` do not define merge lengths. Keep the original retained-video interval for independent validation.
  - **Leading black:** for video spans at least `1.0s`, inspect the first `0.35s` using `blackdetect=d=0.015:pix_th=0.05:pic_th=0.98`. Remove `max(0.01, black_end - 0.001)` only when `black_start <= 0.005` and `0.015 <= black_end <= 0.20`. A failed probe fails the merge; real long dark scenes remain intact.
  - **Prepare every chunk once (bounded parallel):** probe stream profiles and prepare chunks concurrently (`mergeChunkPrepConcurrency`, up to `maxMergeChunkPrepConcurrency = 4`), then assemble `signatures`, `intervals`, and `concatEntries` deterministically in `0..len-1` index order. Normalize video to upright `H.264 yuv420p 30fps`, `-video_track_timescale 30000`. The resolution decision retains exactly the pre-repair codec/pixel format/dimensions/rotation/video time base and audio presence/codec/rate/channel-count comparisons: homogeneous inputs retain their display resolution; previously heterogeneous inputs retain `scale+pad`, even dimensions and the 1280px long-edge limit. Newly audited audio time base, stream indexes/order/counts and channel layout never independently trigger a downscale; they are strictly probed/mapped and canonicalized for every input. There is no sparse-trim path mixing native and FFmpeg timescales and no unsafe concat fallback after probe failure.
  - **Preserve A/V relative placement:** use `-copyts`, trim both tracks against the shared original video start plus black trim, then subtract that same anchor from both PTS. Never independently reset an existing audio track with `asetpts=PTS-STARTPTS`: that removes a genuine audio delay. Resample to `AAC 44100Hz stereo`, fill missing/short audio with silence, and clamp audio to the retained video interval. A session with no audio stays video-only.
  - **Concat:** write each prepared video's actual duration into the concat manifest. Use `-copyts -xerror`, explicit `-map`, `-c:v copy` and one final AAC encode (`aresample=44100:async=1:first_pts=0,apad,atrim=duration=<video total>`). Copying per-file AAC can produce non-monotonic DTS from priming; do not ignore those warnings or re-encode the video as a fallback. `-copyts` prevents AAC priming from shifting the final video start.
  - **Independent acceptance:** each original-to-prepared video duration must agree within `1/30s + source timestamp tick` (reusing a single `probeVideoTimeline` per prepared chunk across duration and endpoint checks). Compare every prepared decoded frame, in order, with the final video's decoded frames; require the exact total frame count, per-chunk rolling hashes, and boundary PTS. `framehash -hash MURMUR3` runs with `-noautorotate -fps_mode:v passthrough -enc_time_base:v 1:30000` on canonical `yuv420p`, with no resizing/color filter. Validate video/audio/container endpoints with fixed `2/30s + 1/30000s` tolerance, replacing the old `max(1.5s, 5%)` allowance. Unknown timestamps, decode failures, lost/reordered/changed frames or endpoint mismatches stop publication, including single-chunk merges.
  - Persist `media_start_secs`/`media_end_secs` from prepared **video** intervals only after the complete merge passes validation. Public API and schema remain unchanged. Already published media is not automatically rewritten.
- Shared strict media evidence lives in `media_integrity.go`: stream finite positive clocks and frame records instead of accumulating unbounded probe output; treat any `-v error` stderr as failure even if the command exits zero. Timeline duration uses multi-threaded `ffprobe -threads 0` displayed-frame PTS and final-frame duration. A valid video-track edit end may shorten the final frame (important for reordered VFR/B-frame packets), but must follow its PTS; the audio-inclusive movie duration never supplies that bound. Frame identity uses ordered decoded-frame 128-bit MurmurHash3 (`MURMUR3`) values rolled into per-chunk SHA256, with timestamp checks at a 30000-tick clock.
- Derived media preserves the verified video span: subtitle burn-in uses explicit mapping and fail-closed decoding plus a `0.1s` video-duration guard. TTS pads narration before limiting it to the source video span, then requires identical complete video frame order and a `0.1s` duration guard; failure retains the verified subtitle-only result. Highlights transcode each requested interval accurately rather than stream-copy seeking to an earlier keyframe, use measured clip durations in manifests and stored `duration_sec`, verify every frame across raw concat, and require `0.1s` endpoint agreement after polish. Frame interpolation intentionally changes pixels, so polished frames are not compared with raw-concat hashes; failed polish falls back only to the already verified raw reel.
- Validation (2026-10-29, local FFmpeg 9.0): all **555 worker specs passed**, with no skipped specs, using `go test ./internal/worker -count=1 -v -ginkgo.no-color -ginkgo.succinct` from `api/`. Existing test migration 60 was applied to `wod_test`; stale uniqueness locks pointing to deleted tasks were cleared only in dedicated test Redis DB 15 before the final run. Coverage includes 600/90000 video timescales, alternate AAC time bases, stream order, audio trim/padding/delay, VFR with B-frames, 200 AAC seams, complete decoded-frame preservation, exact highlight content and publication guards. Generated fixtures do not establish production-image timing/performance or physical-device recording behavior, and historical outputs are not automatically repaired.
- Legacy reconstruction must mirror the deterministic concat manifest order
  (`start_secs` ordering may choose order only) and probe retained chunk
  durations. Never copy capture-clock values onto `merged.mp4`.
- If reconstruction is impossible, a retained chunk object may be used as the
  exact whole-video fallback (`0..probed duration`).

- The optional environment journal stores `environment_*` JSON/JPEG/m4a/NDJSON under the same `videos/{pid}/{sid}/` prefix. Upload evidence before manifests, only after recording. `DELETE /sessions/:session_id/environment?profile_id=...` removes only this journal after profile ownership verification; video and sensor files remain. See [environment-observation.md](environment-observation.md).
