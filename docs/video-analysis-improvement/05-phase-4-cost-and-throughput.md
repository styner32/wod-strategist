# Phase 4: Cost, Throughput, and Resource Efficiency

Priority: P1 after baseline
Goal: reduce model, upload, compute, storage, and polling cost while preserving the Phase 3 evidence/safety contract.

Every change in this phase starts as a named candidate variant. Promote only one major variable at a time unless the experiment is explicitly factorial and budgeted.

## Current external reference points

Verified on 2026-07-11; re-check before implementation because prices and model behavior change.

- [Gemini API pricing](https://ai.google.dev/gemini-api/docs/pricing): standard `gemini-3.6-flash` is listed at $1.50 per million input tokens and $9.00 per million output tokens including thinking; standard `gemini-3.5-flash-lite` is $0.25 input and $1.50 output/thinking.
- [Gemini video understanding](https://ai.google.dev/gemini-api/docs/video-understanding): default visual sampling is 1 FPS, which can miss rapid motion; default video is approximately 300 tokens/second including audio, while low resolution is about 100 tokens/second.
- [Gemini media resolution](https://ai.google.dev/gemini-api/docs/media-resolution): low/medium general video is approximately 70 tokens/frame, high approximately 280; high is primarily recommended for text-heavy/small-detail video.
- [Gemini thinking](https://ai.google.dev/gemini-api/docs/thinking): `gemini-3.6-flash` defaults to medium thinking and `gemini-3.1-pro-preview` defaults to high unless configured.
- [Gemini Files API](https://ai.google.dev/gemini-api/docs/files): temporary files are stored for 48 hours, with a 20 GB project total and 2 GB per-file maximum.
- [Gemini file input methods](https://ai.google.dev/gemini-api/docs/file-input-methods): GCS registration is a possible future way to avoid copying eligible GCS objects into Files API.

Illustrative lower-bound input cost for a 10-minute video at the documented default rate:

```text
600 seconds * ~300 tokens/second = ~180,000 input tokens
Gemini 3.6 Flash standard input: 0.18 * $1.50 = ~$0.27
Gemini 3.5 Flash-Lite standard input: 0.18 * $0.25 = ~$0.045
```

This excludes prompt, output, thinking, repeated segment calls, uploads, Pro calls, and follow-ups. Use actual `UsageMetadata`, not this estimate, for decisions.

## PR 4A — Stage-specific AI configuration

Work items: COST-01, COST-02

### Files to inspect/change

- `api/internal/config/config.go` and tests
- `api/cmd/worker/main.go`
- `api/internal/gemini/client.go`
- `api/internal/worker/worker.go` and call sites
- Infrastructure environment declarations only after configuration is tested

### COST-01 — Make stage settings explicit

Replace the single implicit Flash constant and one global Pro model with validated stage configuration. Suggested names:

```text
GEMINI_MODEL_CHUNK
GEMINI_MODEL_INDEX
GEMINI_MODEL_SEGMENT
GEMINI_MODEL_SYNTHESIS
GEMINI_MODEL_INJURY
GEMINI_MODEL_VERIFY

GEMINI_THINKING_CHUNK
GEMINI_THINKING_INDEX
GEMINI_THINKING_SEGMENT
GEMINI_THINKING_SYNTHESIS
GEMINI_THINKING_INJURY
GEMINI_THINKING_VERIFY

GEMINI_MEDIA_RESOLUTION_CHUNK
GEMINI_MEDIA_RESOLUTION_INDEX
GEMINI_MEDIA_RESOLUTION_SEGMENT
GEMINI_MEDIA_RESOLUTION_VERIFY

GEMINI_FPS_CHUNK
GEMINI_FPS_SEGMENT
GEMINI_MAX_OUTPUT_TOKENS_CHUNK
GEMINI_MAX_OUTPUT_TOKENS_SEGMENT
GEMINI_MAX_OUTPUT_TOKENS_SYNTHESIS
```

Requirements:

- Validate enums/ranges at worker startup.
- A missing variable preserves and records the current control behavior; do not silently change all stages in the configuration PR.
- Every usage row records the resolved value.
- Stage methods accept a typed config object rather than reading environment variables in hot paths.
- Use explicit output-token caps sized to the structured schema; truncation is a failed output, not a partial success.
- Do not change temperature/top-p/top-k for Gemini 3 without evaluation; current Google guidance recommends defaults.

Create named candidate presets in test/evaluation configuration, not hidden conditionals in prompts.

### FPS/media experiments

The current short-chunk Files call has default video processing, while rapid lifts can be missed at 1 FPS. Do not assume lower FPS is always cheaper and accurate.

Evaluate:

| Variant | Chunk FPS | Chunk resolution | Purpose |
|---|---:|---|---|
| control | SDK/default | SDK/default | Current baseline. |
| fast-motion-4 | 4 | medium/low | More temporal samples at general-video token density. |
| fast-motion-5 | 5 | medium/low | Match current deep-segment FPS. |

For index and highlight verification, compare current high resolution with low/medium. Workout video is generally not text-heavy, so high should remain only if the evaluation set demonstrates a material small-detail gain.

Report actual tokens and latency because FPS and resolution interact. Do not calculate cost from FPS alone.

### Thinking experiments

Evaluate minimal/low for classification, indexing, and verification; low/medium for evidence synthesis; and low/medium/high only where deep biomechanics quality justifies it. Pro cannot use minimal. A schema-valid output is not proof that lower thinking preserved factuality.

### COST-02 — Flash-Lite routing

Add `gemini-3.5-flash-lite` as a stage candidate, beginning with the structured real-time chunk contract.

Rollout sequence:

1. Offline evaluation on every fixture, three repeats.
2. Shadow only; candidate output has no user or follow-up side effects.
3. Limited production sample with cost ceiling.
4. User-visible routing only after movement, target, no-exercise, timestamp, and safety gates pass.

Initial escalation conditions should be expressed in validated output/config, not hard-coded guesses:

- invalid response after bounded repair;
- target ambiguous/not visible when a decision is required;
- movement confidence below a threshold chosen from calibration data;
- multiple plausible people;
- visible-risk flag or known-injury workflow;
- unsupported/unknown movement requiring deeper analysis.

During evaluation, send known-injury and safety-critical fixtures directly to the stronger control so Flash-Lite cannot become an unmeasured safety gate. If a Lite-first-plus-escalation route costs more than direct Flash for common cases, reject it.

## PR 4B — Eliminate direct-upload N+1 media lifecycles

Work item: COST-03
Likely highest direct-upload saving

### Current issue

For a direct upload, the optimized handler uploads the full video to Gemini Files, then `splitAndAnalyzeChunks` can create approximately one local/GCS/Gemini upload and Flash generation per 10-second interval. A 20-minute video can produce about 120 additional file and generation lifecycles before deep analysis.

The media duration is not multiplied by 120, but prompts, output/thinking, upload processing, GCS objects, cleanup, failure surface, and API operations are.

### Candidate modes

Keep the current path as `split_upload_control`. Evaluate in this order:

1. `full_index_once`: one structured low/medium-resolution Flash index against the already-uploaded full file, then deep analysis of selected ranges.
2. `range_scan_reuse`: if one index is insufficient, classify nominal ranges against the same full Files URI using `VideoMetadata`, without creating local split files, GCS split objects, or Files uploads.
3. Use the current split-upload path only as fallback/control until non-inferiority is established.

Add a Gemini client method that accepts existing file URI, model, range, FPS, media resolution, thinking level, and schema. Do not re-upload when an active full-file URI exists.

Synthetic evidence rows should use `chunk_source='synthetic_range'`, share the original source URI, and remain unique by `(profile_id, session_id, chunk_index)`. The recorded-chunk file-path unique index from Phase 1 must be partial to `chunk_source='recorded'`; never invent nonexistent split GCS URIs.

Compare:

- movement/timestamp/rep metrics;
- pass-1 coverage;
- number of GCS and Files operations;
- total input/output/thought tokens;
- wall time and retry rate;
- temporary storage bytes.

Do not delete `splitAndAnalyzeChunks` until the candidate passes and rollback has been exercised.

### Future GCS registration

The pinned Go SDK is `google.golang.org/genai v1.43.0`. The current implementation uses Files upload. GCS registration may remove another copy, but dependency changes are explicitly out of scope unless requested. Track this as a separate upgrade/compatibility task:

1. Verify the pinned SDK exposes the registration API and Gemini project/auth requirements.
2. Confirm service-account permissions and file limits.
3. Add integration tests and a Files-upload fallback.
4. Compare processing latency and cleanup semantics.

Do not upgrade the SDK inside this optimization PR.

## PR 4C — Calibrated skip/routing signals

Work item: COST-04

`WorkoutConfidence` and FFmpeg `MotionScore` are currently generated/persisted but do not reduce calls. Treat them as candidate features, not truth.

### Shadow calibration

For every evaluated chunk, record both scores and the validated model/human label. Plot distributions for:

- no exercise;
- normal dynamic movement;
- rapid movement;
- static holds;
- low light/occlusion;
- camera motion;
- visible-risk cases.

Choose thresholds only after measuring false skips. Require both independent signals to support a no-workout decision. A single low score never hard-skips.

### Safety rules

- Never hard-skip an uncertain chunk.
- Never hard-skip a known-injury workflow based only on motion/confidence.
- Protect static holds and slow strength work explicitly.
- Audit a random sample of would-skip chunks by still sending them to the model.
- Record skip reason, threshold version, features, and audit result.
- Make the gate an immediate configuration kill switch.

Promote only if audited false-skip rate is inside the Phase 2 bound and cost reduction remains material after audit/escalation calls.

## PR 4D — Queue and resource isolation

Work item: COST-05

### Current issue

The deployed worker is configured for 2 vCPU and 8 GiB in `infra/compute.tf`. One Asynq server has concurrency 10. A full-analysis task can additionally run 10 split goroutines, so multiple tasks can create roughly 100 concurrent probes/uploads/generations without a process-wide limit. Merge, FFmpeg, real-time, full analysis, and hardsub compete in the same pool.

### Target queues

| Queue | Tasks | Service objective |
|---|---|---|
| `realtime` | `chunk:analysis`, `chunk:analysis-with-session` | Protect p95 live-feedback latency. |
| `analysis` | `video:analysis`, `injury:analysis`, highlight verification | Throughput with bounded Gemini concurrency. |
| `media` | merge, hardsub, highlight generation | CPU/memory-bound and lower priority. |

Weighted queues alone do not guarantee a real-time slot. Prefer separate Asynq server pools/concurrency per queue within the process or separate deployments if load proves necessary.

Add process-wide limiters:

```text
WORKER_CONCURRENCY_REALTIME
WORKER_CONCURRENCY_ANALYSIS
WORKER_CONCURRENCY_MEDIA
GEMINI_MAX_CONCURRENCY
FFMPEG_MAX_CONCURRENCY
```

Recommended staging start for the current 2-vCPU worker: FFmpeg maximum 1, then load-test realtime 4 / analysis 2 / media 1 and a Gemini cap no greater than the sum of active non-media slots. These are test values, not a production guarantee.

Make split/range concurrency use the shared Gemini limiter instead of a task-local semaphore only. Record limiter wait time. Test shutdown across all server pools and ensure task constructors set the intended queue explicitly.

Acceptance:

- hardsub/merge load does not breach real-time p95 gate;
- no OOM during representative longest videos;
- Gemini 429/retry rate does not increase;
- CPU/memory, queue depth/age, peak concurrent Gemini calls, peak FFmpeg processes, and limiter wait are observable;
- configuration rollback restores the prior single-pool mode.

## PR 4E — Optional post-processing and lifecycle

Work items: COST-06, COST-07, COST-08

### COST-06 — Make hardsub explicit

Current full analysis auto-enqueues hardsub even when `EnableTTS=false`. TTS controls narration only, not the full-video subtitle re-encode.

Add a separate `enable_hardsub` request/payload setting:

- recommended default: false/on-demand, pending product confirmation;
- `enable_tts` remains false unless requested and implies hardsub only if product chooses that UX;
- checkpoint logic from Phase 1 marks hardsub `NOT_REQUIRED` when disabled;
- explicit hardsub endpoint remains idempotent.

Measure full-video download bytes, FFmpeg time, output bytes, and storage per hardsub. Do not count only TTS tokens.

Replace unbounded `cmd.CombinedOutput()` on long FFmpeg jobs with streamed stderr plus a bounded tail for error reporting to avoid retaining large logs in memory.

### COST-07 — Bound Files polling and cleanup

Update `api/internal/gemini/client.go`:

- polling waits must select on context cancellation rather than uninterruptible `time.Sleep`;
- add a validated maximum processing/poll duration;
- bound each HTTP operation with an appropriate context/transport timeout and record poll-attempt count; do not apply one short global timeout that breaks legitimate large uploads;
- retain and clean up the returned file name on upload/poll failure;
- install cleanup ownership immediately after a name exists, not only after ACTIVE success;
- use a detached cleanup context with its own short timeout when the task context is canceled;
- make delete idempotent/not-found-safe;
- record upload, poll, and delete outcomes separately.

Suggested starting configuration: `GEMINI_FILE_PROCESSING_TIMEOUT=5m` and `GEMINI_FILE_DELETE_TIMEOUT=30s`, then tune from observed p95. Long videos that legitimately need more processing must be covered by staging tests before lowering/raising bounds.

Files retained for injury reuse must still have an owner and expiry checkpoint. The database's 47-hour timestamp is a safety margin, not a substitute for explicit deletion when work completes.

### COST-08 — GCS artifact lifecycle

Classify objects without violating the required session prefix:

- user/policy assets: `merged.mp4`, delivered highlights, explicitly retained hardsub;
- recoverable intermediates: recorded chunks, `analysis.mp4`, synthetic split chunks, temporary audio/subtitle artifacts.

Add application cleanup only after the required downstream stages have reached terminal states and the product-approved recovery window has elapsed. Add `DeleteObject` to the narrow storage client interface and test through the existing GCS mock transport.

Do not add a bucket-wide age rule that can delete user videos. If using GCS lifecycle as a safety net, tag temporary objects with metadata/custom time and confirm the rule can distinguish them. Keep all paths under `videos/{profileId}/{sessionId}/`; a subdirectory inside that prefix is allowed, a new top-level prefix is not.

Retention duration is a product/privacy decision listed in the program README. Record deleted object count/bytes and make cleanup idempotent.

## PR 4F — Smaller recurring costs

Work item: COST-09

Implement and measure separately:

1. Fix the size-threshold mismatch. Replace `1000 << 20 // 500 MB` with a clearly named/configured byte value. Preserve 1000 MiB as control until product/measurements choose a threshold.
2. Test analysis encode variants:
   - current width-720, 30 FPS, 64 kbps audio;
   - height-720 (`scale=-2:720`);
   - no audio (`-an`) when no stage consumes audio;
   - lower source FPS only if 5-FPS model sampling accuracy is preserved.
3. Remove full-video high-resolution triage in favor of deterministic/text-only evidence ranking from Phase 3.
4. Keep segment prompts compact; return structured evidence and one synthesis instead of repeated long prose/highlight/score contracts.
5. Set stage output caps and record truncation.
6. Cache/load profile and session context once per task, not repeatedly per segment.
7. Add `(profile_id, session_id, status, media_start_secs)`/query indexes only after `EXPLAIN` or observed query data justifies them.
8. Add cursor/latest-only chunk polling: recording UI requests results after the last seen ID; player/history may request the full set once.
9. Log metadata and output length, not full responses/profile/analysis at info level.
10. Avoid redundant local/GCS copies when `merged.mp4` already exists and matches the intended source.

Each item needs before/after token, byte, CPU, DB/query, or latency evidence. Do not bundle all ten into an unmeasurable “optimization” PR.

## Phase 4 promotion gate

For every candidate, attach:

- control/candidate exact configuration and Git SHA;
- evaluation dataset version and repeat count;
- per-stage and per-session cost;
- p50/p95 real-time and full latency;
- movement, no-exercise, target, timestamp, safety, abstention, and coaching metrics;
- parse/coverage/retry rates;
- resource and rate-limit effects;
- rollback setting and successful rollback test.

No candidate is promoted solely because it is cheaper. Safety and target-attribution gates are blocking, and movement/timestamp quality must remain within the predeclared non-inferiority bounds.
