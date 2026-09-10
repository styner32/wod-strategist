# Heart rate quality and summaries

Implementation baseline: 2026-09-08. Code and automated checks are separate from deployment and H10 device acceptance.

## Version and storage contract

- Keep the single `analysis_results` table and `videos/{profileId}/{sessionId}/sensor_telemetry_v{sensorVersion}_{requestId}.ndjson` path. No new database column or migration.
- `POST /sessions/:session_id/sensor-upload` accepts optional `calculation_version`: omitted/0 means 1; supported values are 1 and 2. New mobile queue entries use `calculationVersion: 2`; preexisting entries missing that field send 1. Preserve request ID and calculation version on retries, including after app restart or backup restoration. A version change under the same request ID returns `409 REQUEST_CONTENT_CONFLICT`.
- Freeze version in `sensor_processing.calculation_inputs.calculation_version` when preparing the request. Worker passes it into `sensor.ParseAndProcess`. Historical successful version 1 summaries are read without recalculation; do not bulk reprocess today's or earlier sessions.
- `AnalysisResult.heart_rate` is a response-only DTO (`gorm:"-"`). Populate it before video/fatigue early returns, including pending or failed video. Do not use `session_fatigue` or HR bonus as a prerequisite for displaying measured sensor data.
- Only expose current metrics after matching sensor state COMPLETED, sensor version, request ID, source file generation, and calculation version. Legacy processing with no calculation version matches only summary version 1. Pending/failed/newer requests must not display the last successful summary as their current metrics.

## Quality contract

- `0x2A37`: flags `0x04` means contact status supported; when supported, `0x02` means contact detected. Raw NDJSON HR events keep optional `contact`, raw BPM, and RR. Preserve `false`; omit unsupported contact. No full Polar SDK or ECG acquisition is added.
- `stream_start.clock_anchor.device_timestamp_ns` accepts either a decimal string (current mobile, preserves JavaScript-unsafe integer precision) or a legacy JSON integer. Decode without a float64 intermediate. Reject fractional/overflow values. A decode failure drops the entire stream-start event, including `sampling.acc_hz`, so clock-anchor compatibility also protects ACC sampling-rate metadata.
- Version 1 retains existing calculation behavior. Version 2 uses matching TypeScript/Go state machines and `shared/__fixtures__/heart-rate-quality.json`.
- Exclude contact false and BPM outside inclusive 30–240. A stable value below 55 is accepted with a low-heart-rate warning; low BPM alone is not rejection.
- Drop baseline: median of accepted readings within 10 seconds, at least 3 readings, last accepted reading no older than 5 seconds. A fall of at least 30 BPM and at least 30% freezes the baseline. This check also applies during contact recovery if a recent baseline exists.
- After a drop, require readings at least 70% of the frozen baseline and 5 seconds of uninterrupted otherwise-valid readings. Contact loss, invalid packets, or missing data restart recovery. A 5-second timeout clears current BPM and recent history but preserves a frozen drop baseline. Physical reconnect and a new session clear the baseline. Pause/resume starts a fresh baseline.
- These thresholds are app heuristics awaiting H10 exercise validation, not Polar physiological confidence scores.
- Keep raw measurements and accepted measurements separate. `onReading` publishes accepted BPM synchronously for chunk peak tracking. Invalid readings never supply a current/fallback BPM. Start each chunk with no peak; accept only readings received while that chunk is recording. Never seed or backfill from a previous chunk. Omit chunk HR when none exists. Sensor failure must never stop video recording.

## Aggregation and DTO semantics

- ACC stays streaming. Version 2 retains compact HR observations until footer pause intervals are available. Active duration excludes clipped, merged pauses.
- Integrate each observation until the next HR/lifecycle observation only for intervals shorter than 5 seconds, clipping pauses. Entire gaps of 5 seconds or longer and the unobserved final tail remain unknown. Do not fill excluded or missing intervals with a held BPM.
- Accepted instantaneous samples contribute to minimum/peak even if they are the last sample; averages and zones require accepted duration. With no accepted duration, omit average, rather than zero. With no accepted samples, omit all BPM statistics.
- `valid_seconds + excluded_seconds + unknown_seconds = active_duration`. Exclusion reasons partition excluded time (contact_loss, invalid, sudden_drop, recovering); they do not overlap. `low_bpm_seconds` is a subset of valid time. `contact_coverage` is the proportion of active time with an observed supported contact flag, including false.
- Coverage threshold remains 50%; bonus remains `clamp((weighted_mean_bpm - 140) * 0.5, 0, 20)` only for complete files with adequate valid coverage.
- DTO zones carry explicit `zone` 1–5, `seconds`, and `ratio`; ratio denominator is valid duration. Omit zones without a maximum-HR basis. Include `max_bpm` and `max_bpm_source` when known.
- `applied` means the sensor result was considered by the load calculation, even if bonus is zero. `application_reason` distinguishes no_sensor, pending, failed, stale_summary, quality_insufficient, video_insufficient, no_bonus, adjusted, score_capped. `cardio_before/after/delta` report the actual capped load change, not raw bonus or whole-body score change. No valid video movements means no muscle-load scores.
- `device_name` comes from recorded device metadata; absent names display “심박 센서” / “Heart rate sensor”. Never assume every sensor is H10. Version 1 is labeled “이전 품질 기준” / “Previous quality criteria”; contact/exclusion fields unavailable in old summaries remain absent.

## Verification and release acceptance

Executed locally on 2026-09-08:

- Mobile: 14 Jest suites / 141 tests passed, plus typecheck. Commands: `npm test -- --runInBand features/health features/wod`; `npm run typecheck`.
- Backend: `go test -p 1 ./internal/sensor ./internal/controllers ./internal/worker ./internal/fatigue -ginkgo.focus='Heart rate|calculation version|Sensor|sensor'`. Use isolated PostgreSQL `wod_hr_test` on port 55432 (`TEST_DATABASE_URL`) and Redis on port 56379 (`TEST_REDIS_URL`), after applying existing migrations. No production/test-default database was modified.
- New coverage: shared quality fixtures, raw contact/RR preservation, truncated packets, hook timeout/recovery/reconnect, version pinning/retries, pauses/time partitions, drop/malformed exclusion, final accepted peak, 50% boundary, missing max-HR basis, stale identities, sensor-only DTO, zero/capped bonus, mobile summary states. Worker uses the real GCS client with `MockTransport`, verifies a generation-bound read and persisted version 1/2 summaries.
- Sensor route tests now provide signing credentials and an actual GCS 404. `CreateAnalysisResult` test factory preserves sensor fields; it previously silently dropped them, so recovery/worker setup never entered the intended states.
- Web: `npm --prefix web run build`; real summary component inspected with fixture data at desktop and 390px width. Temporary preview files removed. This is not proof of live production data or native layout correctness.

Still required before H10 acceptance:

1. Deploy API and worker with version 2 support before distributing the new mobile/web clients. No deployment was performed during this implementation.
2. On the real iPhone/H10, record ordinary exercise, burpees, prone/push-up movements, contact loss, recovery, pause/resume, and reconnect. Verify contact support bits actually emitted by H10, raw flags around strap lift, immediate bad-number suppression, 5-second recovery, and uninterrupted video.
3. Inspect the new NDJSON and persisted version 2 summary; reconcile active duration and valid/excluded/unknown time, compare accepted chunk peaks, and verify mobile/web statistics.
4. Read existing version 1 records (including today's earlier sessions) and confirm legacy labeling and unchanged stored summary. Record device/app versions, session IDs, observed flags and acceptance results here. Hardware exercise validation and live historical compatibility have not yet been performed.

## Real exercise evidence (2026-09-09): quality acceptance failed

- The user reported deploying the prior changes and observing H10 readings of 80–90 BPM during high exertion, then removing the strap and seeing normal Apple Watch readings. Apple Watch samples were not independently acquired. This is a failed H10 exercise acceptance case, not evidence that version 2 can detect sustained under-reading.
- Replaying the uploaded `WOD-20260909-01M21X9A35GGJA27HC79CHZ2E3` NDJSON through the current TypeScript and Go version 2 rules accepts all 559 HR events: raw minimum 60, maximum 129, time-weighted mean 92.4883 BPM. Valid time is 558.698 seconds, excluded time 0, unknown time 366.898 seconds, total 925.596 seconds, coverage 60.3609%. The 80–90 BPM segment lasts 60 seconds and passes the rules. These are local replay results, not a fetched production summary.
- No event in either of today's two files contains `contact`; raw GATT flags and an exact app build revision are unavailable. Do not infer H10 contact support or actual skin contact from the absent field. The persisted pending-queue backup identifies calculation version 2 for the WOD upload.
- The detector only consumes BPM/contact/time. A gradual decline, low initial baseline, or long low plateau can pass. Coverage measures time admitted by these rules, not probability that the BPM is accurate. Do not double the BPM, exclude all 80–90 readings, or silently change version 2 to fit this single session.
- WOD RR data also becomes irregular: 172/412 HR notifications from 150 seconds onward omit RR, and 50/376 reported RR intervals exceed 2,000 ms. RR is optional/batched and these observations alone do not establish a validated rejection rule or reconstruct true BPM. Future quality rules require comparison recordings and a new pinned calculation version.
- No historical summary was rewritten. See [the incident record](../diagnostics/2026-09-09-h10-and-partial-merge.md) for scope, measurements, native reproduction, and remaining checks.
