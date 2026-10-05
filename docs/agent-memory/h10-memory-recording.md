# Polar H10 Internal HR Recording (experimental, phase 1)

Opt-in experiment to fill live-BLE gaps (e.g. leaving range during an outdoor carry) with the H10's
own exercise recording. Phase 1 = record → retrieve → save locally → remove from sensor → upload →
web shows the raw record. **No timeline/graph merge, no backend parsing yet.**

## Scope rules
- Toggle `VideoPreferences.h10MemoryRecording` (setup.tsx, default `false`, **iOS-only** row). Route
  param `h10MemoryRecording="true"` only from `navigateToVisionTest` (never preview).
- `visionTestPage.tsx`: `h10MemoryEnabled = iOS && param && !previewOnly`. The live pipeline
  (`useBleHeartRate` → `PolarSensorRecorder` NDJSON, PMD ACC, sensor upload, parser, timeline) is
  unchanged; the only addition to `PolarSensorRecorder` is the read-only `getDevice()` getter.
- Everything is fire-and-forget: `H10MemoryRecorderInstance.begin()` after `PolarSensorRecorder.start`,
  `finish(sessionId)` right after `PolarSensorRecorder.stop()` (also on start rollback and unmount).
  Never `await` it in the stop flow.
- Watchdog (toggle ON, recording, not paused, 1 Hz over `getLiveStatus()`): HR stall = `hrStatus ===
  "Live"` and `hrSamples` unchanged > 15 s; ACC stall = ACC was flowing, unchanged > 5 s while HR still
  moves. Banner `testID="h10-memory-banner"` (yellow stall, red `start_failed`/`failed` with stage/code),
  kept ≥ 10 s. Status line `testID="h10-memory-status"`.

## Files
- `features/health/polar/psftpProtocol.ts` — RFC60/RFC76 framing + protobuf subset (Polar SDK License header).
- `features/health/polar/psftpClient.ts` — ble-plx transport, one exchange at a time.
- `features/health/polar/h10MemoryRecorder.ts` — run lifecycle, local store, upload.
- Web: `web/src/history/h10MemoryRecord.ts` (pure summary), `components/H10MemoryPanel.tsx`
  (collapsible, loads on open, 404 = "no record"). Backend: `onDeviceAssetName` regex in
  `api/internal/controllers/on_device_ai_handlers.go` allows `h10_memory_*.json` for **read only**
  (not in the list families).

## Protocol constants (from Polar BLE SDK)
- Service `0000feee-0000-1000-8000-00805f9b34fb`, MTU characteristic
  `fb005c51-02e7-f387-1cad-8acd2d8df0c8` (write frames, responses arrive as notifications).
- Queries: start `14`, stop `15`, status `16`. File ops (`PbPFtpOperation`): GET `0`, REMOVE `3`.
- RFC60: query = `[id & 0xff, (id >> 8 & 0x7f) | 0x80, ...params]`; request = `[len & 0xff, len >> 8 & 0x7f, ...header]`.
- RFC76 header byte = `next | status << 1 | seq << 4`; status MORE `3`, LAST `1`, `0` = error/response
  (2-byte LE code, `0` = success). Packet size = `max(20, mtu - 3)`. Writes without response, falls
  back once to with-response (`transport.write_mode` in the record).
- Start params: `sample_type=1 (HR)`, `recording_interval=PbDuration{seconds=1}`,
  `sample_data_identifier=<workout sessionId>`.
- File: `/<identifier>/SAMPLES.BPB` = `PbExerciseSamples {1 recording_interval, 2 packed
  heart_rate_samples, 3 heart_rate_offline{start_index, stop_index}}`. Other field numbers are
  kept in `other_fields`.
- H10: one recording slot, never auto-erased, HR/RR only (no ACC), **no timestamps**. Sample `i`
  is assumed at `start.ack_epoch_ms + i * recording_interval_ms` (unverified).

## Lifecycle and deletion rules
- `begin`: save `status:"recording"` record → wait for connected device → leftover cleanup
  (status → stop → list → for each `SAMPLES.BPB`: no local record or local `recording` → fetch + save
  (`recovered_later`, orphan = `orphan_<id>_<ms>` with `profile_id:null`, never uploaded); then REMOVE)
  → start query. A begin while another run is active finishes that run first (serialized queue).
- `finish`: 10-min deadline from stop request (`H10_MEMORY_DEADLINE_MS`). Retries transient BLE
  errors every 3 s (`fetch.attempts`, `fetch_attempt` errors, max 20); `PftpError`/stage errors are
  final. Self-reconnects (after 5 s) when the recording screen already closed the connection.
- **Sensor recording is always removed** after the local save, or after failure/deadline
  (`sensor_remove` error if the remove itself fails, 30 s timeout).
- Error codes: `PFTP_<n>_<NAME>`, `TIMEOUT`, `BLE_<code>`, `DEADLINE_10MIN`, `DEVICE_UNREACHABLE`,
  `NO_RECORDING_FOUND`, `LOCAL_SAVE_FAILED`, `NO_DEVICE`, `NOT_STARTED`, `UNKNOWN`. Stages: `start`,
  `leftover_check`, `leftover_fetch`, `leftover_remove`, `fetch_attempt`, `fetch`, `sensor_remove`.

## Storage and upload
- Local: `Documents/h10mem/<safe sessionId>.json`, renamed `.uploaded.json` after upload.
- GCS: `videos/{profileId}/{sessionId}/h10_memory_hr.json` via `getUploadUrl` +
  `uploadSessionAssetToGcs` (no backend change for upload). Pending records retry on next begin/finish.
- Web read: `GET /sessions/:id/on-device-ai/asset?profile_id=&filename=h10_memory_hr.json` (2 MB cap).

## Unverified until real-device tests
iOS pairing prompt for FEEE; ACC/HR live streams continuing during recording; what the H10 writes
during contact loss (zeros vs `heart_rate_offline`); identifier length/truncation (fallback: single
file on the sensor); write-without-response support; alignment accuracy; fetch time; self-reconnect
racing a new recording screen's BLE hook. Android: compiles only, toggle hidden.
