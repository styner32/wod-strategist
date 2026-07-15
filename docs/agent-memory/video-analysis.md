# Video Analysis Memory

## Active remediation plan

The source-level review and ordered implementation plan live in
[`docs/video-analysis-improvement/`](../video-analysis-improvement/README.md).
Read that plan before changing chunk finalization, timestamps, task state,
model routing, prompts, or video-analysis cost behavior.

Known correctness caveats that remain after the 2026-07-15 media/feedback work:

- Mobile `start_secs`/`end_secs` are capture wall-clock offsets. They include
  camera downtime and are not reliable offsets in concatenated `merged.mp4`.
- The stop callback currently skips upload of the final partial chunk, and
  merge has no authoritative expected chunk count.
- Chunk/full result writes are not fully idempotent and partial deep-analysis
  coverage can currently be reported as COMPLETED.

Resolved invariants:

- The production Asynq mux registers `chunk:analysis-with-session` to
  `HandleChunkAnalysisWithSessionTask`.
- Server-split offsets are cumulative actual chunk durations, so keyframe cuts
  cannot create `index * 10` overlaps or gaps.

Do not build new timestamp consumers on current `start_secs`/`end_secs`. The
remediation target keeps capture offsets and adds separately probed cumulative
media offsets for merged-video analysis, subtitles, highlights, and playback.

## Architecture
Merged-video analysis uses a two-pass design to reduce timestamp hallucination.

Pipeline:
- chunk recording
- per-chunk analysis
- chunk merge
- merged-video deep analysis

## Pass 1: segment indexing
Preferred source of truth is verified `media_start_secs` and
`media_end_secs` on `chunk_analysis_results`. Capture-clock values are never
used as merged-video offsets. Rows without verified media offsets are skipped,
which causes model-based indexing to remain the safe fallback.

Current code removes walking/rest/setup rows, retains `Unknown` for deeper
visual revalidation, and merges only touching segments with the same free-text
movement. A filtered rest gap is never merged into the surrounding movement.

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
- on two-pass failure: video analysis attempts deletion
- injuries exist: injury analysis is intended to become the final owner and delete
  the file
- successful no-injury analysis currently retains file metadata and relies on
  Files API expiration; bounded explicit cleanup is part of COST-07 in the
  remediation plan

## `chunk_analysis_results` schema

| Column | Type | Purpose |
|---|---|---|
| `session_id` | TEXT | Links chunks to a session |
| `file_path` | TEXT | GCS URI of the individual chunk video |
| `exercise_type` | TEXT | Detected movement (e.g. "Snatch", "Pull-up") |
| `start_secs` | REAL | Legacy capture-clock start offset; not reliable merged-media time |
| `end_secs` | REAL | Legacy capture-clock end offset; not reliable merged-media time |
| `media_start_secs` | REAL | Verified media-relative start offset; nullable for legacy/unverified rows |
| `media_end_secs` | REAL | Verified media-relative end offset; nullable for legacy/unverified rows |
| `output` | TEXT | Short coaching feedback from chunk analysis |
| `status` | TEXT | PENDING / COMPLETED / FAILED |

Do not interpret app-recorded capture offsets as merged-media truth. Debug
playback and re-analysis may use `media_start_secs`/`media_end_secs` only after
they were written from server-split offsets, probed concat durations, or a
verified legacy reconstruction.

## Key helpers and concepts
- `buildSegmentsFromChunks`
- `buildIndexPrompt`
- `buildSegmentAnalysisPrompt`
- `parseSegments`
- `filterSegments`
- `mergeSegmentsByMovement`
- `convertToSeconds`
- `formatDuration`

## Gemini API gotchas
- `File.VideoMetadata` is `map[string]any` (not a struct). Extract duration via `file.VideoMetadata["videoDuration"].(string)` and parse with `time.ParseDuration`.
- **NEVER** use the `CachedContent` API with `VideoMetadata` — they are incompatible. The Files API upload serves as the cache layer.

## Single-chunk debug re-analysis

- Feature flag: `ENABLE_CHUNK_REANALYSIS=true` on the API server. It is disabled by default.
- Queue task: `chunk:debug-reanalysis`; its payload contains only `run_id`.
- Persistence: `chunk_reanalysis_runs`. Debug candidates must never be inserted into
  `chunk_analysis_results`, because mobile polling treats those rows as production output.
- The server resolves the GCS source. Never accept a GCS URI, Gemini model, prompt, or retry
  count from the browser.
- When `media_start_secs` and `media_end_secs` are verified, analyze that exact interval from
  the GCS session video. Never apply legacy capture-clock offsets to `merged.mp4`.
- For a legacy row without verified merged-media offsets, first reconstruct the concat timeline
  from the ordered retained chunks and their probed durations. If that is impossible, the retained
  `file_path` chunk may be analyzed as its own `0..duration` video. This is an exact-source
  fallback, not a mapping of `start_secs` onto the merged video.
- Reuse an unexpired Gemini Files upload only when a current or prior debug run
  records the exact same `source_gcs_uri` and the Files object still exists.
  Otherwise download the server-resolved GCS object, upload it once, and retain
  exact-source cache metadata for later debug runs. Do not infer source identity
  from an `analysis_results` session/profile match alone.
- Applying a candidate is always an explicit feedback action. Re-analysis does not rewrite
  the original chunk, final analysis, highlights, subtitles, hardsubs, or TTS.
- The pinned `google.golang.org/genai` SDK rounds `VideoMetadata` offsets to whole seconds.
  `internal/gemini.exactSegmentOffsetsTransport` restores the exact context-scoped offsets
  as canonical protobuf Duration JSON immediately before `AnalyzeSegment` sends the request.
  Keep the fractional-offset wire tests when upgrading the SDK; remove the workaround only
  after the SDK preserves fractional offsets itself.

## Anti-hallucination rules
1. Prefer chunk data over model indexing.
2. Inject exact video duration.
3. Require visual evidence in prompts.
4. Post-filter invalid timestamps.
5. Include profile context to reduce person confusion.

## Uploaded video split analysis

Uploaded videos are split into ~10s chunks and analyzed via Gemini API.
Chunks are processed in parallel with bounded concurrency.

### Key constants (`split_video.go`)
| Constant | Value | Purpose |
|---|---|---|
| `splitChunkDurationSecs` | 10 | Target chunk length (matches real-time recording interval) |
| `splitAnalysisConcurrency` | 10 | Max parallel Gemini API calls during split analysis |

### Timeout risk
- Each chunk takes ~18s (GCS upload + Gemini analysis).
- Default asynq task timeout is **30 minutes**.
- At concurrency 10: max ~160 chunks ≈ **27 min** of video per task attempt.
- Videos longer than ~27 min may time out. The coverage check in `handleVideoAnalysisTwoPass` detects incomplete splits (compares `MAX(end_secs)` from `chunk_analysis_results` against probed video duration) and re-triggers `splitAndAnalyzeChunks` on retry.

### Idempotent skip
- `chunkAlreadyAnalyzed(sessionID, startSecs)` checks for existing `COMPLETED` records.
- On retry after a partial run, already-analyzed chunks are skipped, so only the remaining chunks are processed.
- This makes the entire split flow resumable across task retries/worker restarts.
