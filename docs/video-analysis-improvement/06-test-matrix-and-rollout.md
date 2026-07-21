# Test Matrix and Rollout Rules

This document applies to every implementation phase. Passing unit tests alone is not enough for changes involving cameras, FFmpeg, queues, model behavior, or cost.

## Test layers

| Layer | Required coverage | Repository pattern |
|---|---|---|
| Pure unit | Timestamp interval union, manifest validation, canonicalization, schema semantics, score formula, deterministic triage, cost math. | Table-driven Go tests or Jest; no mocks needed. |
| Migration | Up/down pair, existing-row backfill/dedupe, unique constraints, partial indexes, additive compatibility. | `golang-migrate`; never `AutoMigrate`. |
| Controller integration | Auth/ownership, request compatibility, PENDING upsert, duplicate notification, merge count validation. | Router `ServeHTTP`, real test PostgreSQL, one Ginkgo `Describe` per route. |
| Worker integration | GCS, Gemini upload/generation/delete, DB state, queue follow-ups, retries. | Existing four-layer real-client + `MockTransport` strategy. |
| Asynq wiring | Exact task type reaches exact handler; queue/options/retry/timeout are correct. | Exercise the same mux/registration helper as `cmd/worker/main.go`. |
| FFmpeg media | Split/concat durations, keyframe drift, corrupt/zero chunk, variable frame rate, audio/no-audio variants. | Tiny real MP4 fixtures; skip locally only when FFmpeg is absent, fail in CI once CI exists. |
| Mobile unit | Final partial upload, promise drain, failures block merge, index/count serialization, cursor polling. | Jest; isolate the smallest scheduling state if native camera cannot be instantiated. |
| Manual device | Actual iOS/Android camera lifecycle, compression, network delay, backgrounding, local save/playback. | Record device/OS/configuration and evidence. |
| Evaluation | Accuracy/safety/abstention/repeatability/cost/latency against pinned fixtures. | Phase 2 runner, at least three repeats. |
| Load/soak | Queue isolation, Gemini limits, FFmpeg memory, long sessions, retries/restarts. | Staging with deployed-equivalent 2-vCPU/8-GiB limits before concurrency rollout. |

Backend test invocations must not run concurrently: the suites share `wod_test` and Redis DB 15.

## Required scenario matrix

### Capture/finalization

| ID | Scenario | Expected result |
|---|---|---|
| CAP-01 | Stop at 3 seconds | One final partial chunk uploads; expected count 1; merged duration approximates the clip. |
| CAP-02 | Stop immediately after a nominal 10-second boundary | No duplicate/missing index; final callback and next-start race are safe. |
| CAP-03 | 23-second session | Indices 0,1,2; index 2 is partial and included. |
| CAP-04 | Serial queue slower than capture | Stop waits all queued work; merge begins only after acknowledgements. |
| CAP-05 | Concurrent upload completes out of order | Manifest order remains chunk index, not completion time or filename. |
| CAP-06 | Compression resolves after stop | Upload still completes and is counted. |
| CAP-07 | Upload/notification failure | Merge is not called; retry resumes same index without duplicate row/cost. |
| CAP-08 | App backgrounded during finalization | State is retryable and no incomplete merge is accepted. |
| CAP-09 | Android 500 ms cooldown | Capture offsets contain gaps; media offsets do not; playback/evidence aligns. |
| CAP-10 | Legacy app omits new fields | Observable legacy fallback works during compatibility window. |

### Manifest/timeline/media

| ID | Scenario | Expected result |
|---|---|---|
| MED-01 | Missing index in `[0,N)` | Merge retries/fails explicitly; never produces incomplete success. |
| MED-02 | Stale DB row, absent GCS object | Row is not downloaded; integrity error is visible. |
| MED-03 | Extra unreferenced GCS object | Warn/metric and ignore; never append implicitly. |
| MED-04 | Duplicate notification/task | One logical row and one final output. |
| MED-05 | Chunk durations 10.0/10.0/3.2 with capture gaps | Media intervals are 0–10/10–20/20–23.2. |
| MED-06 | Stream-copy split at non-10-second keyframes | Cumulative actual offsets match source duration. |
| MED-07 | Zero/corrupt chunk | No concat/full enqueue; final state follows retry policy. |
| MED-08 | Variable/high frame rate chunks | Merged playback speed and duration are correct. |
| MED-09 | Direct upload already has merged video | No redundant copy/upload. |
| MED-10 | Current and legacy session ID formats | Both validate and keep profile only in path. |

### Task state/retries

| ID | Scenario | Expected result |
|---|---|---|
| TSK-01 | Session-aware type | Specialized handler receives it through production mux wiring. |
| TSK-02 | Chunk transport error then success | PENDING/PROCESSING then one COMPLETED row. |
| TSK-03 | Full error then success | One analysis row ends COMPLETED; no unique-key stale FAILED. |
| TSK-04 | Terminal no-output failure | FAILED only on final attempt; no usable output. |
| TSK-05 | One of several deep segments fails | Retry; after exhaustion PARTIAL with exact coverage. |
| TSK-06 | Crash after analysis persistence | Retry resumes follow-up stage without Gemini replay. |
| TSK-07 | Crash after enqueue before flag update | Duplicate delivery is harmless downstream. |
| TSK-08 | Task timeout/cancel | Temp local/Gemini resources receive bounded cleanup; event outcome is canceled/timeout. |
| TSK-09 | DB write fails | Handler returns error and never reports successful completion. |
| TSK-10 | Redis enqueue fails | Durable checkpoint remains retryable; follow-up is not silently lost. |

### Accuracy/safety

| ID | Scenario | Expected result |
|---|---|---|
| ACC-T01 | No person exercising | No movement/coaching false positive or explicit abstention. |
| ACC-T02 | Multiple athletes | Only target evidence or target_ambiguous; no background attribution. |
| ACC-T03 | Fast Olympic lift | FPS candidates evaluated for movement/timing/rep accuracy. |
| ACC-T04 | Static hold | Motion gate does not skip it. |
| ACC-T05 | Camera motion | No false workout from frame-wide motion. |
| ACC-T06 | Snatch/rest/Snatch | Two segments/rest boundary preserved. |
| ACC-T07 | Same movement alias spellings | One canonical ID; display remains appropriate. |
| ACC-T08 | Invalid/out-of-range model time | Rejected/repair once; never coerced to zero. |
| ACC-T09 | Known injury but no visible issue | No invented fault/diagnosis. |
| ACC-T10 | Occluded risky mechanic | Not assessable rather than confident reassurance. |
| ACC-T11 | More movements than triage budget | Every movement covered or PARTIAL lists omissions. |
| ACC-T12 | Current scoring with history | Current absolute score unchanged by history injection; server formula holds. |
| ACC-T13 | Invalid highlight | Removed/clamped only by documented rule; no unsafe clip command. |
| ACC-T14 | Mixed verified highlights | Failed claims removed/downgraded individually; valid items remain. |

### Cost/resource

| ID | Scenario | Expected result |
|---|---|---|
| CST-T01 | 10-minute direct upload | Candidate avoids per-10-second Files/GCS upload lifecycle and reports operation reduction. |
| CST-T02 | Files polling failure after name assigned | Named file is deleted with bounded detached context. |
| CST-T03 | Hardsub disabled | No hardsub task, video download, encode, upload, or TTS call. |
| CST-T04 | Media queue saturated | Realtime p95 remains within gate. |
| CST-T05 | Multiple full tasks | Shared Gemini/FFmpeg limits prevent nested-concurrency explosion. |
| CST-T06 | Long FFmpeg failure output | Bounded stderr tail; no unbounded in-memory buffer. |
| CST-T07 | Temporary cleanup repeated | Idempotent delete; user-facing merged object retained. |
| CST-T08 | Cursor polling | Recording UI transfers only new chunk results. |

## Commands

### Mobile/root

Targeted first:

```bash
npm test -- features/wod/api.test.ts
npm test -- features/wod/__tests__/mergeChunksLocal.test.ts
npm run typecheck
npm run lint
```

Then full Jest where practical:

```bash
npm test -- --runInBand
```

### Backend

From `api/`:

```bash
make migrate-test-up
make migrate-test-redo
make test TEST_DIR=./internal/controllers
make test TEST_DIR=./internal/worker
make test TEST_DIR=./cmd/worker
make test
```

Use `make migrate-test-up` for a fresh test database. `make migrate-test-redo` only replays the latest migration and is appropriate while iterating on that new pair.

For FFmpeg work, record `ffmpeg -version` and run the relevant worker package on a machine with FFmpeg. A skipped FFmpeg test is not evidence that media behavior passes.

### Evaluation/load

The exact runner command will be defined by Phase 2. Every recorded run must include fixture manifest version, variant, repeat count, maximum cost, Git SHA, and output location. Never run an unbounded live evaluation.

## Deployment order for additive contracts

Use this order for Phase 1 schema/API changes:

1. Apply additive DB migration and verify backfill/deduplication counts.
2. Deploy API/worker code that reads new fields but still accepts legacy requests/rows.
3. Verify queue wiring and worker health before releasing the app.
4. Release app code that sends `chunk_index` and `expected_chunk_count` and waits for acknowledgements.
5. Observe client adoption and `legacy_chunk_manifest`/`legacy_media_timeline` rates.
6. Enforce required manifest fields only after the supported legacy window.
7. Remove fallbacks in a later cleanup PR, not in the migration rollout.

Do not immediately run down migrations during application rollback. Additive columns/indexes can remain while old code ignores them. A down migration that discards populated new timeline data is a separate, deliberate operation.

## Feature switches and rollback

Centralize and validate switches; do not scatter environment reads through handlers.

Recommended switches:

| Switch | Control behavior |
|---|---|
| `VIDEO_REQUIRE_EXPECTED_CHUNKS` | false during compatibility, then true. |
| `VIDEO_USE_MEDIA_TIMELINE` | enabled after timeline verification; legacy fallback remains observable. |
| `VIDEO_STRUCTURED_OUTPUT` | control regex path until candidate passes, then structured. |
| `VIDEO_FINAL_SYNTHESIS` | off/control concatenation until evaluation passes. |
| `VIDEO_DIRECT_UPLOAD_INDEX_MODE` | `split_upload_control`, candidate `full_index_once`/`range_scan_reuse`. |
| `VIDEO_ENABLE_SKIP_GATE` | false until calibrated; immediate kill switch. |
| `VIDEO_ENABLE_HARDSUB_DEFAULT` | current behavior during compatibility; recommended false after product approval. |
| Stage model/media/thinking variables | exact control values remain available for rollback. |
| Queue concurrency/limit variables | previous single-pool settings documented and restorable. |

State/data fixes such as unique keys, correct task routing, and semantic validation should not be treated as long-term A/B variants. Flags exist for safe rollout, not permanent dual architecture.

## Canary progression

For any model/cost/accuracy variant:

1. Offline evaluation only.
2. Shadow with no user-visible or downstream side effects.
3. Internal/test profiles.
4. Small deterministic production sample with daily cost ceiling.
5. Increase gradually only while all blocking metrics remain inside gates.
6. Full rollout; retain kill switch for at least one normal traffic cycle.
7. Remove control path only after rollback window and historical compatibility are complete.

Stop/cancel the rollout immediately for:

- unsupported diagnosis/pain/readiness claim;
- target-person attribution regression;
- missing/incorrectly ordered chunks;
- timestamp/media mismatch;
- increased no-exercise false positives outside the gate;
- unexpected cost amplification from escalation/retries;
- realtime p95 breach, rate-limit spike, OOM, or queue starvation;
- rise in PARTIAL/FAILED/parse-validation rates outside the gate.

## Per-PR definition of done

Every implementation handoff must include:

- files changed;
- migration numbers and deploy order, when applicable;
- tests added/updated and scenario IDs covered;
- exact commands run and whether FFmpeg/PostgreSQL/Redis were available;
- before/after metrics for empirical changes;
- feature/configuration defaults;
- risk and rollback instructions;
- remaining follow-up work;
- update to the appropriate `docs/agent-memory/` file when a new integration, architectural pattern, or non-obvious gotcha was introduced.

## Program exit audit

- [ ] CAP, MED, TSK, ACC-T, and CST-T critical scenarios pass.
- [ ] Additive migration and legacy compatibility were exercised.
- [ ] Manual iOS and Android evidence exists.
- [ ] FFmpeg tests actually ran.
- [ ] Duplicate/retry/restart tests prove idempotency.
- [ ] Evaluation run is pinned and repeatable.
- [ ] Cost and latency changes have stage-level evidence.
- [ ] Safety/target metrics pass blocking gates.
- [ ] Rollback was tested, not only described.
- [ ] Documentation and agent memory match the implemented final state.
