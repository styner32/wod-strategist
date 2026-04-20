# Video Analysis Memory

## Architecture
Merged-video analysis uses a two-pass design to reduce timestamp hallucination.

Pipeline:
- chunk recording
- per-chunk analysis
- chunk merge
- merged-video deep analysis

## Pass 1: segment indexing
Preferred source of truth is `chunk_analysis_results` from the DB.
Use app-recorded `start_secs` and `end_secs` whenever available.
Adjacent chunks within 10 seconds may be merged.

Fallback:
- if no chunks exist, use model-based indexing
- inject real video duration into the prompt
- use high media resolution
- post-filter timestamps beyond real duration

## Pass 2: segment analysis
Analyze each segment independently with `VideoMetadata` offsets.
Constrain analysis to the exact time range.

## File lifecycle
Upload once and reuse the same Gemini file URI across passes.

Typical lifecycle:
- upload
- active polling
- index/analyze
- delete

Cleanup rule:
- no injuries: video analysis handler deletes file
- injuries exist: injury analysis handler becomes the final owner and deletes file

## `chunk_analysis_results` schema

| Column | Type | Purpose |
|---|---|---|
| `session_id` | TEXT | Links chunks to a session |
| `file_path` | TEXT | GCS URI of the individual chunk video |
| `exercise_type` | TEXT | Detected movement (e.g. "Snatch", "Pull-up") |
| `start_secs` | REAL | Start offset in merged timeline (app-recorded) |
| `end_secs` | REAL | End offset in merged timeline (app-recorded) |
| `output` | TEXT | Short coaching feedback from chunk analysis |
| `status` | TEXT | PENDING / COMPLETED / FAILED |

## Key helpers and concepts
- `buildSegmentsFromChunks`
- `buildIndexPrompt`
- `buildSegmentAnalysisPrompt`
- `parseSegments`
- `filterSegments`
- `mergeAdjacentSegments`
- `convertToSeconds`
- `formatDuration`

## Gemini API gotchas
- `File.VideoMetadata` is `map[string]any` (not a struct). Extract duration via `file.VideoMetadata["videoDuration"].(string)` and parse with `time.ParseDuration`.
- **NEVER** use the `CachedContent` API with `VideoMetadata` — they are incompatible. The Files API upload serves as the cache layer.

## Anti-hallucination rules
1. Prefer chunk data over model indexing.
2. Inject exact video duration.
3. Require visual evidence in prompts.
4. Post-filter invalid timestamps.
5. Include profile context to reduce person confusion.