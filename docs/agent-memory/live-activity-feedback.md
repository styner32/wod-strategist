# Live capture feedback and activity counting

## Activation and compatibility

- Migration `000051` adds nullable JSONB fields `capture_assessment`,
  `contextual_coaching`, `movement_observations` to original chunks, and
  `activity_summary` to `sessions`. Use `NullableJSONDocument`, not
  `JSONDocument`: unevaluated fields must remain SQL NULL, not `{}`.
- Worker switches `ENABLE_CAPTURE_FEEDBACK`, `ENABLE_CONTEXTUAL_COACHING`,
  `ENABLE_ACTIVITY_COUNTING` default to false and can be enabled independently.
  Apply the migration before deploying API/worker code. Turn switches on only
  for a controlled evaluation, then roll out after real-video/device acceptance.
- New mobile `/chunk-complete` calls send `live_analysis_version: 1`. Only these
  calls get new observations. Old recordings and uploaded-video split analysis
  are not backfilled. Both original chunk handlers implement the same contract.
- `GET /api/v1/sessions/:session_id/activity-summary?profile_id=<owned id>`
  requires profile and session ownership. Existing chunk array responses remain
  compatible. Mobile and web use `shared/activity.ts` for the new contracts.

## Evidence and clocks

- Base chunk inference sees no recent-chunk history. New structured blocks are
  stripped before the legacy observed-signals regex is applied. Independent
  `output`, movement tags, and observed signals are never replaced by connected
  coaching or post-recording count review.
- Capture assessment states: `good`, `needs_adjustment`, `unknown`; one issue
  (`tilt`, `viewpoint`, `framing`, `occlusion`, `dark`, `backlight`) and advice in
  `ko`/`en`. NO_EXERCISE does not suppress camera feedback. The mobile camera
  cue expires 30 seconds after the source's capture end, not its DB update time.
- Movement observation version 1 requires target/activity state, `events`, and
  `unassessed`. An event has movement, unit (`reps` or `seconds`), start/end,
  complete, and visual evidence. Original times are chunk-local video seconds;
  reviewed times are absolute merged-media seconds. `duration_secs` comes from
  the probed video, not capture-wall-clock duration. Unassessed original events
  use `clock: chunk` with their source ID; only verified review uses `clock: media`.
  Unknown is not zero.
- A rep is a performed cycle, not a competition-valid rep. One complete compound
  cycle is one rep; an alternating side is one rep. Timed catalog activities
  use observed seconds. Unknown names/units, overlaps, incomplete cycles, and
  unassessable target evidence do not become counted reps. Legacy rep_count and
  muscle-load default estimates are never inputs to this aggregate.
- The API labels coverage `recorded_chunks`: it cannot assert coverage of an
  unrecorded workout, or detect every missing trailing upload without a complete
  capture manifest. Capture gaps remain explicit. Never silently label this an
  exact full-workout total.

## Coaching and delivery

- Query the same profile/session only. Use up to six completed, non-overlapping
  chunks wholly inside the 60 seconds before the current capture start.
- Matching movement, target confidence above 0.5, and unambiguous
  evidence are required. Rest, failed rows, overlaps, target uncertainty, and movement changes break
  history. Gaps between available sources are explicit `gap_after_secs`; they
  cannot support continuity/trend claims. No waiting for out-of-order results.
- Send compact source IDs/times, observed form issues, and basic coaching only;
  never recursively include contextual coaching or cumulative counts. The text
  request has a 10-second deadline. Its IDs must match the selected inputs.
- Persist base inference before optional coaching. A re-delivery resumes saved
  evidence instead of repeating the video call. Optional generation failure
  leaves the base coaching intact. Text is still generative; these structural
  guards cannot guarantee that every sentence is factually grounded. Evaluate
  adversarial histories before enabling the feature.
- Mobile requests deliver chunk and aggregate responses independently, ignore
  previous sessions, serialize polls, abort stale requests, and refresh the
  capture-age clock independently of response arrivals.

## Review lifecycle

- Merge dispatches `activity:review` before full analysis. Enqueue failure is
  propagated after attempting the independent full-analysis dispatch. Review
  uses the original merged video's pinned GCS generation, not an analysis proxy.
- For each source chunk, include one neighboring chunk on either side only
  when both capture and verified media clocks are continuous (1 ms rounding
  tolerance). Actual pauses/camera cooldown are hard evidence boundaries.
- Own repetitions by completion in `(core_start, core_end]`; clip timed events
  to the core. Reviewed evidence replaces that core's provisional evidence.
- Persist each window and resume successful windows on retry. Asynq has two
  retries, a 20-minute task timeout, and 90-second window deadlines. Failed
  windows retain provisional evidence with explicit unassessed coverage.
- Store review schema version, media generation, and a source fingerprint of
  canonical chunk identity/status/times/observations. Canonicalize JSON before
  hashing because PostgreSQL JSONB reorders keys. Coaching timestamps/text are
  excluded. Session locking plus source/generation checks reject stale writers.
- States are provisional, queued, running, completed, partial, failed, disabled, unavailable.
  Completed means review processing completed, not guaranteed count accuracy.
  Disabling counting stops new processing; existing results remain readable.

## Verification and acceptance

Automated tests cover counting ownership, duplicate deliveries, clock gaps,
source replacement, missingness, both live handlers, optional-coaching failure,
review resumption, and authenticated aggregate reads. Tests use Ginkgo/Gomega,
real PostgreSQL/Redis, and real Gemini/GCS clients with MockTransport.

Before enabling in production, collect manually annotated videos for squat,
pull-up, compound/alternating movements, fast skipping, timed activity, rest,
movement changes, frame exits, occlusion and capture pauses. Record the model,
device/OS, frame rate, feature switches, video source/generation and the exact
truth/observed event intervals. Run `scripts/evaluate-activity-counts.py` against
the annotated truth and API summary; retain its output with the input pair.

Measure per movement: matched/missed/extra cycles, missing and excess rates,
and unassessed coverage. Compare the same current chunk with correct/incorrect
history and check coaching claims manually. No quantified model accuracy or
real-device acceptance is implied by transport/aggregation tests.

On actual iOS/Android devices, verify lighting/framing changes, cue clearance
and expiry, pause/resume, final partial chunk, network delays, app backgrounding,
gallery output, and recording stability. Record capture-to-feedback latency and
incremental token usage (`chunk:contextual-coaching`, `activity:review`). Enable
each switch separately; restore it to false if acceptance fails. Rollback leaves
stored observations available and does not run the down migration in production.

## Local verification performed (2026-09-16)

- `api/`: `go test -p 1 ./internal/activity ./internal/config ./internal/gemini ./internal/worker ./internal/controllers ./cmd/worker -count=1 -ginkgo.no-color -ginkgo.succinct` passed with the local test DB/Redis and mocked external APIs. The writable Go cache was `/private/tmp/wod-activity-go-cache`.
- Root: `npm test -- --runInBand features/wod/__tests__/liveFeedback.test.ts features/wod/__tests__/useLiveWorkoutFeedback.test.tsx` passed (9 tests).
- Root: `npm run typecheck` passed. `web/`: `npm run build` passed; the existing bundle-size advisory remains.
- `api/`: `make migrate-test-up` and `make migrate-test-redo` passed against `wod_test` only.
- Evaluation-script synthetic matching/missing/extra/coverage checks passed. These are tests of the evaluator, not measurements of model accuracy.
- Real annotated-video evaluation, physical-device acceptance, production migrations, feature activation, and deployment have not been performed.
