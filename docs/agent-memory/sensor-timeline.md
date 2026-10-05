# Optional sensor timeline

Implementation baseline: 2026-09-10. Automated regression coverage does not establish deployment or real H10 acceptance.

## Purpose and compatibility

- H10 remains optional. ACC is a post-workout reference graph, not an input to workout classification, AI intensity, rest detection, muscle load, or readiness.
- Preserve live BLE quality filtering, current BPM display, recording-chunk peak BPM, and both `buildHeartRateContext` prompt paths.
- Deterministic load/readiness calculations use `ComputeSessionMuscleLoads(score)` without `hr_bonus`. Valid completed HR is displayed with `applied: false`, `application_reason: reference_only`, and absent cardio before/after/delta fields. Historical summary `hr_bonus` may remain stored.
- Generate timelines only for newly processed calculation version 2 files. Version 1 and previous successful records require no backfill.

## Storage and processing

- Store the optional JSON in `analysis_results.sensor_timeline` (migration `000050`), alongside the existing summary and processing state. General analysis responses omit this column; the dedicated `GET /sessions/:session_id/sensor-timeline?profile_id=...` endpoint returns it.
- Successful timeline identity is `source.sensor_version`, `request_id`, `source_generation`, and `hr_calculation_version`. Worker commits summary and timeline together under the existing request/generation/version/lease CAS.
- `ParseAndProcess` computes only the summary. `ParseAndProcessWithTimeline` additionally returns `Timeline` and independent `TimelineError`. A timeline-only error is stored as `{schema_version:1,status:"failed",error,source}` while the complete sensor summary is saved with `sensor_state: COMPLETED`.
- ACC/stream events are replayed from a private bounded temporary file after the authoritative footer cutoff is known. This reads the original object once, preserves its size/hash checks, and avoids retaining all high-frequency samples in memory. Spool limit is twice the 20 MiB raw-file limit; parser defers `Close`, and `Build` also closes/removes the file. Summary-only and version 1 calls do not create a spool.
- `PolarSensorRecorder.stop()` disables input before capturing `end.t` and awaiting the BLE stop command; `start()` must reject attempts while that stop promise is pending. Older clients could write HR during that await with `hr.t > end.t`. Calculation version 2 drops those trailing HR observations with a quality warning; checksum, identity, and other corruption checks still apply. The sensor footer remains authoritative: never replace it with `MAX(chunk_analysis_results.end_secs)`, because sensor processing can finish before the last video chunk upload or analysis. This does not automatically reprocess existing `FAILED` records.
- Reject nonfinite, negative, or JavaScript-unsafe duration before integer conversion; validate evaluated sample times as well. `MaxTimelineBuckets = 100000` bounds observed/output buckets independently of elapsed time. Long empty spans are represented directly as gaps; never allocate or iterate once per second solely from untrusted `end.t`.

## Metric and clocks

- `acc_magnitude_std_g` is the population standard deviation of `sqrt(x*x+y*y+z*z)` in each one-second capture bucket, in g. Use Welford accumulation and round to four decimal places.
- Clip samples to `0 <= t < end.t` before counts or statistics. Evaluate the last partial bucket using its actual duration.
- Track monotonic sample times per stream across packet and bucket boundaries. A reconnect invalidates only a bucket that actually contains a stream transition; it must not invalidate the last complete bucket before an empty interval.
- ACC validity requires known sampling rate, at least two samples and 80% expected coverage, no gap/edge exceeding three sample periods, and no saturation or stream/time error. HR uses the existing version 2 accepted intervals with 80% per-bucket coverage. Missing values remain null.
- `duration_ms` is elapsed capture time including pauses. The graph domain is `0..duration_ms`, even when leading or all sensor points are absent.
- Merged playback uses only verified chunk `start_secs`/`end_secs` paired with `media_start_secs`/`media_end_secs`. Convert capture milliseconds to media seconds proportionally inside a valid segment; never seek directly using capture seconds. Restrict this mapping to the original merged video.

## Web rendering and interaction

- Split each channel at null values, explicit matching `gaps`, and discontinuities between sparse buckets **before** display decimation. Keep null separators even when both sides fit in one pixel bin.
- Pointer/keyboard selection retains the actual capture timestamp. Tooltips read only the containing half-open bucket (allow the final session endpoint); a missing interval cannot borrow the nearest valid reading. A missing sensor interval may still seek if its video mapping is valid.
- Video/chunk readiness changes refresh mapping after sensor completion. Poll every 5 seconds for at most 5 minutes per active session/profile while sensor processing is pending or a completed timeline lacks mapping/video. Refetch missing video URLs (including an initial 404). `sensorTimelineRecovery.ts` refetches active chunk/session metadata once per verified mapping/source, including the final ready response even if a video URL already exists. A failed refresh keeps readiness polling active within the same five-minute budget; identical successful responses do not create a metadata fetch loop. Stop when mapping, metadata and video are ready, the sensor API fails, or the budget expires; sessions without sensor data do not poll.
- Reset local graph zoom/selection when session, profile, timeline source, or selected video kind changes.

## Regression checks

- Sensor tests cover unsafe duration, sparse long gaps, footer clipping, reconnects, timestamp reversals, and temporary-file cleanup.
- Controller/worker tests use the real test PostgreSQL and transport-backed GCS client. `CreateAnalysisResult` must copy `SensorTimeline`; otherwise endpoint fixtures silently lose their timeline.
- Run DB-dependent packages serially with `go test -p 1` and isolated `TEST_DATABASE_URL` / `TEST_REDIS_URL`. Web tests cover discontinuities, dense nulls, exact-time selection, capture domain, and mapping refresh; mobile HR tests guard the existing feedback paths.

Verified locally on 2026-09-10:

- `go test ./internal/sensor ./internal/fatigue` (from `api/`).
- `go test -race ./internal/sensor ./internal/fatigue` (from `api/`).
- `go test -p 1 ./internal/controllers ./internal/worker -ginkgo.focus='Heart rate|calculation version|Sensor|sensor' -count=1` (from `api/`), with all migrations applied to a fresh temporary PostgreSQL database and a temporary Redis instance; both services stopped after testing.
- `npm test -- --runInBand features/health features/wod`: 15 suites / 148 tests passed.
- `go test ./internal/worker -run '^TestWorker$' -ginkgo.focus='^buildChunkAnalysisPrompt' -count=1` (from `api/`): existing AI prompt HR input passed; this focus does not use DB.
- `npm test -- --runInBand web/test/sensorTimeline.test.ts web/test/sensorTimelineInteraction.test.ts web/test/highlights.test.ts`: 3 suites / 28 tests passed, including 18 new timeline regressions.
- `npm run typecheck`, `npm --prefix web run build`, and `git diff --check` passed. The build retains its bundle-size warning. Focused ESLint could not initialize because the installed `typescript-eslint` threw `Cannot read properties of undefined (reading 'Cjs')`; no dependency/config changes were made.

No deployment, historical reprocessing, or iPhone/H10 exercise test was performed for this correction.

## Sensor stop cutoff correction — 2026-09-15

- Changes: `features/health/polar/polarSensorRecorder.ts` freezes input before the BLE stop await and prevents a new session from overwriting a pending stop; `api/internal/sensor/parser.go` trims post-footer HR with warnings. Removed the proposed video-duration overrides from the app, worker, API response, and web graph.
- Regression tests: `polarSensorRecorder.test.ts` exercises delayed BLE replies, timeouts, concurrent stop/start, and subsequent recording; `timeline_boundaries_test.go` compares trimmed output with a clean file and retains checksum rejection; `sensor_quality_test.go` verifies `COMPLETED` persistence with a 10-second sensor footer, a 10.25-second trailing HR sample, and only 5 seconds of video chunk analysis available.
- Passed `go test ./internal/sensor -count=1` from `api/`.
- Passed `go test -p 1 ./internal/worker -count=1 -ginkgo.focus='Sensor quality worker|SensorTelemetry'` from `api/`, using `TEST_DATABASE_URL` for a fresh temporary PostgreSQL database with all migrations applied. The temporary server was stopped and its data removed after testing.
- Passed `npm test -- --runInBand features/health/polar/__tests__/polarSensorRecorder.test.ts web/test/sensorTimeline.test.ts web/test/sensorTimelineInteraction.test.ts --silent`: 3 suites / 37 tests.
- Passed `npm run typecheck`, `npm --prefix web exec -- tsc -b web/tsconfig.json --pretty false`, and `git diff --check`.
- This correction was validated locally only. No deployment, real-device test, or reprocessing of failed analysis ID 365 was performed.
