# Independent observations, workout progress and final assessment

## One-way data boundary

1. Segment observers see the current video window, target identity, profile and user workout hints. Never provide previous observations, historical scores, cumulative fatigue, or previous assessments to an observer.
2. `workout_progress` assembles observed intervals and complete repetition events chronologically. It contains evidence, not fatigue, recovery or intensity scores.
3. Final assessment reads this evidence. It must never update observations or feed its output back into the timeline builder.

Current-segment posture/camera guidance remains available. Following the user's 2026-10-14 clarification, bounded `addContextualCoaching` is allowed after independent observation: at most six qualified same-movement chunks from the preceding 60 seconds, with separate `contextual_coaching` storage. It cannot rewrite observations, basic output or `workout_progress`, claim whole-session fatigue/recovery, or feed back into the video observer. The existing `ENABLE_CONTEXTUAL_COACHING` switch and default remain unchanged. Full performed-history real-time analysis remains a later phase requiring the user's acceptance of final assessment, separate storage, source/version references, and comparison against the independent observer. Context-derived signals are never automatically promoted into original observations.

## Persisted contract (version 1)

`analysis_results.workout_progress` and `session_reanalysis_runs.workout_progress` are nullable JSONB. SQL NULL means not evaluated. Progress has `version: 1`, `clock: media`, `source_version`, `intervals`, and `unassessed`.

Each interval has `id`, `start_secs`, `end_secs`, `activity` (`exercise`, `rest`, `unknown`), `movement`, nullable `load`, `observation_refs`, `rep_events`, `form_observations`, `rom_observations`, and `pace_observations`. Form/ROM/pace observations are `{start_secs,end_secs,evidence}` objects. Optional `capture_start_secs`/`capture_end_secs` preserve verified recording/media mapping. `rep_events` use the existing complete-cycle event contract (`movement`, `unit`, `start_secs`, `end_secs`, `complete`, `evidence`). Events and playback bounds use original merged-media seconds. Gaps carry an explicit clock and reason; missing coverage is not rest or zero repetitions.

Capture mapping considers chunks overlapping the attempted media range, after
eligible WOD onset trimming. Pre-onset pauses do not suppress WOD evaluation.
Equal separation in the media/capture clocks represents recorded between-set
footage; excess capture separation is explicitly unassessed. Conflicting relevant
mappings prevent elapsed-rate confirmation. A span crossing a recording pause
has no paired capture endpoints. Full-video observations require a verified
duration; an unknown duration cannot establish repetition bounds.

Load is `{value, unit: lb|kg, basis: total|per_implement, status: pending_confirmation|confirmed}`. Planned WOD loads are candidates, never proof of actual load. Ambiguous prescribed weights remain unresolved. `source_version` identifies video/evidence content and is independent of assessment text. Optional `media_source_id` preserves the exact media identity across reuse of the same immutable uploaded Files source; never reuse it for a different Files URI.

For example, Power Clean at media 10–40 seconds, directly observed rest at
40–50 seconds and Double Under at 50–70 seconds are three separate intervals.
An absent 40–50 second observation is an `unassessed` gap instead of a rest
interval. Each complete rep retains its own start/end and evidence beneath the
containing interval; final evaluation text is stored separately.

Use existing activity validation and cycle ownership. Never use `observed_signals.rep_count`, estimated cadence/intensity, workout targets, or muscle-load estimates as performed-event evidence. Preserve same-movement reappearances, actual load changes, incomplete cycles and camera gaps. No fixed muscle recovery half-life is used.

## Confirmation and APIs

- `GET /api/v1/sessions/:session_id/workout-progress`: `{progress, records}`; progress is null when unavailable.
- `PUT /api/v1/sessions/:session_id/performance-records`: `{source_version, records:[{interval_id, start_secs, end_secs, reps, load:{value,unit,basis}, measurement: set|window, technique: unspecified|singles|touch_and_go}]}`. Confirms a complete selected video window, repetitions and actual load. Repeated saves replace the same interval record; source changes return 409. User confirmation never changes raw observations.
- `GET /api/v1/performance-benchmarks?profile_id=...&movement=...`: confirmed current-source records, comparisons and best records. Exact owned profile/session scope is required for all reads/writes.

A `set` stays inside its selected exercise interval. A `window` can continue
from that interval through contiguous, directly observed rests and the same
movement at the same load candidate. Unknown activity, different movements or
loads, omitted media and discontinuous/unverified capture mappings stop the
window. This allows five Power Cleans in a verified 60-second exercise/rest/
exercise window to be recorded as actual 5 reps/minute. A 30-second set with
five reps is instead labeled a normalized 10 reps/minute. Window confirmations
retain every participating source interval in their original-candidate snapshot.

Performance records preserve source, original candidate and user-confirmed values. `performed_at` and `performed_at_source` refer to the workout's resolved time, never the confirmation time. The server derives repetitions/minute from confirmed `elapsed_seconds` (including observed pauses), distinguishes actual 60-second windows from normalized rates, and derives median cycle seconds only when complete event evidence matches the confirmed window/count. Verified continuous capture timing takes precedence when media and capture durations differ; ambiguous partial mappings cannot produce a rate. Missing values are null. Preserve original weight/unit; comparison uses kg rounded to one decimal, with total/per-implement bases separate.

Direct comparisons require the same canonical movement, load/basis, measurement, workout prescription and technique. Sets require equal repetitions and can compare different completion times; fixed windows require equal elapsed duration and can compare different repetition counts. Unknown prescriptions/techniques and different WODs are reference comparisons with conditions shown. Default baseline is the latest earlier directly comparable record. Heavier-load records at equal repetitions have an independent baseline and remain separate from pace improvement; never infer maximal strength or estimated 1RM from pace.

Confirmed windows also expose response-only `rest_comparisons`: observed rest
media bounds and elapsed seconds, before/after median repetition duration, and
duration change percent. These metrics replay from the preserved source window,
never from assessment text. Positive percent means longer repetition duration,
not greater recovery. A count mismatch, source gap or unverified repetition clock
leaves before/after values null. Missing surrounding evidence yields no comparison.
They do not estimate physiological recovery and never update `workout_progress`.

## Assessment, reanalysis and release

Coverage completeness (`progressCoverageComplete`) is judged only inside the assessed span: every attempted window, including failed and `segment_budget_omitted` ones, but not the unindexed edges filled as `not_analyzed`. Version-1 WODs end the span at the last validated `wod_evidence` end. Count-only reasons (`no_countable_evidence`, `incomplete_cycle`) and capture-clock `recording_gap` keep coverage complete; `unverified_capture_mapping` does not. The triage budget (`maxSegmentsForDuration`) applies to indexed segments only. Between-set observation windows are added afterwards and never bridge an omitted segment. A malformed model entry voids only its clamped claimed bounds; an unlocatable entry still voids the whole window.

Segment responses also contain time-bounded direct form/ROM/pace observations. Final WOD scoring uses all evidence; warmup scoring treats controlled progressive ROM expansion positively. Spinal/pelvic observations must be visible and movement-specific; abdominal pressure or muscle activation cannot be established from video alone.

Only existing version-1 WOD eligibility invokes onset trimming and version-1 score policy. Timeline availability does not upgrade historical scoring or Effort eligibility. Explicit selected-video reanalysis can create progress without historical backfill. Reanalysis candidates enter comparisons only after apply. Apply atomically replaces progress; changed-source confirmations require review again. Summary fingerprints include workout type and progress as well as analysis text.

Release order: migration, API/worker, web/mobile. Automated tests cover boundaries, coverage, clocks, source invalidation, ownership and benchmark math; actual-video accuracy and physical-device acceptance remain separate. Validate final assessment against manually annotated videos and user review before designing cumulative real-time analysis.

## Local verification (2026-10-13)

Passed with an isolated PostgreSQL/Redis instance and actual Gemini/GCS clients
backed by MockTransport; no production database or external model was called:

- Full `workoutprogress` (18 specs), `activity` and `wodeval` domain suites.
- 105 selected worker specs covering independent prompts/live handlers, warmup,
  legacy/full WOD analysis, final failure, enrichment, clocks and source identity.
- 36 selected controller specs covering confirmation, ownership, stale sources,
  candidate application, legacy reanalysis and WOD/Effort policies; an additional
  16 reanalysis worker/helper specs passed.
- Eight frontend suites / 97 tests, root typecheck, web build (existing bundle-size
  advisory), and Swagger JSON/YAML/embedded-Go schema consistency.
- Migration 56 apply → rollback → reapply; existing analysis text and score hashes
  remained equal. Test services were temporary. Production rollout and physical
  device/annotated exercise-video acceptance have not been performed.

Commands run from `api/`, with `TEST_DATABASE_URL`/`TEST_REDIS_URL` pointing only
to the isolated test services:

```sh
go test -p 1 ./internal/workoutprogress ./internal/activity ./internal/wodeval -count=1 -ginkgo.no-color -ginkgo.succinct
go test -v -p 1 ./internal/worker ./internal/controllers ./internal/fatigue ./cmd/server ./cmd/worker -count=1 -ginkgo.no-color -ginkgo.succinct -ginkgo.focus='Independent workout progress|Workout summary source fingerprint|Workout progress observation windows|buildAnalysisPrompt|open-set movement prompts|Recovery Workout Analysis|HandleSessionDebugReanalysisTask|Analysis enrichment lifecycle|Live feedback worker integration|WOD full-session evaluation|WOD start|ComputeSessionMuscleLoads|parseSessionScore|HandleVideoAnalysisTask|workout-progress|performance-records|performance-benchmarks|apply progress|reanalysis|WOD training load|/wod-training-load|/effort'
go test -v -p 1 ./internal/controllers ./internal/worker -count=1 -ginkgo.no-color -ginkgo.succinct -ginkgo.focus='(?i)reanal|workout-progress|performance-records|performance-benchmarks|/effort|wod-training-load'
```

Root commands: `npm test -- --runInBand shared/workoutProgress.test.ts features/wod/ui/WorkoutProgressCard.test.tsx features/wod/ui/WorkoutVideoPlayer.test.tsx features/wod/api.test.ts features/wod/__tests__/liveFeedback.test.ts features/wod/__tests__/useLiveWorkoutFeedback.test.tsx web/test/workoutProgress.test.tsx web/test/analysis-presentation.test.ts`, `npm run typecheck`, `npm run generate-types`, and `git diff --check`. Web: `npm run build`. Migration: `migrate -path internal/db/migrations -database "$TEST_DATABASE_URL" down 1` followed by `up` against the isolated migrated database only.

## Bounded coaching restoration verification (2026-10-14)

The user's requested restoration passed 52 selected worker specs and 12 mobile
feedback/hook tests plus typecheck. Both chunk handlers are tested with the switch
on/off: prior coaching enters only the text postprocessor, independent video input
excludes it, raw repetition evidence and `workout_progress` remain unchanged.
Saved-chunk resumption generates only missing optional coaching; subsequent
delivery preserves the observation fingerprint and skips already saved coaching.
Localized display requires the current chunk ID and falls back to basic output.
No feature defaults, deployment or full-history real-time analysis were changed.

Commands (Go from `api/`, using isolated test DB/Redis with mocked external APIs):

```sh
go test -v -p 1 ./internal/worker -count=1 -ginkgo.no-color -ginkgo.succinct -ginkgo.focus='Live coaching evidence window|Live feedback worker integration|Independent workout progress helpers|HandleSessionDebugReanalysisTask|HandleVideoAnalysisTask'
npm test -- --runInBand features/wod/__tests__/liveFeedback.test.ts features/wod/__tests__/useLiveWorkoutFeedback.test.tsx
npm run typecheck
git diff --check
```
