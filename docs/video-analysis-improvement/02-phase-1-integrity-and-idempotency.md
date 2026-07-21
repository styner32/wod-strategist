# Phase 1: Integrity and Idempotency

Priority: P0
Goal: make the bytes, ordering, timestamps, task routing, retries, and visible status trustworthy before optimizing model behavior.

Implement this phase as four ordered PRs. Do not combine it with model changes, prompt rewrites, GCS retention, or infrastructure resizing.

## Target invariants

At the end of Phase 1:

1. Every captured chunk has a stable zero-based `chunk_index`.
2. The final partial chunk follows the same compression/upload/notification path as all earlier chunks.
3. Mobile finalization calls merge only after all intended chunk uploads were acknowledged.
4. The server knows `expected_chunk_count` and never infers completeness from a point-in-time object listing.
5. Capture wall-clock offsets and merged-media offsets are distinct fields.
6. Merge and full analysis order by `chunk_index` and use probed media durations.
7. Duplicate HTTP notifications, Asynq delivery, or worker restarts cannot create duplicate logical chunks or stale full results.
8. `FAILED` means terminal failure; `PARTIAL` means usable output with incomplete required evidence; transient retry states are not terminal.

## PR 1A — Reliable capture finalization and manifest

Work items: REL-01, REL-02

### Files to inspect/change

- `app/workout/visionTestPage.tsx`
- `features/wod/api.ts`
- `features/wod/api.test.ts`
- A narrow `features/wod/chunkFinalization.ts` helper and matching test are recommended if native screen state prevents deterministic unit tests; keep it specific to this workflow
- `api/internal/controllers/dto.go`
- `api/internal/controllers/handlers.go` (move touched video handlers to a domain file if required by `api/AGENTS.md`)
- `api/internal/controllers/analysis_test.go` and route integration tests
- `api/internal/worker/worker.go`
- `api/internal/worker/chunk_analysis.go`
- `api/internal/worker/merge_chunks.go`
- `api/internal/worker/merge_chunks_test.go`
- `api/internal/db/db.go`
- New migration pair in `api/internal/db/migrations/`

### REL-01 — Upload the final partial chunk and await all uploads

#### Mobile behavior contract

Refactor the callback so `isRecordingChunks.current` controls only whether another recording starts. It must not decide whether the just-finished file uploads.

Required behavior:

1. Assign `chunkIndex` when a recording starts, not when an asynchronous upload finishes.
2. For every successful `onRecordingFinished`, including stop-triggered final clips:
   - append the local path;
   - calculate capture offsets;
   - compress if configured;
   - upload;
   - notify `/chunk-complete` with that same index.
3. Do not start a next recording after stop.
4. Track a promise that covers the complete chain: compression, signed upload, and `/chunk-complete` acknowledgement. Tracking only the PUT request is insufficient.
5. At stop, wait for the final callback and then await all scheduled upload-chain promises with `Promise.allSettled`.
6. If any chain failed, do not call `/merge-chunks`. Keep local files and present a retryable finalization error. A console log is not sufficient.
7. Compute `expected_chunk_count` from the set of scheduled chunk indices, not React `chunkCount` state, because state updates are asynchronous.
8. After all acknowledgements succeed, call merge immediately. Remove the fixed delay as a correctness mechanism.

Recommended component-local refs:

- `nextChunkIndexRef`: next zero-based index assigned at capture start.
- `uploadCompletionsRef`: map from index to the complete upload-chain promise.
- `uploadFailuresRef`: map from index to the last error for retry UX.
- A distinct `isFinalizingRef`; do not overload `isRecordingChunks` with upload semantics.

The exact implementation may preserve serial and concurrent modes, but both modes must expose completion to finalization. `drainUploadQueue` must not swallow errors before the per-index promise is settled.

#### API contract additions

Add these fields:

```go
// ChunkCompleteRequest
ChunkIndex *int `json:"chunk_index,omitempty"` // pointer: zero is a valid index; nil identifies a legacy client

// MergeChunksRequest
ExpectedChunkCount *int `json:"expected_chunk_count,omitempty"` // required for new mobile clients
```

Propagate both fields through the corresponding TypeScript request types and `VideoAnalysisPayload`/task constructors.

Compatibility rules:

- New app versions must always send both values.
- Accept omitted values temporarily for legacy clients and emit a metric/log field `legacy_chunk_manifest=true`.
- Never silently treat an explicit `expected_chunk_count <= 0` as valid.
- After the supported mobile upgrade window, make `expected_chunk_count` mandatory in a separate cleanup PR.

#### Tests

Add tests for:

- stop at 3.2 seconds: index 0 uploads and merge receives count 1;
- stop at 23.2 seconds: indices 0, 1, 2 upload, including the final partial clip;
- compression finishes after stop: merge waits for it;
- serial queue has two pending tasks: merge waits for both;
- concurrent upload failure: merge is not called and retry state is shown;
- chunk index 0 survives JSON encode/decode (guards against an `omitempty` integer bug);
- merge request rejects an explicit zero/negative expected count.

Use Jest for pure mobile/API behavior. Do not attempt to instantiate the native camera in unit tests; extract only the smallest scheduling/finalization state machine if needed.

### REL-02 — Make the chunk manifest authoritative

#### Additive schema

Create a migration pair. Do not rename or remove current columns in this phase.

Add to `chunk_analysis_results`:

| Column | Type | Initial/default behavior |
|---|---|---|
| `chunk_index` | `INTEGER NULL` | New clients set it; legacy rows remain null. |
| `chunk_source` | `TEXT NOT NULL DEFAULT 'recorded'` | `recorded`, `synthetic_split`, or later `synthetic_range`; backfill existing `split_chunk_` rows to `synthetic_split`. |
| `capture_start_secs` | `DOUBLE PRECISION NULL` | Copy new request `start_secs`; backfill from existing `start_secs`. |
| `capture_end_secs` | `DOUBLE PRECISION NULL` | Copy new request `end_secs`; backfill from existing `end_secs`. |
| `media_duration_secs` | `DOUBLE PRECISION NULL` | Set from FFprobe, never from wall clock. |
| `media_start_secs` | `DOUBLE PRECISION NULL` | Set during manifest/merge timeline construction. |
| `media_end_secs` | `DOUBLE PRECISION NULL` | Set during manifest/merge timeline construction. |

Add indexes in migration SQL, not GORM tags:

```sql
CREATE UNIQUE INDEX ...
ON chunk_analysis_results(profile_id, session_id, chunk_index)
WHERE chunk_index IS NOT NULL;

CREATE UNIQUE INDEX ...
ON chunk_analysis_results(profile_id, session_id, file_path)
WHERE file_path <> '' AND chunk_source = 'recorded';

CREATE INDEX ...
ON chunk_analysis_results(profile_id, session_id, status, chunk_index);
```

Before creating unique indexes, deterministically deduplicate existing identical recorded file paths. Retain one row using this precedence: `COMPLETED` over `PROCESSING` over `PENDING` over `FAILED`, then newest `updated_at`, then highest `id`. Exclude empty `file_path` rows and synthetic sources from this cleanup and partial index. Put the cleanup SQL in the migration so every environment applies the same rule.

Update `db.ChunkAnalysisResult` with nullable/pointer fields matching the migration. Constraints remain migration-owned.

#### Persist a manifest row before enqueue

In `Controller.ChunkComplete`:

1. Validate `chunk_index >= 0` when present.
2. Verify profile ownership and URI format as today.
3. Upsert the logical chunk row keyed by `(profile_id, session_id, chunk_index)` for new clients, or `(profile_id, session_id, file_path)` for legacy clients.
4. Set `chunk_source='recorded'`, `file_path`, capture offsets, heart rate, client confidence, and status `PENDING` without overwriting a prior `COMPLETED` result.
5. Enqueue analysis only when the row is not already COMPLETED.
6. If enqueue fails, return an error but retain PENDING. A client retry then resumes the same logical row.

Do not insert a second row from the worker. Worker handlers must update/upsert this manifest row.

#### Merge validation algorithm

For requests with `expected_chunk_count=N`, `HandleMergeChunksTask` must:

1. Query by both `profile_id` and `session_id`.
2. Select only `chunk_source='recorded'` and require exactly one logical manifest row for every index in `[0, N)`.
3. Require a non-empty file path for every index.
4. List current original GCS objects and require each manifest file path to exist in that set.
5. Order exclusively by `chunk_index`.
6. Ignore unreferenced extra objects with a warning/metric; never add them to the merge implicitly.
7. Treat missing rows/objects as retryable. Treat duplicate/conflicting indices as a data-integrity error and do not merge.

Chunk analysis status does not determine whether uploaded media is mergeable. A PENDING or FAILED analysis row can still point to a valid uploaded clip. Merge the complete media manifest, propagate `expected_chunk_count` into `video:analysis`, and make the full-analysis indexing stage wait/retry while expected chunk analyses are nonterminal. When an expected chunk is terminal FAILED, recover that media interval from the merged/full file or report it in PARTIAL coverage; never omit its bytes from `merged.mp4`.

Remove the current tautological existence check that tests DB records against a map built from those same DB records.

Legacy fallback may continue ordering null-index rows by capture `start_secs`, but it must log `legacy_chunk_manifest=true` and must never mix indexed and null-index chunks in one merge.

#### Acceptance criteria

- A late object not present at the first listing cannot produce a successful incomplete merge.
- A stale DB row whose file is absent in GCS is not downloaded.
- Duplicate `/chunk-complete` calls produce one logical row and at most one active analysis.
- A missing index produces a retryable merge status that names only the missing indices in structured logs, not sensitive URLs.
- Indexed and legacy sessions both remain readable during rollout.

## PR 1B — Correct media timeline

Work items: REL-03, REL-04
Depends on: PR 1A

### Files to inspect/change

- `api/internal/worker/merge_chunks.go`
- `api/internal/worker/split_video.go`
- `api/internal/worker/video_analysis.go`
- `api/internal/worker/generate_hardsub.go`
- `api/internal/worker/generate_highlight.go`
- `api/internal/worker/verify_highlights.go`
- `api/internal/controllers/handlers.go` response mapping
- `features/wod/schema.d.ts`, player/timeline consumers
- Relevant worker, controller, and frontend tests

### REL-03 — Separate capture and media timelines

#### Definitions

- Capture time: elapsed wall clock from recording start. It includes the Android camera cooldown and is useful for live UI/heart-rate alignment.
- Media time: timestamp in `merged.mp4`. It is the cumulative duration of actual chunk media and excludes time when the camera was not recording.

Never copy capture time into a media field. Never use capture time in `VideoMetadata`, highlights, hardsubs, or merged-video playback.

#### Timeline construction

The merge worker already downloads all chunks. Before concat:

1. FFprobe every local chunk's actual duration.
2. Reject a chunk whose duration cannot be determined or is non-positive. This is retryable for a potentially incomplete/corrupt upload; make it terminal only after the task's final attempt.
3. Calculate in index order:

```text
media_start[0] = 0
media_end[i] = media_start[i] + probed_duration[i]
media_start[i+1] = media_end[i]
```

4. Update `media_duration_secs`, `media_start_secs`, and `media_end_secs` for all rows in one DB transaction.
5. Run concat.
6. Probe `merged.mp4` and compare its duration with the cumulative input duration. Allow only a documented encoder tolerance (start with max of 250 ms or one output frame per chunk; adjust only from test evidence).
7. Commit the merged object and enqueue full analysis only after the media timeline is valid.

If an analysis-grade re-encode is created, it must preserve a zero-based timeline and remain within the same duration tolerance as `merged.mp4`. Validate it before using the same media offsets with Gemini. If it does not preserve the timeline, use the verified merged file or fail explicitly; never apply merged-video offsets to a materially shifted analysis copy.

The full-analysis path must prefer `media_start_secs`/`media_end_secs`. A legacy fallback to `start_secs`/`end_secs` is allowed only when all media fields are null; emit `legacy_media_timeline=true`.

Expose both timelines to clients with explicit names. Keep current `start_secs`/`end_secs` response fields during compatibility; document them as capture time and add the media fields. Do not silently change the meaning of an existing JSON field.

#### Consumers to migrate

- `buildSegmentsFromChunks` and all `VideoMetadata` ranges.
- Highlight parse/clip/verify range validation.
- Hardsub subtitle placement.
- Workout player overlays or chunk markers against `merged.mp4`.
- Coverage calculations introduced in PR 1D.

#### Tests

- Three chunks with durations 10.0, 10.0, 3.2 seconds and capture starts 0.0, 10.5, 21.0 produce media intervals `[0,10]`, `[10,20]`, `[20,23.2]`.
- Android 500 ms cooldown never appears as a gap in merged-video offsets.
- A zero/corrupt duration prevents enqueue of full analysis.
- Merged duration outside tolerance fails.
- Legacy rows fall back without mixing timelines.

### REL-04 — Derive split-video offsets from actual durations

`runFFmpegSplit` uses stream copy, so keyframes determine boundaries. Keep stream copy for now, but stop assigning `startSecs = index * splitChunkDurationSecs`.

After discovering split files and before starting parallel analysis:

1. Probe every chunk duration in filename/index order.
2. Build a complete in-memory split manifest using cumulative durations.
3. Validate monotonicity, positive duration, no overlap, and final cumulative duration within tolerance of the source duration.
4. Pass manifest offsets into goroutines.
5. Persist `chunk_index` plus media fields for synthetic chunks.
6. Persist `chunk_source='synthetic_split'`; do not identify source type by filename in new code.
7. Use `(profile_id, session_id, chunk_index)` for retry skip/upsert, not floating-point `start_secs` equality.

Do not force exact 10-second boundaries by re-encoding in this PR; that adds cost and a new quality variable. If later evaluation proves exact boundaries necessary, test forced keyframes as a separate experiment.

## PR 1C — Exact handler routing and idempotent persistence

Work items: REL-05, REL-06
Depends on: PR 1A; may run in parallel with PR 1B after the migration contract is fixed.

### REL-05 — Register the session-aware handler exactly

In `api/cmd/worker/main.go`, register:

```go
mux.HandleFunc(worker.TypeChunkAnalysisWithSession, w.HandleChunkAnalysisWithSessionTask)
mux.HandleFunc(worker.TypeChunkAnalysis, w.HandleChunkAnalysisTask)
```

Register the longer/specific type before the prefix type.

Add a mux-level test, not merely a direct handler test. The test must submit a `chunk:analysis-with-session` task through the same registration function used by `main` and prove session-derived WOD/profile context is sent. Keep direct unit/integration tests, but fix `chunk_analysis_with_session_test.go` so its success path calls the specialized handler.

To make wiring testable, a small worker-specific `newWorkerServeMux(w)` helper is acceptable. Do not introduce a general handler framework.

### REL-06 — Idempotent chunk and analysis result state

#### Chunk state machine

```text
PENDING -> PROCESSING -> COMPLETED
                     -> PENDING (retryable failure)
                     -> FAILED  (final/non-retryable failure)
```

Rules:

- Handlers update the manifest row rather than `Create` a new row.
- A retry can transition FAILED to PROCESSING only through an explicit retry endpoint/action.
- COMPLETED is immutable for duplicate deliveries unless the user explicitly requests reanalysis.
- Check every DB error. A task must not return success when its required state write failed.
- Check model `err` before checking for empty output. An empty successful response is an error category of its own.
- Store sanitized user-facing failure text separately from detailed structured logs.

#### Full-result state machine

Use the existing unique `analysis_results.session_id` constraint as the idempotency key and replace independent inserts with an upsert/update lifecycle:

```text
PENDING -> PROCESSING -> COMPLETED
                     -> PROCESSING (retryable failure)
                     -> PARTIAL    (final attempt with usable but incomplete evidence)
                     -> FAILED     (final attempt with no usable result)
```

Keep public status separate from an internal `analysis_stage` enum:

```text
MERGE_PENDING -> MERGED -> ANALYSIS_ENQUEUED -> ANALYZING
    -> ANALYZED -> FOLLOWUPS_ENQUEUED -> COMPLETE
```

Direct uploads may begin at ANALYSIS_ENQUEUED. The ANALYZED checkpoint keeps public `status=PROCESSING` and is required by REL-08 to avoid repeating Gemini calls when only a follow-up enqueue failed.

Create/upsert PENDING before enqueueing `video:analysis` for both merged and direct-upload paths. At handler start, atomically claim/update PROCESSING and increment an attempt counter. On success, update the same row. Never depend on a second `Create` succeeding.

Legacy and two-pass handlers must use the same persistence rules; otherwise switching `PIPELINE_MODE` changes retry correctness.

`RetryAnalysis` must reconstruct the task from persisted source metadata. Preserve the original workout type, movement candidates, injuries, WOD description, profile, source GCS URI, expected chunk count, and pipeline/variant configuration. A retry must not silently fall back to empty/default context.

#### Duplicate-delivery tests

- Deliver the same chunk task twice: one model generation and one COMPLETED logical row.
- Deliver duplicate notification while first task is PROCESSING: no second active analysis.
- Fail full analysis once, then succeed: the single row ends COMPLETED, not FAILED.
- Fail after output persistence but before follow-up enqueue: retry does not repeat a Gemini call.
- Force a DB update error: task returns error and never reports success.

## PR 1D — Coverage, retries, and durable follow-ups

Work items: REL-07, REL-08
Depends on: PRs 1B and 1C

### REL-07 — Represent coverage and failure honestly

#### Replace `MAX(end_secs)` completeness

Implement a pure interval-union helper over successful media intervals:

1. Sort by media start.
2. Clamp to `[0, videoDuration]`.
3. Reject invalid/non-positive intervals.
4. Merge overlaps/adjacency using a small encoder tolerance.
5. Return covered seconds, uncovered gaps, ratio, and invalid count.

Use it for split-index completeness. A late successful interval must not hide an earlier gap, which is possible with the current `MAX(end_secs)` query.

#### Split behavior

- `splitAndAnalyzeChunks` must return an aggregate error when any required chunk failed.
- Preserve successful rows so a retry processes only missing/failed indices.
- Retry FAILED/PENDING indices; skip only validated COMPLETED indices.
- Carry all context fields into inline analysis: WOD description, heart rate when applicable, client confidence, movement candidates, injury context, and observed-signals parsing.

#### Full-analysis completion contract

Persist a structured coverage object, preferably an additive `coverage_json TEXT NOT NULL DEFAULT '{}'` column initially:

```json
{
  "planned_segments": 8,
  "successful_segments": 7,
  "failed_segments": 1,
  "successful_media_seconds": 91.4,
  "planned_media_seconds": 103.2,
  "observed_movements": ["snatch", "burpee"],
  "covered_movements": ["snatch", "burpee"],
  "uncovered_ranges": [{"start_secs": 80.0, "end_secs": 91.8}]
}
```

Completion rules:

- COMPLETED: every selected/required deep segment succeeded and every observed canonical movement has at least one successful deep segment.
- PARTIAL: at least one deep segment succeeded, but one of the above conditions failed after retries are exhausted.
- FAILED: no usable deep-analysis output exists after retries are exhausted, or an unrecoverable integrity error occurred.
- Intermediate errors leave PROCESSING/PENDING and return an error for retry; they do not write terminal FAILED.

The app/history API must display PARTIAL distinctly and offer retry. Do not label it complete or silently hide failed coverage.

### REL-08 — Explicit task policy and resumable follow-ups

#### Task options

Define named options/constants next to each task constructor and test them. Avoid handler-local `retryCount >= 3` checks whose meaning differs from Asynq task configuration.

Recommended initial policy to validate in staging:

| Task | Total attempts | Timeout | Queue intent |
|---|---:|---:|---|
| `chunk:analysis*` | 3 | 3 minutes | real-time/high priority |
| `merge:chunks` | 3 | 30 minutes | media |
| `video:analysis` | 3 | 30 minutes initially | analysis |
| `injury:analysis` | 3 | 20 minutes | analysis |
| `hardsub:generate` | 2 | 30 minutes | media |
| highlight generation/verification | 2 | 20 minutes | media/analysis respectively |

These are safe starting bounds, not permanent tuning. OBS-02 must record timeout proximity and Phase 4 must tune them from p95 duration and deployed resource limits. Document Asynq's retry semantics in tests: `MaxRetry(2)` means one initial attempt plus two retries.

Classify errors:

- Nonretryable: malformed payload, invalid session/profile ownership, unsupported URI/format, deterministic schema validation failure after any allowed repair.
- Retryable: transport timeout, rate limit, transient GCS/DB/Redis failure, file still processing, incomplete expected manifest.
- Terminal only on final attempt: persistent model empty output, corrupt media, missing object after recovery window.

#### Follow-up checkpoint contract

Avoid both bad outcomes: replaying the full AI analysis because hardsub enqueue failed, or returning success and permanently losing the follow-up.

Use an additive checkpoint on the existing analysis result rather than a new generic workflow framework:

- `analysis_stage`: the internal enum defined in REL-06 (`MERGE_PENDING` through `COMPLETE`).
- `hardsub_enqueue_status`: `NOT_REQUIRED`, `PENDING`, `ENQUEUED`.
- `injury_enqueue_status`: `NOT_REQUIRED`, `PENDING`, `ENQUEUED`.

Flow:

1. Persist output/coverage and stage ANALYZED transactionally.
2. Enqueue required downstream tasks with stable payloads and Asynq uniqueness options.
3. Mark each enqueue status only after success.
4. Mark public status COMPLETED/PARTIAL and stage COMPLETE after required enqueues are durable.
5. On retry, if stage is ANALYZED or FOLLOWUPS_ENQUEUED, skip download and Gemini calls; resume only missing enqueues/finalization.
6. Make downstream workers idempotent by `(profile_id, session_id, artifact type)` because a crash can occur after enqueue but before the DB flag update.

If a later implementation adopts a transactional outbox, document it in `docs/agent-memory/`. Do not introduce one during this PR unless checkpoint fields prove insufficient.

#### Merge-to-analysis checkpoint contract

An enqueue failure after `merged.mp4` upload must not repeat every download and FFmpeg operation. Persist the merge handoff before enqueue:

1. Compute a deterministic manifest hash from profile ID, session ID, expected count, ordered chunk indices, file paths, and measured durations.
2. After the media timeline and merged upload are verified, upsert the session's PENDING analysis row with `analysis_stage='MERGED'`, the merged/analysis source URI, and the manifest hash.
3. Enqueue `video:analysis` with a stable uniqueness key, then update stage to `ANALYSIS_ENQUEUED`.
4. On merge retry, if the stored manifest hash matches and the merged object exists, skip download/concat/upload and resume only the enqueue/update.
5. If the manifest hash changed, stop with a data-integrity error; never reuse a merged object built from a different manifest.

Use additive fields on the existing per-session analysis state rather than a generic workflow table for this first implementation. Make direct-upload and merged-video source metadata explicit so `RetryAnalysis` can rebuild the same payload.

## Phase 1 test commands

Run from the repository root unless stated otherwise:

```bash
npm test -- features/wod/api.test.ts
npm test -- features/wod/chunkFinalization.test.ts
```

Run from `api/` after applying the new migration to the test DB:

```bash
make migrate-test-redo
go test -p 1 ./internal/controllers ./internal/worker
go test -p 1 ./cmd/worker
```

If the repository scripts differ, use the closest existing targeted command and report it. Do not run backend test packages concurrently because they share PostgreSQL and Redis test state.

## Phase 1 release gate

Do not proceed to model/output changes until all are true:

- [ ] Final partial chunks are uploaded on both iOS and Android.
- [ ] Serial, concurrent, compressed, and uncompressed paths await acknowledgement.
- [ ] Merge receives and validates an expected index set.
- [ ] All merged-video consumers use media offsets.
- [ ] Synthetic split offsets are cumulative actual durations.
- [ ] Session-aware task routing has a mux-level test.
- [ ] Duplicate task/HTTP delivery is idempotent.
- [ ] Transient failure cannot leave a terminal FAILED row.
- [ ] Partial segment evidence is stored as PARTIAL with structured coverage.
- [ ] A follow-up enqueue retry does not repeat completed Gemini analysis.
- [ ] Legacy clients/sessions have tested, observable compatibility fallbacks.
