# Phase 2: Observability and Evaluation

Priority: P0 before model/cost rollout
Goal: make every proposed optimization measurable by session, stage, variant, attempt, and evidence coverage.

Phase 2 may begin while late Phase 1 tests are finishing, but no model, media-resolution, FPS, thinking, or skip-rule change may be promoted until the baseline report is complete.

## Existing foundation and gaps

The repository already has:

- `token_usages` with prompt, candidate, and total tokens;
- `pipeline_stage_metrics` with stage, variant, API calls, skipped calls, upload bytes, and duration;
- `saveTokenUsage` and `recordStageMetrics` helpers in `api/internal/worker/worker.go`.

Current gaps:

- no thinking or cached-content tokens;
- no media seconds, FPS, media resolution, or thinking level per call;
- no task attempt, outcome, error class, parse result, or actual successful segment count;
- real-time and synthetic split calls are not consistently distinguishable;
- stage metrics are mostly written only on success;
- no estimated cost report tied to a dated pricing snapshot;
- no annotated evaluation set or repeatable variant runner;
- `compare` is user-selectable in the web upload page even though the video path does not actually execute both variants.

## PR 2A — Complete call and task telemetry

Work items: OBS-01, OBS-02, OBS-03

### Files to inspect/change

- `api/internal/gemini/client.go`
- `api/internal/worker/worker.go`
- Every Gemini call site under `api/internal/worker/`
- `api/internal/db/db.go`
- `api/internal/db/migrations/000019_create_token_usages.up.sql` and `000036_create_pipeline_stage_metrics.up.sql` for current shape only; create a new migration pair for changes
- Worker tests and `api/internal/gemini/client_test.go`

### OBS-01 — Complete usage and cost inputs

Extend `gemini.TokenUsage` and `db.TokenUsage` additively. At minimum persist:

| Field | Source/meaning |
|---|---|
| `prompt_tokens` | Existing `PromptTokenCount`. |
| `candidate_tokens` | Existing `CandidatesTokenCount`. |
| `thought_tokens` | `UsageMetadata.ThoughtsTokenCount`. Thinking is billed as output. |
| `cached_content_tokens` | `UsageMetadata.CachedContentTokenCount`. |
| `total_tokens` | Existing total; retain for reconciliation. |
| `model` | Exact model ID sent, not a friendly alias. |
| `stage` | Stable enum such as `chunk_realtime`, `chunk_split`, `video_index`, `video_triage`, `video_segment`, `video_synthesis`, `injury`, `highlight_verify`, `tts`. |
| `variant` | `current`, `candidate_name`, or experiment variant. Do not overload pipeline mode. |
| `attempt` | Zero-based task retry count. |
| `call_index` | Zero-based call within this task attempt. |
| `outcome` | `success`, `transport_error`, `model_error`, `empty`, `invalid_output`, `canceled`. |
| `latency_ms` | Around the GenerateContent request only; upload/poll is a separate stage event. |
| `media_seconds` | Duration actually included in this request, not whole session duration for a clipped segment. |
| `media_resolution` | `low`, `medium`, `high`, or SDK default. |
| `fps` | Explicit FPS or documented default value. |
| `thinking_level` | Explicit setting or the known model default recorded as `default_medium`, `default_high`, etc. |

Keep raw usage immutable. Do not hard-code a dollar amount into a row without also storing a pricing version, because rates change. Implement the cost report with a versioned rate map whose output includes:

- pricing source URL;
- pricing snapshot date;
- model and inference class (standard/batch/flex/priority);
- input, cached input, candidate, and thought-token subtotals;
- per-stage and per-session cost.

The production real-time path uses standard inference unless code proves otherwise; do not apply batch/flex rates to it.

When the SDK returns no usage on a failed request, write a stage event with outcome/error class; do not fabricate zero-token success.

### OBS-02 — Task and stage lifecycle

Extend `pipeline_stage_metrics` or create a narrowly named `pipeline_stage_events` table if the aggregate row cannot represent attempts. Do not introduce a generic event framework.

Required fields:

| Field | Requirement |
|---|---|
| `session_id`, `profile_id` | Existing ownership dimensions. |
| `task_type` | Exact Asynq type. |
| `stage` | Stable stage enum. |
| `variant` | Experiment configuration name. |
| `attempt` | Retry count. |
| `outcome` | `success`, `partial`, `retryable_error`, `terminal_error`, `skipped`. |
| `error_class` | Low-cardinality class; never raw error text. |
| `duration_ms` | Stage wall time. |
| `queue_wait_ms` | Enqueue-to-start time. Add an enqueue timestamp to the task payload or persisted PENDING record if Asynq context does not expose it. |
| `upload_bytes`, `download_bytes` | Direction-specific bytes. |
| `media_seconds` | Media processed by the stage. |
| `api_calls`, `skipped_calls` | Actual, including split calls. |
| `planned_items`, `successful_items`, `failed_items` | Chunks/segments as appropriate. |

Write an event from a `defer` so success, error, cancel, timeout, and panic-recovery paths are all observable. Metrics failure remains non-blocking, but log it with a low-cardinality stage and session ID.

Add explicit events for:

- GCS download;
- FFprobe/motion probe;
- FFmpeg split, concat, and analysis encode;
- Gemini Files upload and ACTIVE polling;
- each generation stage;
- database persistence;
- each downstream enqueue;
- hardsub/TTS/highlight generation.

Do not log full Gemini responses, prompts, profile text, signed URLs, raw GCS lists, or analysis output at info level. Log request/stage identifiers, sizes, counts, usage, latency, and output length. Keep detailed content only in controlled debug environments with redaction.

### OBS-03 — Evidence and validation quality

Persist or derive these per analysis:

- expected, available, completed, and failed chunk counts;
- capture duration and merged media duration;
- interval-union coverage and uncovered gaps;
- observed movement count and deep-analyzed movement count;
- planned, attempted, successful, and failed deep segments;
- structured-output schema validation success;
- semantic validation failures by category: range, enum, overlap, duplicate, score, formula, target ambiguity;
- number of abstentions;
- highlight verification pass/fail per claim, not only one session boolean;
- terminal public status and internal stage.

Use the Phase 1 `coverage_json` as the single persisted coverage contract. Metrics should be derived from it rather than reimplementing a second coverage algorithm.

### Telemetry tests

- SDK fixture containing thought/cache counts maps every field correctly.
- Missing usage metadata yields no fake usage record and does yield a failed stage event.
- A retried task emits separate attempt rows.
- A partial segment run records actual successes, not selected count.
- Logs captured in tests do not contain prompt text, full model output, signed URLs, profile demographics, or raw injury details.
- Cost-report fixture reconciles input + candidate + thought tokens using a pinned pricing snapshot.

## PR 2B — Annotated evaluation set and runner

Work item: OBS-04

### Dataset storage and privacy

Do not commit real member videos to Git. Commit only:

- `api/testdata/video-eval/README.md` with acquisition, consent, redaction, and access rules;
- a JSON Schema for annotations;
- a de-identified manifest with fixture IDs and approved protected-object references;
- tiny synthetic/consented clips only if repository policy explicitly allows them.

If evaluation videos are placed in the product bucket, preserve the required layout: `videos/{testProfileId}/{evaluationSessionId}/{filename}`. Use dedicated test profiles and session IDs, least-privilege access, and an approved retention policy. Never mix evaluation labels with production user endpoints.

Minimum coverage categories:

- no exercise / setup / walking;
- single movement and multiple movements;
- multi-person gym with target ambiguity;
- occlusion and target leaving/re-entering frame;
- camera motion or camera repositioning;
- low light and poor angle;
- rapid Olympic lifts and quick transitions;
- static holds or slow controlled movement;
- fatigue across early/middle/late workout;
- visible risky mechanics positive cases;
- matched negative safety cases where no risky mechanic is visible;
- rest between two sets of the same movement;
- Android-style capture gaps and variable/keyframe split durations.

### Annotation schema

Each fixture must include:

```json
{
  "fixture_id": "eval-001",
  "duration_secs": 63.4,
  "conditions": ["multi_person", "fast_motion"],
  "target": {"visible_ranges": [{"start_secs": 0, "end_secs": 63.4}]},
  "movements": [
    {
      "canonical_id": "snatch",
      "start_secs": 10.2,
      "end_secs": 24.7,
      "rep_count": 3,
      "target_confirmed": true
    }
  ],
  "rest_ranges": [{"start_secs": 24.7, "end_secs": 34.1}],
  "visible_risk_events": [],
  "not_assessable_ranges": [],
  "notes": "reviewer-visible context only"
}
```

Use two qualified reviewers for movement intervals, target attribution, and safety events. Resolve disagreements and record reviewer agreement. Do not label pain, diagnosis, readiness, or internal physiology from pixels.

### Runner requirements

Create an offline/staging runner that accepts:

- manifest path;
- exact variant configuration;
- repeat count;
- output directory or DB experiment key;
- dry-run mode;
- maximum cost budget.

Pin and record:

- Git SHA;
- exact model IDs;
- prompt/schema version hashes;
- FPS, media resolution, thinking level, segment budget, and gating rules;
- run date and pricing snapshot;
- every raw usage count and latency.

Run each nondeterministic model variant at least three times on the same fixture set. Never compare a single candidate run with a single historical run.

### Required metrics

| Dimension | Definition |
|---|---|
| Movement detection | Macro precision/recall/F1 by canonical movement. A match requires the same movement and interval IoU at or above the predeclared threshold (start with 0.5). |
| No-exercise behavior | False-positive rate per labeled no-exercise chunk. |
| Target attribution | Fraction of predictions assigned to a non-target person; separately report ambiguous/abstained cases. |
| Time localization | Start/end absolute error and interval IoU. |
| Rep count | Mean absolute error, reported only where the view supports counting. |
| Repeatability | Agreement/variance across repeated runs of the same fixture/configuration. |
| Evidence validity | Fraction of claims with supporting labeled time range; semantic-validation failure rate. |
| Safety | Visible-risk precision/recall and unsupported safety-claim rate. Weight false reassurance and diagnoses as critical errors. |
| Abstention | Coverage, accuracy-at-coverage, Brier score or expected calibration error for confidence-bearing outputs. |
| Coaching quality | Blinded human rubric for factuality, specificity, actionability, and consistency with visible evidence. |
| Cost | Mean/p50/p95 estimated cost per session and per media minute, split by stage. |
| Latency | Real-time chunk p50/p95 and full-session completion p50/p95. |

## PR 2C — Safe experiment/compare mechanism

Work item: OBS-05

### Correct current compare behavior

- `web/src/upload/UploadPage.tsx` currently defaults to `compare`; change the user-facing default to the production variant after product confirmation.
- Do not label video compare as “legacy and optimized in parallel” while `HandleVideoAnalysisTask` routes compare only to optimized.
- Do not show two variant outputs to members by default.

### Target experiment behavior

Prefer the offline runner for expensive/full comparisons. For sampled production shadow evaluation:

1. Select a small deterministic sample using a hash of session ID and an experiment seed.
2. The control path remains the only user-visible result.
3. Candidate work runs in a low-priority queue and must not delay control work.
4. Persist variant output/metrics in an internal experiment record keyed by `(session_id, experiment_key, variant)`; do not compete for the unique public `analysis_results.session_id` row.
5. Exclude hardsub, TTS, injury, and highlight side effects from the candidate unless that stage is the experiment target.
6. Apply a per-day cost ceiling and kill switch.
7. Delete or redact candidate raw output according to the approved evaluation retention policy.

Do not call the same expensive stage twice merely to collect cost metrics that can be obtained from the control request's usage metadata.

## Baseline report

Before Phase 3, commit or attach a dated baseline report containing:

- dataset version and category counts;
- control Git SHA/configuration;
- all metrics above with confidence intervals where applicable;
- cost/session and latency by stage;
- parse/semantic-validation failures;
- chunk and deep-analysis coverage;
- known dataset blind spots;
- proposed numerical rollout gates for the next experiment.

The rollout gates must be filled with baseline-derived numbers. Do not leave “no regression” undefined. At minimum, any candidate must have:

- no critical unsupported diagnosis/pain/readiness claims;
- no target-attribution regression on multi-person fixtures;
- no increase in no-exercise false positives outside the predeclared bound;
- movement and timestamp metrics within predeclared non-inferiority bounds;
- a statistically and operationally meaningful cost or latency improvement.

## Phase 2 release gate

- [ ] Every Gemini call records exact model, stage, attempt, media configuration, raw token classes, latency, and outcome.
- [ ] Every task/stage records errors and retries, not only success.
- [ ] Coverage and semantic validation are queryable.
- [ ] Logs are content-minimized.
- [ ] The evaluation manifest covers all required categories and has reviewer agreement.
- [ ] Control baseline has at least three repeated runs per fixture.
- [ ] Production compare is internal/sampled and side-effect free.
- [ ] Phase 3 numerical gates are written before candidate rollout.
