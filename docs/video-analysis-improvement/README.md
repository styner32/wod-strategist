# Video Analysis Improvement Program

Status: implementation-ready review plan
Reviewed against the repository: 2026-07-11
Scope: real-time 10-second chunk analysis and post-workout full-video analysis

## Purpose

This directory converts the video-analysis review into small, ordered work packages that another agent can implement without having to rediscover the pipeline. It is intentionally prescriptive about sequencing, files, behavior, tests, and acceptance criteria.

The program has three goals:

1. Make session finalization, timestamps, retries, and persistence correct.
2. Measure accuracy, latency, and cost before changing model behavior.
3. Reduce cost and latency without silently lowering coaching or safety quality.

Do not begin with model swapping or aggressive skip rules. The current pipeline has integrity defects that can make an accuracy or cost comparison invalid.

## Repository constraints

Every implementation agent must follow:

- [`AGENTS.md`](../../AGENTS.md)
- [`api/AGENTS.md`](../../api/AGENTS.md) for backend changes
- [`app/AGENTS.md`](../../app/AGENTS.md) for React Native changes
- [`docs/agent-memory/backend-testing.md`](../agent-memory/backend-testing.md)
- [`docs/agent-memory/migrations.md`](../agent-memory/migrations.md)
- [`docs/agent-memory/storage-and-session-format.md`](../agent-memory/storage-and-session-format.md)

In particular:

- Use versioned `golang-migrate` SQL. Never use `AutoMigrate`.
- Use Ginkgo/Gomega and the existing real-client test strategy.
- Keep every object under `videos/{profileId}/{sessionId}/`.
- Never put `profile_id` inside the session ID.
- Preserve both current `WOD-...` and legacy `P{id}-WOD-...` session ID formats.
- Do not change dependency versions, infrastructure sizing, or product retention policy unless the corresponding work package explicitly calls for a decision and the user approves it.

## Reading order

| Document | Use |
|---|---|
| [01-current-pipeline.md](01-current-pipeline.md) | Understand the code that exists today and its failure modes. |
| [02-phase-1-integrity-and-idempotency.md](02-phase-1-integrity-and-idempotency.md) | Fix data loss, timeline errors, duplicate work, retry semantics, and task routing. |
| [03-phase-2-observability-and-evaluation.md](03-phase-2-observability-and-evaluation.md) | Establish cost, latency, coverage, and accuracy baselines. |
| [04-phase-3-accuracy-and-contracts.md](04-phase-3-accuracy-and-contracts.md) | Add structured evidence, safer prompts, correct segmentation, and final synthesis. |
| [05-phase-4-cost-and-throughput.md](05-phase-4-cost-and-throughput.md) | Run measured model, media, upload, queue, storage, and post-processing optimizations. |
| [06-test-matrix-and-rollout.md](06-test-matrix-and-rollout.md) | Apply cross-phase test, deployment, rollback, and definition-of-done rules. |

## Required execution order

Each row is a release gate. Do not start a dependent row until the preceding exit condition is met.

| Order | Work package | IDs | Exit condition |
|---:|---|---|---|
| 1 | Reliable mobile finalization and server manifest | REL-01, REL-02 | The final partial chunk is uploaded; merge receives an expected count; missing or failed uploads prevent merge instead of producing an incomplete video. |
| 2 | Correct media timeline | REL-03, REL-04 | All post-workout timestamps use probed media durations; gap and keyframe-split cases pass. |
| 3 | Exact routing and idempotent state | REL-05, REL-06 | The session-aware task reaches its handler; duplicate delivery produces one logical chunk/result and no stale `FAILED` result. |
| 4 | Failure, retry, and follow-up semantics | REL-07, REL-08 | Partial coverage is visible and retryable; explicit per-task retry/timeout policy exists; downstream enqueue failure cannot be silently lost. |
| 5 | Baseline instrumentation and evaluation set | OBS-01 through OBS-05 | Cost/session, latency, task outcome, parse failure, timeline coverage, and annotated accuracy metrics can be compared by variant. |
| 6 | Output and safety contracts | ACC-01 through ACC-04 | Structured responses validate; unknown profile attributes are omitted; uncertain target/injury claims abstain. |
| 7 | Segmentation and session synthesis | ACC-05 through ACC-08 | Rest boundaries and movement coverage are preserved; a final synthesis uses all successful evidence and validated scoring. |
| 8 | Cost and throughput experiments | COST-01 through COST-09 | Candidate changes pass the evaluation gates and are rolled out behind configuration. |

## Work-item index

| ID | Summary | Priority | Primary document |
|---|---|---:|---|
| REL-01 | Upload the final partial chunk and await all uploads | P0 | Phase 1 |
| REL-02 | Make an expected chunk manifest/count authoritative | P0 | Phase 1 |
| REL-03 | Separate capture time from merged-media time | P0 | Phase 1 |
| REL-04 | Derive split-video offsets from actual durations | P0 | Phase 1 |
| REL-05 | Register the exact session-aware Asynq handler | P0 | Phase 1 |
| REL-06 | Make chunk and full-result persistence idempotent | P0 | Phase 1 |
| REL-07 | Represent partial coverage and terminal failure correctly | P0 | Phase 1 |
| REL-08 | Define task retries, timeouts, queues, and durable follow-ups | P0/P1 | Phase 1 |
| OBS-01 | Record complete token and estimated-cost metadata | P0 | Phase 2 |
| OBS-02 | Record task attempts, stages, latency, and outcomes | P0 | Phase 2 |
| OBS-03 | Record analysis coverage and validation quality | P0 | Phase 2 |
| OBS-04 | Build a privacy-safe annotated evaluation set | P0 | Phase 2 |
| OBS-05 | Replace user-facing compare work with sampled shadow evaluation | P1 | Phase 2 |
| ACC-01 | Use structured JSON output plus semantic validation | P1 | Phase 3 |
| ACC-02 | Remove fabricated profile defaults | P0 | Phase 3 |
| ACC-03 | Add target-person ambiguity and abstention | P0 | Phase 3 |
| ACC-04 | Limit injury output to visible evidence | P0 | Phase 3 |
| ACC-05 | Canonicalize movements and retain rest boundaries | P1 | Phase 3 |
| ACC-06 | Make triage cover movements and the whole session | P1 | Phase 3 |
| ACC-07 | Add evidence-based final synthesis and score validation | P1 | Phase 3 |
| ACC-08 | Validate and selectively verify highlights | P1 | Phase 3 |
| COST-01 | Add stage-specific model/thinking/media configuration | P1 | Phase 4 |
| COST-02 | Evaluate Flash-Lite routing for simple chunk work | P1 | Phase 4 |
| COST-03 | Reuse the full Gemini file for uploaded-video indexing | P1 | Phase 4 |
| COST-04 | Calibrate motion/confidence gating in shadow mode | P1 | Phase 4 |
| COST-05 | Separate real-time, AI, and media queue capacity | P1 | Phase 4 |
| COST-06 | Make hardsub generation explicit and TTS opt-in | P1 | Phase 4 |
| COST-07 | Bound Gemini Files API polling and cleanup | P0/P1 | Phase 4 |
| COST-08 | Add GCS lifecycle and temporary-artifact cleanup | P1 | Phase 4 |
| COST-09 | Reduce redundant media, prompt, log, and polling work | P1/P2 | Phase 4 |

## Implementation protocol for another agent

For each work item:

1. Read the listed code paths and the applicable agent-memory documents.
2. Confirm the previous work item's exit condition on the current branch.
3. Write or update tests first where the defect can be reproduced deterministically.
4. Make the smallest implementation diff that satisfies the behavior contract.
5. Run the work item's targeted commands, then the phase-level commands in [06-test-matrix-and-rollout.md](06-test-matrix-and-rollout.md).
6. Update the work-item checkbox and record measured before/after values. Do not mark an empirical optimization complete with only a passing unit test.
7. Use one reviewable PR per row in the execution-order table unless a migration and its compatibility code must ship together.

Every handoff must report files changed, tests added or updated, commands run, migration or deployment order, observed metrics, risks, and rollback procedure.

## Decisions that must not be guessed

The review recommends defaults, but these values require product or measured evidence before production rollout:

| Decision | Recommended starting point | Required evidence/owner |
|---|---|---|
| Raw and derived video retention | Keep merged user video per current product policy; expire temporary split/analysis artifacts after successful downstream completion plus a short recovery window. | Product/privacy approval and recovery requirements. |
| Flash-Lite routing threshold | Start in shadow mode on low-risk, high-confidence chunks; no user-visible switch initially. | Evaluation-set non-inferiority and safety review. |
| Motion skip threshold | Do not hard-code one. Collect distributions and label false skips first. | OBS-04 baseline; zero unacceptable injury/static-hold skips. |
| Partial full-analysis UX | Prefer explicit `PARTIAL` with coverage over falsely reporting `COMPLETED`. | Product copy and retry behavior approval. |
| Accuracy rollout gates | Establish the current baseline first; then require non-inferior safety/target attribution and bounded movement/timestamp regression. | Evaluation owner signs off. |
| Hardsub default | Recommended off/on-demand; TTS remains opt-in. | Product confirmation because this changes delivered artifacts. |
| Queue concurrency and FFmpeg slots | Derive from actual CPU/memory and Gemini quota; begin with FFmpeg concurrency near available vCPU. | Load test in the deployed worker environment. |

## Program completion criteria

This program is complete only when:

- A stopped session cannot silently omit an uploaded or final partial chunk.
- All post-workout timestamps map to the merged video's media timeline.
- Duplicate delivery is safe at chunk, result, and follow-up stages.
- `COMPLETED`, `PARTIAL`, and `FAILED` reflect actual evidence coverage.
- Every Gemini stage reports model, media seconds, input/output/thinking/cache tokens, latency, attempt, and outcome.
- Accuracy and cost variants can be evaluated without showing duplicate results to users.
- Target identity, injury, and highlight claims are evidence-bound and can abstain.
- Cost changes have before/after measurements and documented rollback switches.
