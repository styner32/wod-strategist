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
- A completed chunk can still contain `[NO_EXERCISE]` and empty coaching. Inspect
  provider text and activity state before diagnosing delivery failure. Recovery
  observers must treat visible equipment-free warmup, joint rotations, dynamic
  ROM and holds as possible exercise; missing apparatus is not proof of rest.
  Use Unknown when exercise is visible but its exact name is unclear, and keep
  the shared observation-only boundary. Do not fabricate feedback for empty video.
- `GET /api/v1/sessions/:session_id/activity-summary?profile_id=<owned id>`
  requires profile and session ownership. Existing chunk array responses remain
  compatible. Mobile and web use `shared/activity.ts` for the new contracts.

## Evidence and clocks

- Base chunk inference sees no recent-chunk assessment history (form, counts,
  coaching, fatigue). Since 2026-10-26 it receives the Tracking channel by
  default (`ENABLE_TRACKING_CONTINUITY`; defaults to `true` when unset, set
  `false` to disable): `trackingInputs` supplies the single unified
  `target_person` instruction (`trackingOutputPrompt`, omitting legacy
  `TargetPersonPrompt` so duplicate blocks are never requested), attaches up to
  two marked reference JPEGs (the
  latest within 60 s of the chunk start, plus the session's first) and a
  whitelisted `trackingHandoff` (matched cues, frame region, station, previous
  activity state, non-authoritative movement hint). It never waits for a late
  previous chunk. The chunk reports `target_person.boxes` (≤3, `{t, box_2d}` in
  normalized [ymin,xmin,ymax,xmax] 0..1000), `frame_region`, `station` inside
  the existing `target_cues` text column (no migration), even on `[NO_EXERCISE]`
  chunks when the target person is visible, and does not force `confidence: 0.5`
  when explicit appearance cues are absent. When
  `target_confidence ≥ 0.7` and not ambiguous, `saveTrackingReference` renders
  the last box onto that frame (ffmpeg + `image/draw`) to
  `videos/{pid}/{sid}/tracking/{chunkID}.jpg` and records `reference_image`;
  failures never fail the chunk. Pass 2 (production and reanalysis) attaches the
  reference nearest the window plus the session anchor. The web player overlays
  the same boxes (toggle, default off; see [web-media-playback.md](web-media-playback.md)).
  New structured blocks are
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

Following the user's 2026-10-14 clarification, bounded recent-window coaching
is restored in both live handlers and saved-chunk resumption after independent
observations are persisted. `ENABLE_CONTEXTUAL_COACHING` still controls generation
with its existing default. Mobile displays valid current-chunk localized coaching
with basic output as fallback; camera feedback remains independent. Accumulated
counts stay in post-recording review. This separate 60-second coaching layer cannot
rewrite observations, basic output or `workout_progress`, or supply its text to
video observers. Full performed-history real-time analysis remains a follow-up
after the user's acceptance of final assessment; see [workout-progress.md](workout-progress.md).

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
- **Frame-Grid Contact Sheet Review (2026-10-22, Option 1 — Single-Chunk Window):**
  - `HandleActivityReviewTask` probes the downloaded merged video locally via `probeVideoDuration`. When local probing succeeds (`duration > 0`), it skips `GeminiClient.UploadVideo` (0s Files API upload/polling) and analyzes each 30s sliding window (`10s` owned chunk + up to `10s` leading/trailing adjacent context) via `AnalyzeVideoIntervalWithFrameGrids` using `activityReviewFrameGridOptions()` (`FPS=4.0`, `Cols=8`, `Rows=4` = 4 JPEG contact sheets per 30s window, `FormatSecondsBadge: true` burning decimal `%.2fs` media seconds onto each tile, `ThinkingLevel: "MEDIUM"` via `gemini.WithThinkingLevel`, `gemini-3.8-flash`).
  - Why: Concurrent `VideoMetadata` slicing on large uploaded Files API videos (`79 MB+`) triggered server-side HTTP 504 `DEADLINE_EXCEEDED` at ~89s on 57.6% (19/33) of windows in the 33-chunk (`327.96s`) Grace benchmark. Switching to 4-sheet Frame-Grid contact sheets reduced input tokens per 30s window by 52.2% (`11,770` -> `5,632` prompt tokens), cut per-window cost by 36% (`$0.078` -> `$0.050`/window), lowered median call latency from `88.78s` to `34.90s`, and achieved `33/33` (100%) single-pass completion with accurate rep detection.
  - Fallback: If `probeVideoDuration` returns `<= 0` (e.g., synthetic non-MP4 test stubs or missing `ffprobe`) or `GenerateFrameGrids` fails with `len(sheets) == 0`, `HandleActivityReviewTask` lazily uploads the video once via `ensureUploaded` (`UploadVideo` + `defer DeleteFile`) and falls back to `AnalyzeSegmentWithModel` (`VideoMetadata`).
- **Rest-Confidence Skipping & Batch-3 Frame-Grid Grouping (2026-10-26):**
  - **Rest-confidence skipping (`shouldSkipActivityReviewRestChunk`):** Before downloading `merged.mp4` from GCS, `HandleActivityReviewTask` evaluates completed Pass-1 chunks that have valid non-overlapping clocks, non-exercise `ExerciseType`/`ObservedSignals`, and `MovementObservations` with `target_state == "identified"`, `activity_state == "rest"`, `events == []`, `unassessed == []`:
    - **MoveNet excluded:** `chunk_analysis_results.workout_confidence` is never consulted.
    - **Polar H10 strictly optional:** `loadSessionSensorTimeline` reads `analysis_results.sensor_timeline` (`capture_clock` `acc_magnitude_std_g` buckets overlapping `[StartSecs*1000, EndSecs*1000)`) only when `SensorState == COMPLETED` and valid; missing/failed sensor data imposes zero penalty and falls back to `rest -> rest -> rest` and `motion_score`.
    - **`WARMUP` / `COOLDOWN` (`IsRecoveryWorkoutType`):** Ignores `motion_score` and H10 thresholds and skips immediately when Pass 1 forms a contiguous rest run (`isRestRunInterior`, i.e., `rest -> rest -> rest`). Non-interior rest chunks are still reviewed so slow mobility/holds are preserved.
    - **`WOD` / `ACCESSORY`:** Never skips a rest chunk immediately following an active chunk (`noPrecedingActive` guard preserves cross-boundary spillover reps finishing in `(MediaStartSecs, MediaEndSecs]`). Vetoes skip if `motion_score >= 0.045` or optional H10 has `max >= 0.12g` or `mean >= 0.08g`. Confirms skip on `isRestRunInterior` (`rest -> rest -> rest`), optional H10 stillness (`coverage >= 0.50` and `max < 0.08g`), or `motion_score < 0.012`.
    - **Boundary ownership:** Compute all rest-skip candidates before reusing completed reviews. On `WOD` / `ACCESSORY`, also review the immediate adjacent successor of each original non-skippable candidate, even if Pass 1 called that successor rest. This preserves repetitions completing across the boundary after a motion/H10 veto. Use the original candidate decisions so protection does not cascade through the remaining rest run; apply the same plan on retries.
    - Skipped chunks are recorded immediately as `ReviewedChunk{State: "completed", Observations: activity.ConfirmedRest(dur)}`. If all chunks are already completed or rest-skipped (`len(batches) == 0`), `HandleActivityReviewTask` saves `completed` (or `partial` if unassessed gaps exist) and exits before downloading `merged.mp4`.
  - **Batch-3 grouping (`buildActivityReviewBatches`):** Remaining uncompleted chunks are grouped into contiguous (`activity.Adjacent`) batches of up to `3` chunks (`<= 35.0s` core duration) with up to 1 adjacent chunk leading/trailing context (`<= 15.0s` per side, ~50s max window / 6–7 Frame-Grid sheets at `4 FPS`, `180s` per-call timeout via `activityReviewWindowTimeout`). Each batch response is decoded once over `[WinStart, WinEnd]` and distributed per chunk via `activity.Own(doc, *row.MediaStartSecs, *row.MediaEndSecs)` (which sets each chunk's `DurationSecs = end - start`).
  - **Out-of-frame (`not_visible`) handling & tracking references (2026-10-27):**
    - `activity.Decode` preserves `TargetState: "not_visible"` (`Reason: "target_not_visible"`) and `"ambiguous"` (`Reason: "unassessable_target_or_activity"`) instead of overwriting them with `"unknown"`, so Pass-2 continuity (`classifyChunkActivity`) classifies Pass-1 `not_visible` chunks as `chunkUnclassified` rather than `chunkActive ("Unknown")`.
    - Before downloading `merged.mp4`, `HandleActivityReviewTask` skips interior chunks of contiguous Pass-1 `not_visible -> not_visible -> not_visible` runs (`isPass1ExplicitNotVisible` + `isNotVisibleRunInterior`), keeping departure and return boundary chunks adjacent to non-`not_visible` chunks reviewable so boundary-completing reps are preserved. Skipped interior chunks record `activity.ConfirmedNotVisible(*row.MediaStartSecs, *row.MediaEndSecs, sensorActive)` (`Reason: "target_not_visible_sensor_active"` when optional Polar H10 `acc_magnitude_std_g` has `max >= 0.12g` or `mean >= 0.08g`, otherwise `"target_not_visible"`), avoiding blind video calls when the athlete is out of camera view.
    - When `TrackingContinuityEnabled` is true, `HandleActivityReviewTask` attaches stored green-box `TARGET` reference JPEGs (`loadTrackingReferences` + `cachedPass2TrackingInputs`, memoizing GCS object reads across batches) to both `AnalyzeVideoIntervalWithFrameGrids` (`gridOpts.ReferenceImages`) and the `AnalyzeSegmentWithModelAndImages` fallback (`WithThinkingLevel(..., "MEDIUM")`).
    - When appearance cues are configured, `formatTargetPersonContext` and `agenticHighlightPrompt` forbid falling back to center bystanders when zero visible people match persistent cues, requiring immediate fast-exit (`not_visible` / `[NO_EXERCISE]` / `target_status: "absent"`).
  - **Catalog coverage (`Box Step-up` / `Box Step-over`):** Canonical `Box Step-up`, `Box Step-over`, `DB Box Step-up`, `DB Box Step-over`, `Weighted Box Step-up`, `Weighted Box Step-over`, `Sandbag Box Step-up`, `Sandbag Box Step-over` (plus English/Korean aliases) are registered in `internal/movement/catalog.go` and `internal/fatigue/fatigue.go` so `activity.Decode` counts them as `reps` instead of `unsupported_movement_or_unit`.
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
