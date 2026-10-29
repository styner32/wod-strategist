# Session archive lifecycle

## Public contract

- `GET /history?profile_id=...` returns active records; `archived=true` returns only archived records. Existing `limit`, `before_id`, `from` and `to` behavior is retained.
- `POST /history/:id/archive` and `/unarchive` require the analysis owner's profile. Responses retain `message` and add an opaque decimal-string `history_revision`.
- `POST /sessions/archive-states` accepts `{profile_id, session_ids}` (1–100 IDs, duplicates deduplicated) and returns `{checked_at, history_revision, states}`. Each state is `active`, `archived`, `not_registered`, or `unavailable`. Ownership conflicts across sessions/analysis/chunks are unavailable without foreign metadata. A missing server row is not automatically archived: the mobile client may use it only with matching locally recorded user/profile provenance.
- Ordinary aggregate queries exclude archived rows, including incomplete-score/time evidence counts. Existing saved analysis/sensor originals and stretches are retained; archived mobility is excluded from new recommendations. Cost accounting and video preservation are independent.

## Persistence and cancellation

- Migration `000061` adds `analysis_results.automatic_processing_cancelled_at` and `profiles.workout_history_revision` and extends relevant status constraints with `CANCELLED`. The profile revision is read-only in GORM structs: use a table-level update so profile-edit `Save` cannot overwrite it.
- Archive locks **profile before analysis**, sets the durable original-processing stop marker, cancels unfinished main/chunk/highlight/sensor/manual work, and interrupts active summary/Agentic runs/items. Completed outputs are retained. Restore clears only `archived_at`; it never clears the stop marker or schedules analysis. Repeated mutations are idempotent.
- All original worker entry points and API ingestion/fan-out check the stop marker. Worker child contexts poll every 250 ms. `db.WithSessionAutomaticWork` locks profile then parent before publishing results or enqueueing; late saves, retries and recovery must use the same fence, including `WithoutCancel` writes.
- Upload completion, chunk completion and merge requests persist movement/WOD/appearance metadata inside that same processing fence and transaction as task enqueue. A rejected or failed enqueue must leave source metadata unchanged. Original workers acquire the same fence before reading source data, so an early Redis delivery waits for the ingress transaction to commit.
- Metadata-bearing acceptance checks request cancellation before entry, then uses a detached context bounded by `sessionUploadAcceptanceTimeout` (15 seconds), including metadata helpers and SQL commit. A caller disconnect after Redis accepts the task cannot cancel that commit. Redis acceptance and SQL commit are still separate systems: a DB commit failure or acceptance timeout after successful enqueue requires reconciliation; this is not an outbox or a distributed atomicity guarantee.
- New explicit manual runs after restore use their own run IDs; canceled old IDs are terminal. Session candidate apply uses `PrepareManualEnrichmentOutbox`, while original completion uses `PrepareEnrichmentOutbox`. Recovery must not create another run from `interrupted` state. Provider cleanup and billed usage still run after cancellation.
- Migration also retires old archived work, so its first restore cannot resurrect pre-feature manual or enrichment tasks. Unknown legacy personal calibrations on profiles with archived records are conservatively invalidated; frozen session snapshots survive.

## Derived inputs

- Archive/restore recomputes personal ENMO calibration from raw pre-normalization P05/P95 values in the latest 25 unarchived completed sensor summaries, selecting 3–5 valid v3+ sources with at least 120 valid ACC seconds. Fewer than three clears the personal fields; the explicitly labeled bootstrap remains available. Initial sensor promotion uses the same profile lock.
- New advice accepts historical personal hybrid values only when the frozen numeric calibration inputs match current profile calibration. Explicit bootstrap inputs remain usable. Missing/mismatched provenance yields `hybrid_sensor_excluded_reason` (`calibration_provenance_unknown` or `calibration_changed`), while Effort/time remain usable. Original sensor results are never overwritten or automatically reprocessed.
- Advice snapshots history revision and revalidates after AI generation. If archive/restore changed history, discard generated advice and return fresh deterministic guidance. Cost already consumed remains recorded.
- Mobile record-derived aggregates and preset reuse require fresh server validation; offline/failed verification cannot reuse an earlier active cache. Raw local evidence remains inspectable. Account/profile switches and background transitions invalidate pending verification. Explicit native runs subscribe to the starting account and selected profile, cancel on either transition, and discard their answer even if the user switches back before completion. Old UI actions cannot reload or change the new scope's state.
- Recent settings use `wod_recent_workout_setup_v2:{userId}:{profileId}`, separating independent `draft` and `recentRecording` slots. Recorded settings retain source session identity through reuse and are verified at actual recording start. Legacy v1 is retained but is not automatically reused.
- Local archive checks have a five-second deadline, including authentication-token reads. Aggregate batches must share the same history revision; verification is repeated after local-source reads and before native AI execution/publication. `Camera.startRecording()` returns before native startup succeeds: recent recording provenance changes only after `addOriginalChunk` accepts the first actual original source. Startup or manifest failures retain the previous source; duplicate finalization callbacks and pause/resume do not rebind it again.
- Invalidating whiteboard OCR (fresh inputs, scope changes or applying saved settings) also clears its loading state. Old responses cannot apply results or clear a newer request's loading state.

## Cross-workout dependency audit

| Consumer | Source and exclusion boundary |
| --- | --- |
| Readiness, fatigue and pre-workout strategy | `controllers/strategy_handlers.go`: active completed analyses and sessions only; current calibration and history revision travel with the input snapshot. |
| Training load, tracking start and insufficient-evidence counts | `controllers/wod_training_handlers.go`: every aggregate filters `a.archived_at IS NULL`, including missing-score/time and unevaluated rows. |
| Confirmed performance comparison | `controllers/workout_progress_handlers.go`: confirmed records join their active, completed source analysis and matching progress version. |
| Related WOD and worker history context | `controllers/related_wods_handlers.go` and `worker/history.go`: both source/candidate history exclude archived analyses. |
| Sensor calibration and historical sensor intensity | `db/sensor_calibration.go`: recompute from active raw sources, then check frozen calibration provenance before future advice. |
| New stretch recommendations | `worker/stretch_recommendations.go`: active mobility history and revision checks around generation/publication; saved recommendations and shared stretches survive. |
| Local behavior aggregate and native summary/reanalysis | `features/environment/eligibleHistory.ts`, `review.ts`, `EnvironmentCard.tsx`: fresh verified sources, same revision, current owner/profile, and late-result rejection. |
| Recent workout settings | `features/wod/recentWorkoutSetup.ts`, workout setup and recorder: separate independent drafts from source-bound recordings, validate again at actual recording start. |

The development-only video comparison query also excludes archived source analyses. Ownership, raw-source viewing, downloads, cost accounting, and review evidence are deliberately retained; they do not supply another workout's automatic advice.

## Validation and release

- Run DB/Redis suites serially against an isolated test database (suffix `_test`) and Redis DB 15. Relevant specs include `history/:id/archive`, `history/:id/unarchive`, `sessions/archive-states`, `Archived session processing fence`, `archive calibration`, and `automatic processing publication fence`.
- Apply migration before new API/workers, fully replace old workers before enabling clients. Do not run a mixed old/new worker fleet while relying on cancellation guarantees. Web archive stops server processing; it does not remotely stop the phone camera. Already accepted provider requests may still incur usage, but late results cannot publish.

### Validation record (2026-10-29)

- Ginkgo: 60 controller, 17 DB/calibration and 129 worker specs passed against isolated PostgreSQL/Redis services. Covered archive/restore ownership and idempotency, batched local status, guarded processing ingress, late publications, canceled old runs, recovery, calibration contributors and stale advice. Redis task-uniqueness keys require a fresh isolated test instance for repeated queue-suite runs.
- Migration: seeded legacy archived rows, then verified `60 → 61 → 60 → 61`, including legacy unfinished manual/enrichment/sensor work and retained completed outputs.
- All Go packages compiled with `go test ./... -run '^$'` from `api/`.
- Web: five focused suites / 37 tests passed, plus `npm run build` from `web/`. `npm run lint` cannot initialize with the installed TypeScript/ESLint combination (`Cannot read properties of undefined (reading 'Cjs')`); dependency versions were not changed.
- Mobile: 14 focused suites / 196 tests passed, TypeScript checking and an offline iOS Hermes bundle export. Native camera/provider behavior on a physical device and production rollout remain unverified.
- Review follow-up: 99 controller and 131 worker specs passed against isolated PostgreSQL/Redis, including 12 ingress acceptance/race/error regressions and two worker entry-fence regressions. Mobile: 15 suites / 205 tests passed, covering failed/accepted original capture provenance, OCR invalidation and stale responses, and storage-size units; TypeScript checking passed. These checks do not establish distributed Redis/SQL atomicity or physical-device camera behavior.

Commands from the repository root (DB/Redis environment must target isolated test services):

```sh
cd api
go test ./internal/controllers ./internal/db ./internal/worker -p 1 -count=1 -v -ginkgo.no-color -ginkgo.succinct -ginkgo.focus='POST /api/v1/(upload-complete|chunk-complete|merge-chunks)|POST /api/v1/history/:id/(archive|unarchive)|GET /api/v1/history|POST /api/v1/sessions/archive-states|strategies|/wod-training-load|/related-wods|performance-benchmarks|archive calibration|automatic processing publication fence|fences archived and restored original processing|does not resume a stopped original sensor request|rejects new explicit work while archived|rejects applying a completed manual candidate|Archived session processing fence|Session cancellation context|Analysis enrichment lifecycle|Chunk lifecycle recovery|Handle(VideoAnalysis|ChunkAnalysis|ChunkAnalysisWithSession|MergeChunks|InjuryAnalysis|GenerateHighlight|GenerateHardSub|VerifyHighlights|ChunkDebugReanalysis|SessionDebugReanalysis|RecognitionReview)Task|SensorTelemetry|SensorRecovery'
go test ./... -run '^$'
cd ..
npx jest web/test/historyArchive.test.tsx web/test/historyPollingInteraction.test.ts web/test/wodTraining.test.tsx web/test/recognitionReview.test.tsx web/test/sessionReviewWorkspace.test.tsx --runInBand --silent
npm --prefix web run build
npx jest --runInBand --silent --runTestsByPath \
  features/wod/__tests__/archiveStates.test.ts \
  features/wod/__tests__/recentWorkoutSetup.test.ts \
  features/environment/__tests__/eligibleHistory.test.ts \
  features/environment/__tests__/EnvironmentCard.test.tsx \
  features/environment/__tests__/review.test.ts \
  features/wod/__tests__/HistoryList.highlightPolling.test.tsx \
  app/workout/__tests__/setup.redesign.test.tsx \
  'app/(tabs)/__tests__/index.test.tsx' \
  app/workout/__tests__/visionTestPage.original.test.tsx \
  app/workout/__tests__/visionTestPage.feedbackOverlay.test.tsx \
  features/wod/__tests__/ProcessingSection.test.tsx \
  features/wod/api.test.ts \
  features/environment/__tests__/observation.test.ts \
  features/wod/ui/PreWodStrategyCard.test.tsx \
  'app/(tabs)/__tests__/profile.test.tsx'
npx tsc --noEmit --pretty false
EXPO_OFFLINE=1 npx expo export --platform ios --output-dir /private/tmp/wod-archive-mobile-export
git diff --check
```
