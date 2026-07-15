# Storage and Session Format Memory

## Session ID format
Current format:
`{TYPE}-{YYYYMMDD}-{ULID}`

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
- `sessionIDFromObjectName()` in `api/internal/handlers/dev_handlers.go` — tries the new `videos/{pid}/{sid}/{file}` regex first, falls back to filename-prefix parsing.
- `buildVideoAssets()` — matches objects by either directory containment or filename prefix.
- `formatSessionLabel()` in `HistoryList.tsx` — handles both formats on the client.
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
- Test script `scripts/test-chunk-upload.js` must include `profile_id` in the `/upload-url` POST body.

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
- Preserve both layouts unless a migration is explicitly requested.

## Capture time versus media time

- `chunk_analysis_results.start_secs` and `end_secs` may be mobile capture-clock
  offsets. They are not authoritative positions in concatenated media.
- `media_start_secs` and `media_end_secs` are media-relative offsets. Server
  splits build a continuous source-relative timeline from cumulative probed
  output-chunk durations; do not calculate later starts as `index * 10`.
- Mobile merge probes the actual ordered concat inputs and stores cumulative
  media intervals only after the merge succeeds.
- Legacy reconstruction must mirror the deterministic concat manifest order
  (`start_secs` ordering may choose order only) and probe retained chunk
  durations. Never copy capture-clock values onto `merged.mp4`.
- If reconstruction is impossible, a retained chunk object may be used as the
  exact whole-video fallback (`0..probed duration`).
