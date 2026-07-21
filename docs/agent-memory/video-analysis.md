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

## Whole-session debug re-analysis

- Feature flag: `ENABLE_SESSION_REANALYSIS=true` on the API server. It is
  independent from `ENABLE_CHUNK_REANALYSIS` and disabled by default.
- Queue task: `session:debug-reanalysis`; its payload contains only `run_id`.
- Persistence: `session_reanalysis_runs`. Candidates never replace the unique
  production `analysis_results` row and never enqueue highlights, hardsubs,
  subtitles, injury analysis, or TTS.
- Owner APIs are `POST /sessions/:session_id/reanalyses`,
  `GET /sessions/:session_id/reanalyses`, and
  `GET /sessions/:session_id/reanalyses/:run_id`. POST accepts only
  `client_request_id`, enforces one active run per session, blocks while any
  chunk debug run is active, and limits each profile to 5 runs per rolling 24h.
- The API resolves and snapshots the exact GCS session source. The worker may
  reuse a still-valid Gemini Files object only from a chunk or session debug run
  with the same `source_gcs_uri`; an authoritative source duration is still
  probed before reuse.
- Only the latest active, non-retracted structured chunk corrections are
  included. Free-text notes and unconfirmed re-analysis candidates are never
  prompt context. Corrections carry `media_start_secs`/`media_end_secs`, remain
  non-authoritative labels, and must be revalidated from visible evidence.
- A saved `fatigued` value cannot create visual fatigue evidence. Saved
  `not_fatigued`, `walking_rest`, or non-exercise activity suppresses conflicting
  original fatigue aggregation for that debug run.
- Status values are `QUEUED`, `RUNNING`, `COMPLETED`, `FAILED`,
  `VIDEO_UNAVAILABLE`, and `CONTEXT_UNAVAILABLE`.

## Highlight playback-event contract (v2)

- `analysis_results.highlight_segments` remains a JSON-encoded array; no schema
  migration or bulk backfill is required. New entries set `version: 2` and keep
  the legacy parent fields `start`, `end`, `type`, `movement`, and `reason`.
- A parent is a playback event. Its `observations` keep exact visual-evidence
  ranges and use `positive_form`, `form_issue`, `fatigue_onset`, or
  `technique_event`, with an optional `confidence`. Never lengthen an exact
  observation merely to meet the playback duration.
- Merge observations for the same movement when they overlap or are at most
  `1.5` seconds apart. Pad the parent symmetrically to at least `5` seconds,
  bounded by its analyzed media segment and full video. Split a chain longer
  than `20` seconds at the largest observation gap, recursively if necessary.
- Rest, setup/preparation, `Unknown`, a movement change, or an intervening
  source segment is a hard merge boundary. Different movements may share a
  parent only when the source analysis segment explicitly names a compound
  movement.
- Parent type is derived from retained observations: positive only is
  `best_form`, form issue is `worst_form`, fatigue only is `fatigue_point`,
  positive plus issue/fatigue is `mixed_form`, and standalone technique is
  `key_moment`. A technique observation merged with evaluation evidence adds
  the `key_moment` tag instead of creating a duplicate card.
- Select at most one positive, one improvement/fatigue, and one independent
  technique event per normalized movement (maximum three parent cards).
- Treat legacy flat arrays as input to the same deterministic normalizer at API
  read, verification, and reel-generation boundaries. Preserve unknown legacy
  shapes rather than silently erasing them. Normalizing v2 output must be
  idempotent.
- Some old API rows have no stored video duration. For those read-only responses,
  never pad past the last exact observation; use preceding context and allow a
  shorter parent near media start. Verification and reel generation must instead
  use the processed/probed video duration and apply normal bounded padding.
- Verification operates on each exact observation. After rejected or omitted
  observations are removed, recompute the parent type, tag, summary, and padded
  range. A parent with no retained observations is removed.
- Reel generation cuts each selected parent once from
  `videos/{profileId}/{sessionId}/merged.mp4`, then reuses that clip across Full,
  Best, Improvement, and Key outputs. Use only merged-media timestamps; never
  apply capture-clock chunk offsets to the merged video.

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
