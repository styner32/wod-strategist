# Confirmed-record recognition diagnosis

## Boundary

The web history performance panel compares current-source manual confirmations
with raw persisted observations. Opening the panel and changing selection/time
filters do not invoke AI. Only the explicit diagnosis action creates a run.
This is guided retrospective diagnosis, not independent recognition accuracy,
automatic correction, or model training. It never updates observations,
`workout_progress`, confirmed performance records, or benchmarks.

## Baseline and scope

- Read `analysis_results.workout_progress` before response enrichment. Never use
  `performance_records.original_candidate` as the original prediction: synthetic
  gap confirmations can contain the user's corrected movement there.
- Match the complete confirmed window against all overlapping raw intervals and
  media-clock gaps. Use canonical movements and complete `unit=reps` events with
  completion ownership `(record.start_secs, record.end_secs]`; do not double count
  merged records or repeated references. Missing count evidence remains null.
- Overall comparison covers current confirmed records only. It is not a metric
  for unconfirmed portions of the video.
- Review times are original merged-media seconds. Both bounds filter complete
  records: a record crossing either bound is excluded, not partially recounted.
  A record's confirmed total is never an expected total for each split clip.

## API and persistence

- `GET /api/v1/sessions/:session_id/recognition-evaluation`: deterministic current
  comparison, source version, and evidence/confirmation fingerprint; no model call.
- `POST /api/v1/sessions/:session_id/recognition-reviews`: `record_ids`,
  `start_secs`, `end_secs`, `source_version`, `evaluation_fingerprint`, and
  `client_request_id`. The server owns source resolution and evidence snapshots.
- `GET /api/v1/sessions/:session_id/recognition-reviews` and
  `GET /api/v1/sessions/:session_id/recognition-reviews/:run_id`: durable history
  and results. Reading/resuming status never creates another model request.
- Migration `000060` creates `recognition_review_runs`. Preserve immutable source
  generation, raw progress/chunks, and confirmations along with mutable execution
  status and durable batch results. Only one active run per owned session is
  permitted. Request-key reuse with different content or stale evidence is `409`.
- Run statuses: `QUEUED`, `RUNNING`, `COMPLETED`, `PARTIAL`, `FAILED`.
  Source/confirmation changes mark historical results outdated rather than
  rewriting their snapshots. Account deletion removes run snapshots.

## Model input and output

- `recognition:review` uses the configured deep-analysis model and native 5 FPS
  segment input. Download the exact GCS generation once and upload accurately
  re-encoded selected clips only; do not use keyframe stream-copy trimming or
  upload the whole source. Internal split cores are at most 150 seconds with
  5-second context clamped to the confirmed window.
- Include only relevant historical evidence in each diagnostic prompt. Treat
  notes/output as evidence data, not instructions. No full-history reinjection.
- Findings contain timestamped differences, original and visual evidence,
  possible reason/stage, and limitations. Evidence levels are `stored_fact`,
  `video_supported_hypothesis`, and `undetermined`. Saved skip/gap reasons can
  establish a routing outcome; they do not reveal private model reasoning.
- Clip-local timestamps are validated and mapped back to original media; overlap
  context must not create duplicate owned findings. Total-only confirmations do
  not identify which individual repetition was missed.
- Resume completed batches without another model call. Partial failures remain
  explicit. Delete only this run's newly created Files objects and local media.
- Record actual usage under task `recognition:review` and stage
  `recognition_review`. Never append `:batch` for ordinary split clips: the cost
  ledger reserves that suffix for the discounted Gemini Batch API.

Automated tests validate transport, arithmetic, ownership, durability and UI
behavior. They do not establish explanation quality on real workout footage.

## Verification (2026-10-28)

Implementation locations: `web/src/history/components/RecognitionReviewPanel.tsx`
and `web/src/history/recognitionReview.ts` own the web flow; `history.ts`,
`WorkoutProgressPanel.tsx`, `SessionDetailPage.tsx`, and `SessionCostCard.tsx`
connect its API, confirmation invalidation, player and cost UI.
`api/internal/recognitionreview/` owns comparison/types; controller
`recognition_review_handlers.go`/`recognition_review_dto.go`, DB
`recognition_review.go`/migration 60, and worker `recognition_review.go` own the
durable API and diagnostic task. Server/worker wiring, model configuration,
account deletion/factories, generated API schemas and their focused tests were
updated alongside these additions.

The following checks passed with external Gemini/GCS calls intercepted by the
real clients' `MockTransport`. PostgreSQL and Redis ran in dedicated disposable
containers, separate from development services:

- 12 comparison/batch domain specs; 14 worker helper/integration specs; 10 route
  integration specs; 3 account-deletion specs including diagnostic snapshots.
- The configuration suite, server/worker compilation, root TypeScript checking,
  and five web regression suites passed. New regression cases cover manual-only
  execution, crossing bounds, pending-request identity, polling without repeated
  AI, stale results, partial/unknown counts and token usage, clock mapping,
  precise uploaded clips, cleanup recovery, and preservation of original data.
- Swagger JSON, YAML and embedded Go agree on the new routes and definitions;
  OpenAPI and generated TypeScript retain explicit nullable count/match fields.
  Only this feature's schema additions were retained; unrelated stale generated
  documentation was not regenerated into the change.
- Migration 60 applied, rolled back, and reapplied against the temporary database.
- Web production build passed with its existing bundle-size advisory. Targeted
  ESLint could not start because the existing `typescript-eslint` installation
  throws while reading `Cjs`; no dependency/tooling configuration was changed.

Commands (Go from `api/`; test connection variables point to temporary services):

```sh
go test -p 1 ./internal/recognitionreview ./internal/config ./cmd/server ./cmd/worker -count=1 -ginkgo.no-color -ginkgo.succinct
go test -v -p 1 ./internal/worker ./internal/controllers ./internal/auth -count=1 -ginkgo.no-color -ginkgo.succinct -ginkgo.focus='recognition review helpers|HandleRecognitionReviewTask|recognition-evaluation|recognition-reviews|DeleteAccount'
```

Root/web commands:

```sh
npm test -- --runInBand web/test/recognitionReview.test.tsx web/test/workoutProgress.test.tsx web/test/sessionReviewWorkspace.test.tsx web/test/historyPolling.test.ts web/test/analysis-presentation.test.ts
npm run typecheck
npm run generate-types
cd web && npm run build
git diff --check
```

A wider run additionally included `shared/workoutProgress.test.ts` and found two
existing expectation failures in unchanged confirmation code: the expected
objects omit the now-explicit movement, and the older merged-target test assumes
a target change without updating its explicit draft movement. These were not
changed as part of diagnosis. Real model/annotated-video validation, production
migration and deployment remain unperformed. Release order is migration 60,
API/worker, then web.
