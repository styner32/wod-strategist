# Video Analysis Memory

For the local SDK v1.71 Static/Agentic experiment, see
[`video-mode-comparison.md`](video-mode-comparison.md). Production processing
modes and the 5 FPS segment-analysis path are unchanged.

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
current implementation keeps capture offsets and separately probed cumulative
media offsets; remaining manifest/coverage work must preserve this separation for merged-video analysis, subtitles, highlights, and playback.

## Architecture

### Original chunk failure recovery (2026-09-11)

- Both original chunk handlers persist terminal results through
  `persistChunkAnalysisResult`. A PostgreSQL transaction advisory lock keyed by
  `(profile_id, session_id, file_path)` serializes insert/update, including an
  absent row. A late success updates a failure while retaining its ID and verified
  media offsets; terminal failure cannot overwrite success. No schema change is
  required. Existing duplicate rows remain for feedback references, but merge
  includes each GCS URI once. Other writers must use the same helper for original
  chunks; this does not make server-split/full-analysis writes idempotent.
- Final status write failures return a retryable error, including after the
  analysis retry budget is exhausted. `SkipRetry` follows a successful failure
  write, not a failed DB write.
- The production worker wires a real `QueueInspector` to Redis DB 5. Missing
  chunk results trigger inspection of the default queue's archived, pending,
  scheduled, retry, and active tasks. Live matching tasks keep merge waiting,
  regardless of merge retry count. Archived task payloads restore capture times.
- `merge_requested_at` is set when creating a merge task. A chunk absent from
  both DB terminal results and the queue is eligible for orphan recovery after
  35 minutes from that time. Old payloads fall back to GCS object `Created`.
  Queue/storage inspection failures remain retryable; they do not prove failure.
- Merge orders by capture timestamps when all are present. Missing timestamps
  allow only unambiguous numeric `chunk_<number>.mp4/.mov` ordering. Untimed native
  UUID filenames cause a persisted session failure instead of a guessed video
  order. This remains a legacy fallback, not an authoritative upload manifest.

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

For standard WODs, current code removes walking/rest/setup rows, retains `Unknown` for deeper
visual revalidation, and merges only touching segments with the same free-text
movement. Recovery workouts retain low-motion/rest/setup chunks and exclude explicit walking; see [recovery-and-mobility.md](recovery-and-mobility.md). A filtered rest gap is never merged into the surrounding movement.

Detailed analysis no longer adds observation windows between indexed sets
(`addBetweenObservationWindows` was removed 2026-10-19), so no rest between sets
is directly observed. Verified media of chunks that chunk analysis explicitly
classified as non-exercise (`activity_state` walking/rest_setup/not_exercise, or
a non-empty non-exercise movement such as `Rest`; recovery: walking only) is
recorded as a `skipped_non_exercise` media gap (`skippedNonExerciseGaps`). Media
nobody classified or observed (unlabelled chunks, missing media offsets, sets the
indexer missed) is filled as `not_analyzed` and keeps interior coverage incomplete.
Prefix waiting and post-workout tail are not automatically classified. Budget
omissions and failed windows remain explicitly unassessed. Production warmup
triage reserves early/late observations of repeated movements when the segment
budget permits a pair.

Fallback:
- if no usable verified chunk segments remain after split/recovery attempts, use model-based indexing
- inject real video duration into the prompt
- use high media resolution
- post-filter timestamps beyond real duration

## Pass 2: segment analysis
Analyze each segment independently with `VideoMetadata` offsets.
Constrain analysis to the exact time range.

- `NewVideoAnalysisTask` sets an explicit 1-hour Asynq timeout (`videoAnalysisTaskTimeout = time.Hour`).
- Pass 2 analyzes segments concurrently against the uploaded full-video URI (`maxConcurrentSegmentAnalyses = 5`), then aggregates token usage, `wodEvidence`, `workout_progress` intervals/gaps, highlights, and markdown output in chronological segment index order (`0..len(segments)-1`).
- Segments with `Type == "Unknown"` (case-insensitive, including `Unknown` chunk windows) route to `gemini-3.8-flash` with `HIGH` thinking (`AnalyzeSegmentWithModel(..., gemini.ModelFlash38)`); non-`Unknown` exercise segments use the client's Pro model (`gemini-4-argon`, `HIGH` thinking).
- `GET /api/v1/sessions/:session_id/cost` returns `total_duration_ms` and `by_stage` (`[]StageDurationItem`) aggregated from `pipeline_stage_metrics` so the web UI (`SessionCostCard` and `SessionDetailPage`) can display total and per-stage analysis duration.

Current-window observation includes time-bounded complete-cycle/form/ROM/pace
evidence in `workout_observations`. Do not send history, previous assessments,
session fatigue aggregates, or score requests to any segment, including the last.
`workout_progress` assembles these observations before a separate text-only final
assessment. WOD version-1 eligibility alone controls onset trimming and version-1
scores in production; a new timeline never upgrades historical sessions. Only an
explicitly applied version-1 whole-session reanalysis promotes one (see
[`wod-training-load.md`](wod-training-load.md)). Warmup progression
is evaluated at final assessment, preserving controlled early/late ROM changes.
See [`workout-progress.md`](workout-progress.md) for storage, confirmation and
the one-way observation/timeline/assessment boundary.

Argon and Flash use the same observation-only prompt. `ObservationOnlyRulesPrompt`
and the final `ObservationOnlyOutputCheckPrompt` apply to prose, coaching,
highlight reasons and every form/ROM/pace/rep JSON evidence string. Record visible
positions, trajectories, alignment, ranges and timed pauses; do not infer fatigue
presence/onset/cause, strength/power decline, muscle weakness/activation,
abdominal pressure or recovery, including hedged claims. Missing evidence does
not establish no fatigue or maintained strength. Ground reaction/force-transfer
efficiency and breathing intent are also interpretations, not direct video facts.
Keep complete timed repetitions when removing interpretation. Inspect the entire
claimed rest range for restarted exercise; missing frames are unassessed, not rest.
Agentic highlight and injury-focused supplementary video inspection use the same boundary.
Mobility `limited_*` labels describe the current movement only; new observer
output excludes muscle-tightness/stiffness labels while parsers retain historical
compatibility. Debug prompt versions are `chunk-debug-observations-v2` and
`session-independent-observations-v3`; enrichment is `highlight-observation-v2`.

## File lifecycle
Upload once and reuse the same Gemini file URI across passes.

Typical lifecycle:
- upload
- active polling
- index/analyze
- delete

Cleanup rule:
- on two-pass failure: video analysis attempts deletion
- injuries exist: the optimized injury path can reuse the file but does not guarantee deletion; final-owner cleanup remains COST-07 work
- successful no-injury analysis currently retains file metadata and relies on
  Files API expiration; bounded explicit cleanup is part of COST-07 in the
  remediation plan

## `chunk_analysis_results` schema

| Column | Type | Purpose |
|---|---|---|
| `session_id` | TEXT | Links chunks to a session |
| `file_path` | TEXT | GCS URI of the individual chunk video |
| `exercise_type` | TEXT | Detected movement (e.g. "Snatch", "Pull-up") |
| `start_secs` | REAL | Legacy start: mobile capture-clock offset or server-split source offset; not a universal merged-media contract |
| `end_secs` | REAL | Legacy end with the same source-dependent meaning as `start_secs` |
| `media_start_secs` | DOUBLE PRECISION | Verified media-relative start offset; nullable for legacy/unverified rows |
| `media_end_secs` | DOUBLE PRECISION | Verified media-relative end offset; nullable for legacy/unverified rows |
| `output` | TEXT | Short coaching feedback from chunk analysis |
| `status` | TEXT | PENDING / COMPLETED / FAILED |

Do not interpret app-recorded capture offsets as merged-media truth. Debug
playback and re-analysis may use `media_start_secs`/`media_end_secs` only after
they were written from server-split offsets, probed concat durations, or a
verified legacy reconstruction.

Highlight confidence filtering uses each non-null, increasing `media_*`
interval independently. Equal capture/media endpoints are valid: migration
`000038` backfilled only `split_chunk` rows, leaving unmapped mobile rows NULL.
Do not disable the filter based on session drift or missing capture offsets.

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

- Explicit selected-video reanalysis is available to authenticated owners,
  including legacy and warmup sessions. The legacy `ENABLE_SESSION_REANALYSIS`
  setting no longer blocks these owned requests; source readiness, quota and
  active-run checks still apply. Chunk debugging has its separate switch.
- Queue task: `session:debug-reanalysis`; its payload contains only `run_id`.
- Persistence: `session_reanalysis_runs`. Candidate generation never replaces the unique
  production `analysis_results` row and never enqueues highlights, hardsubs,
  subtitles, injury analysis, or TTS. The separate explicit `/apply` action can replace the production result (see below).
- Owner APIs are `POST /sessions/:session_id/reanalyses`,
  `GET /sessions/:session_id/reanalyses`, and
  `GET /sessions/:session_id/reanalyses/:run_id`. POST requires
  `client_request_id` and accepts optional `appearance_hints`, `model`, `wod_description`, and `wod_start_secs` (`session_reanalysis_dto.go`). `wod_start_secs` is allowed for all WOD sessions (including legacy `wod_evaluation_version == 0` sessions). It enforces one active run per session, blocks while any
  chunk debug run is active, and limits each profile to 20 runs per rolling 24h (`sessionReanalysisDailyQuota = 20`). `SESSION_REANALYSIS_DAILY_QUOTA` overrides it; `config.InitServer` rejects a non-positive or non-integer value at startup and passes it as `controllers.Config.SessionReanalysisDailyQuota` (0 = default).
- The API resolves and snapshots the exact GCS session source. The worker may
  reuse a still-valid Gemini Files object only from a chunk or session debug run
  with the same `source_gcs_uri`; an authoritative source duration and local `.mp4` copy are
  prepared so segment analysis can run via Frame-Grid contact sheets (`AnalyzeVideoIntervalWithFrameGrids`).
- Segment resolution order:
  1. Verified `media_start_secs`/`media_end_secs` on `chunk_analysis_results` (`buildSessionDebugSegments`). When a completed original chunk's object has no verified interval on any row (`sessionDebugTimelineIncomplete`), `reconstructSessionDebugMediaIntervals` rebuilds the **whole** concat timeline with `retainedChunkTimeline` (shared with chunk debug). Each retained URI is probed once (duplicate rows share it, like merge). Any unavailable object writes nothing, and rebuilt offsets that disagree with verified ones by >0.05s abort. Unmapped chunks stay unobserved (`not_analyzed`). Duplicate rows are observed once; corrections on any duplicate row apply.
  2. Only when no chunk has a verified interval: stored `analysis_results.workout_progress` intervals (`storedProgressSegments`), and only if `MediaSourceID` equals `progressMediaSource` of this run's Files URI or local file. `analysis_results.output` headers are never parsed: legacy headers can carry capture-clock offsets.
  3. `IndexVideo`, filtered to the source duration before `filterExerciseAndUnknownSegments(segments, workoutType)` merges.
- Selection keeps exercise and `Unknown` segments (`Unknown` routed to `gemini-3.8-flash`; an unlabelled index segment becomes `Unknown`). Explicit non-exercise (`[NO_EXERCISE]`, `Rest`, `Walking`, `Setup`) is skipped and recorded as `skipped_non_exercise`; recovery workouts keep rest/setup and skip only walking (same as `includeChunkInDeepAnalysis`). No between-set `Unknown` windows are inserted and `maxSegmentsForDuration` does not truncate session reanalysis.
- Completed-segment checkpoints and terminal/retry state writes use `sessionDebugPersistContext` (`context.WithoutCancel` + 30s timeout), so a task deadline cannot leave a run `RUNNING` or lose finished segments; retries reuse checkpointed segments.
- Only the latest active, non-retracted structured chunk corrections are
  included. Free-text notes and unconfirmed re-analysis candidates are never
  prompt context. Corrections carry `media_start_secs`/`media_end_secs`, remain
  non-authoritative labels, and must be revalidated from visible evidence.
- Movement/activity/time corrections are non-authoritative hints for independent
  video observers. Fatigue/evaluation corrections go only to the separate final
  evaluator and cannot create or suppress original visual observations. Final
  corrections never alter the new `workout_progress` source or repetitions.
- Status values are `QUEUED`, `RUNNING`, `COMPLETED`, `FAILED`,
  `VIDEO_UNAVAILABLE`, and `CONTEXT_UNAVAILABLE`.

## Highlight playback-event contract (v2)

- `analysis_results.highlight_segments` remains a JSON-encoded array; no schema
  migration or bulk backfill is required. New entries set `version: 2` and keep
  the legacy parent fields `start`, `end`, `type`, `movement`, and `reason`.
- A parent is a playback event. Its `observations` keep exact visual-evidence
  ranges. New observer prompts emit `positive_form`, `form_issue`, or
  `technique_event`, with an optional `confidence`; pauses/tempo changes are
  technique events, not fatigue diagnoses. Historical `fatigue_onset` input is
  still accepted by the normalizer. Never lengthen an exact
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
- There is no measured supported video-length limit recorded here. A previous estimate of ~27 minutes incorrectly treated concurrency 10 as serial processing.
- Illustrative split-only arithmetic: `ceil(chunk_count / 10) × per_chunk_seconds`; this excludes splitting, upload/poll variance, rate limits, and sequential deep calls. It cannot establish an end-to-end timeout guarantee.
- The current completeness heuristic compares `MAX(end_secs)` with probed duration. It can miss earlier gaps and still uses legacy offsets; replacing it with successful media-interval union coverage remains REL-07.

### Idempotent skip
- `chunkAlreadyAnalyzed(sessionID, startSecs)` checks for existing `COMPLETED` records.
- On retry after a partial run, already-analyzed chunks are skipped, so only the remaining chunks are processed.
- This skips some repeated work after retries, but floating-point start equality and non-idempotent writes do not guarantee full resumability; REL-06/REL-07 remain open.

## Gemini Model & Thinking Configuration
- Worker and bare-client deep video analysis default: `gemini-4-argon` (2026-10-14). General session segments, chunk/session reanalysis, injury follow-ups, and legacy whole-video analysis inherit it.
- Config env vars:
  - `GEMINI_MODEL`: Deep video model name (default `gemini-4-argon`). Set an explicit old model ID to roll back; existing Terraform variable overrides take precedence over defaults.
  - `GEMINI_THINKING_LEVEL`: Full video / deep segment analysis thinking level (default `HIGH`). Valid: `HIGH`, `MEDIUM`, `LOW`.
  - `GEMINI_THINKING_CHUNK`: Short video (~10s chunk) analysis thinking level (default `LOW`). Valid: `HIGH`, `MEDIUM`, `LOW`. Minimizes response latency and thinking token costs for real-time coaching feedback.
  - `GEMINI_THINKING_BUDGET`: Optional integer thinking budget in tokens; omitted from Argon requests even when set.
- Stage-specific thinking levels:
  - Short video / chunk analysis: Uses `AnalyzeChunkVideo`, which resolves to `GEMINI_THINKING_CHUNK` (`LOW` by default). Note that `MINIMAL` is not supported on Gemini 3.8 / 3.7 Flash and will be rejected.
  - Full video / deep segment analysis: Uses `AnalyzeSegmentWithModel` / `AnalyzeVideo`, which resolves to `GEMINI_THINKING_LEVEL` (`HIGH` by default) to maximize accuracy and temporal grounding.
- Thinking config supports Argon plus names containing `3.8`, `3.7`, or `2.5`. Argon defaults to `HIGH` even with bare-client options. Legacy `gemini-3.1-pro-preview` keeps its existing request behavior.
- Argon `GenerateContent` calls have `maxOutputTokens=32768` and a 20-minute context deadline, bounded by any earlier caller/task deadline. SDK remains `google.golang.org/genai v1.71.0`; no automatic continuation. `MAX_TOKENS` or a `continuationToken` returns an incomplete-output error with retained usage before any analysis parsing.
- Reanalysis accepts `argon`, `argon4`, `gemini-4-argon`, `pro`, and `gemini-pro` as Argon. `pro31` and `gemini-3.1-pro-preview` explicitly select the old model. Stored historical model IDs are not rewritten.

Short chunks, indexing, video count review, real-time feedback, image/text parsing, WOD evaluation/summary, and Agentic highlights continue to use Flash explicitly. Web reanalysis omits the model and inherits the worker default; chunk creation does not accept a model field, while whole-session creation does. No UI/API schema or DB migration is required.

Deployment acceptance still requires matched, annotated-video comparison of Argon HIGH against the previous model for evidence quality, counts/timestamps, complete output, latency, and tokens. Transport tests do not establish model accuracy or deployed settings.

## Whole-Workout Re-Analysis & Apply Workflow
- **Re-analysis with custom WOD description**: When an athlete updates their workout description or appearance hints in the web app, a new `session_reanalysis_runs` record is created with `wod_description`.
- **Worker Execution**: `session_debug_reanalysis.go` overrides the target WOD description with the run's `wod_description` for both indexing and segment prompts.
- **Candidate Apply**: `POST /api/v1/sessions/:session_id/reanalyses/:run_id/apply` atomically updates `analysis_results` (output, session_score, highlight_segments, wod_description, status) and `sessions` (`wod_description`, `workout_type`) using an atomic DB transaction.

### Web status polling (2026-10-01)
- `web/src/history/historyPolling.ts` bounds each pending work identity to 40 successful refreshes (4 for provisional activity). `dataUpdateCount` is cumulative cache state: subtract the baseline for the current run IDs/source, never use its absolute value as the limit. QUEUED -> RUNNING retains the budget; new IDs, terminal completion, or explicit status refresh reset it.
- The UI displays paused polling and offers status-only resumption without creating another AI request. Reset the relevant budgets before invalidating after a newly submitted job. Preserve background polling suppression and capped backoff (8 seconds for reanalysis, 10 seconds for enrichment/activity).

## AI Cost & Token Tracking
- Repository estimate constants in `internal/cost/cost.go` (not a verified current vendor quote):
  - `gemini-3.8-flash`: $1.50 / 1M prompt tokens, $9.00 / 1M candidate tokens.
  - `gemini-3.5-flash-lite`: $0.25 / 1M prompt tokens, $1.50 / 1M candidate tokens.
  - `gemini-3.1-pro-preview`: $1.25 / 1M prompt tokens, $5.00 / 1M candidate tokens.
  - `gemini-4-argon`: Standard $2/$10 per 1M input/output through 2026-11-30; $4/$20 from `2026-12-01T00:00:00Z`. Use each ledger row's `created_at`, not the query date.
  - Exchange rate: 1,380 KRW / USD.
- Calculation includes prompt + tool input and candidate + thinking output with the fixed exchange rate. Argon dates are preserved in account aggregation by splitting the two pricing periods. Other configured model rates are unchanged. Cache discounts, service tiers, and invoice reconciliation remain outside these estimates; see [token-usage.md](token-usage.md).
- Endpoints:
  - `GET /api/v1/sessions/:session_id/cost`: Cost breakdown for a single session.
  - `GET /api/v1/analytics/cost`: Cumulative token/cost totals for owned profiles (optionally one `profile_id`); task/model breakdown arrays are on the session-cost response only.

## Movement Catalog Matching & Unassessed Movement Preservation
- `movement.NormalizeKey(raw)` normalizes `&` to ` and ` (in addition to trimming, lowercasing, converting hyphens to spaces, and collapsing whitespace). This ensures `Clean and Jerk` matches `Clean & Jerk`.
- `movement.Canonical(raw)` maps raw exercise names, common aliases (e.g. `Rowing` -> `Row`, `Double Unders` -> `Double-under`, `DU` -> `Double-under`, `C&J` -> `Clean & Jerk`), and regular English plurals (`-es`, `-s`) to the official catalog name. `activity.CanonicalMovement` delegates to `movement.Canonical`.
- Unspecified variants/implements (`Snatch`, `Muscle-up`, `Dip`, `Squat`, generic press names, `Jump Rope`, `GHD`, including plurals) remain unconfirmed; never infer Power, Bar, Ring, Air, Strict, or Single-under from these names. Preserve the raw name in unassessed evidence. `fatigue.GetMovementWeights` may use an existing exact generic weight (`snatch`), but `movement.IsAmbiguous` blocks subtype substring fallback for the other unspecified names.
- Jump rope cycle observation (`Double-under`, `Single-under`): The movement observation prompt instructs Gemini to count each observable takeoff-to-landing jump cycle as 1 rep cycle, rather than attempting to isolate high-speed rope rotations or dropping them into `unassessed`.
- Unassessed intervals (`activity.Interval` and `activity.Gap`) include a `Movement string `json:"movement,omitempty"`` field.
- When an interval is unassessed due to `unassessable_target_or_activity`, `unsupported_movement_or_unit`, `incomplete_cycle`, or `analysis_incomplete`:
  - `Decode` extracts the detected movement from events or unassessed entries.
  - `Build` falls back to `row.ExerciseType` if the observation's movement field is empty.
  - The UI (`ActivitySummaryPanel` and `ActivitySummaryCard`) displays the recognized movement next to the gap reason (e.g. `The athlete or movement is unclear (Clean and Jerk)`).

## Analysis Value Reuse, Bounded Parallelism, and Batch API (2026-10-14)
- **Previous-value & checkpoint reuse**:
  - `detectWODStart` (`wod_evaluation.go`): When `len(request) == 0`, reuses `s.WODStart` from `analysis_results.session_score` if `s.WODStart.Status == "confirmed"` and either `s.WODStart.Source == "manual"` or `startCoveredBySegment(*s.WODStart.Seconds, segments)` is true.
  - `normalizeWorkout` (`normalize_workout.go`): Reuses `existing.NormalizedWorkout` from `analysis_results` without calling Gemini when `strings.TrimSpace(existing.WODDescription) == strings.TrimSpace(wodDescription)` and `NormalizedWorkout` is non-empty (`<> ''` and `<> '[]'`).
  - `chunk_debug_reanalysis.go`: Supports `reuse_previous: true` in `SourceContextSnapshot` (reusing a matching `COMPLETED` `ChunkReanalysisRun` with identical `(chunk_analysis_result_id, model, source_gcs_uri, media_start_secs, media_end_secs, schema_version)` and a `prompt_hash` of either analysis mode, `chunkDebugPromptHash`) and checks both `ChunkReanalysisRun` and `SessionReanalysisRun` when reusing active Gemini Files uploads.
  - `session_debug_reanalysis.go`: Filters prompt corrections per segment (`correctionsForSegment`) and caches/reuses completed segments by `sha256(model|mode|start|end|prompt)` (analysis mode: `frame-grid-v1`/`video`) across both intra-run retries (`run.SourceContextSnapshot["completed_segments"]`) and prior `COMPLETED` `SessionReanalysisRun` rows for the same `(session_id, profile_id, source_gcs_uri)`.
- **Bounded parallelism (`maxConcurrentSegmentAnalyses = 5`)**:
  - Applied across `video_analysis.go` (Pass 2), `session_debug_reanalysis.go` (Pass 2 + `Unknown` routing to `gemini-3.8-flash`), `activity_review.go` (rows loop with mutex-protected DB checkpointing), `injury_analysis.go` (intervals loop), and `generate_hardsub.go` (TTS chunks loop).
- **Batch chunk re-analysis API**:
  - `POST /api/v1/sessions/:session_id/chunks/batch-reanalyses` (`CreateBatchChunkReanalysis`) accepts `chunk_ids` (1..50), `client_request_id`, and optional `reuse_previous` (default `true`), enforcing profile quota and enqueueing `chunk:debug-reanalysis` tasks in one request.
- **Gemini Batch API & 50% cost discount**:
  - `Client.AnalyzeSegmentsBatch` (`internal/gemini/client.go`) submits `InlinedRequests` via `c.client.Batches.Create` with exact fractional `videoMetadata` offsets (`replaceBatchVideoMetadataOffsets` in `exactSegmentOffsetsTransport`) and polls `c.client.Batches.Get`.
  - `cost.IsBatchTaskType` (`internal/cost/cost.go`) applies a 50% discount (`BatchDiscountMultiplier = 0.5`) to any `token_usages.task_type` ending in `:batch` (e.g., `video:analysis:batch`, `session:reanalysis:batch`).

## Frame-Grid Contact Sheet Reanalysis (2026-10-19)
- **FFmpeg + Go Contact Sheet Generation (`internal/worker/frame_grid.go`)**:
  - `GenerateFrameGrids(ctx, videoPath, start, end, opts)`: Extracts frames via FFmpeg (`-vf fps=...,scale=...`), burns top-left `HH:MM:SS.mmm` timestamps (`inconsolata.Bold8x16` on semi-transparent dark badge) in pure Go (no `libfreetype`/`drawtext` requirement), and tiles into `[]FrameGridSheet` JPEG contact sheets.
  - Default `FrameGridOptions`: `FPS=4.0`, `Cols=8`, `Rows=4` (32 frames / 8s per sheet), `MaxSheets=12` (384 frames max per call), `TileWidth=240`, `JPEGQuality=85`, `defaultFrameGridBorderPx=2`. Intervals exceeding 32 frames automatically split into multiple chronological sheets; `GenerateFrameGrids` itself can scale `FPS` down to `minFrameGridFPS=0.25` and subsample.
  - Reanalysis uses frame grids only when `frameGridEligible`: the interval fits the sheet budget at `minReanalysisFrameGridFPS = 2.0` or faster (≤192s at defaults). `AnalyzeVideoIntervalWithFrameGrids` returns `errFrameGridIntervalTooLong` with no sheets otherwise, and callers use `AnalyzeSegmentWithModel` (VideoMetadata). At most `maxConcurrentFrameGridExtractions = 2` extractions decode frames at once.
  - `TimestampOffset *time.Duration`: Optional base timestamp override when extracting `[0, duration]` from a standalone chunk file that maps to a known merged-media offset.
- **Gemini Inline Frame-Grid Analysis (`internal/gemini/client.go`)**:
  - `Client.AnalyzeFrameGrids` and `Client.AnalyzeFrameGridsWithModel(ctx, images, mimeType, prompt, model)` send one or more contact-sheet images inline (`InlineData`) with `MediaResolutionHigh` and configured thinking (`HIGH` default), excluding `Thought` parts from the returned text. The worker `GeminiClient` interface exposes only `AnalyzeFrameGridsWithModel`.
  - `(w *Worker) AnalyzeVideoIntervalWithFrameGrids(ctx, videoPath, start, end, prompt, model, opts)` combines `GenerateFrameGrids`, frame-grid reading prompt instructions, and `GeminiClient.AnalyzeFrameGridsWithModel`.
  - Both `HandleSessionDebugReanalysisTask` and `HandleChunkDebugReanalysisTask` retain the downloaded local `.mp4` for the duration of the task and analyze eligible segments/chunks via `AnalyzeVideoIntervalWithFrameGrids` (with fallback to `AnalyzeSegmentWithModel` if a local video file is unavailable, the interval is too long, or FFmpeg cannot decode it). Session reanalysis always has local media (the source is downloaded to probe its duration). A chunk run on a session video that reuses a cached Files object has no local media and deliberately uses VideoMetadata, avoiding a second download of the whole session video.
  - The analysis mode (`debugAnalysisModeFrameGrid = "frame-grid-v1"` or `debugAnalysisModeVideo = "video"`) is part of the reuse identity: `sessionDebugSegmentCacheKey(model, mode, start, end, prompt)` and `chunkDebugPromptHash(prompt, mode)` (stored as `prompt_hash`, with the mode actually used). `reuse_previous` accepts a completed candidate of either mode and keeps that candidate's own `prompt_hash`.
