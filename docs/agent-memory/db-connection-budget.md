# Database Connection Budget & History Batching

## Cloud SQL connection budget (`db-f1-micro`)
- `wod-strategist-db-dev` runs on `db-f1-micro` (`max_connections = 25`, `superuser_reserved_connections = 3` → `~22` non-superuser connections).
- Total application connection budget is capped at **`18`** (`DefaultMaxOpenConns = 18` in `api/internal/db/db.go`):
  - Cloud Run API (`google_cloud_run_v2_service.api`): `scaling.max_instance_count = 2` (`--max-instances=2` in `api/Makefile`) × `DefaultServerMaxOpenConns = 6` (`DB_MAX_OPEN_CONNS`) = `12` max open connections.
  - Cloud Run Worker Pool (`google_cloud_run_v2_worker_pool.worker`): `scaling.manual_instance_count = 1` × `DefaultWorkerMaxOpenConns = 6` (`DB_MAX_OPEN_CONNS`) = `6` max open connections.
  - Idle pool sizing: `DefaultMaxIdleConns = 2` (`DB_MAX_IDLE_CONNS`), `DefaultConnMaxIdleTime = 30s`, `DefaultConnMaxLifetime = 30m`.
- Never increase `DefaultServerMaxOpenConns`, `DefaultWorkerMaxOpenConns`, or Cloud Run `max_instance_count` unless the Cloud SQL tier (`max_connections`) is upgraded first and `max_instances × DB_MAX_OPEN_CONNS + worker_instances × DB_MAX_OPEN_CONNS <= usable_slots - 4`.

## Lock & external I/O rules
- Never perform GCS RPCs (`ListObjects`, `ObjectAttrs`, `GenerateSignedURL`), Gemini API calls, or FFmpeg work inside `db.WithSessionAutomaticWork` or any transaction holding `SELECT id FROM profiles WHERE id = ? FOR UPDATE`.
- `profiles FOR UPDATE` serializes per-profile mutations across `/chunk-complete`, `/merge-chunks`, archive/unarchive, and worker `DispatchRecordingMedia`. Keep those transactions to pure SQL reads/writes (`< 20ms`).

## `GET /api/v1/history` payload & N+1 constraints
- `historyOmittedAnalysisColumns` in `api/internal/controllers/history_handlers.go` omits `sensor_timeline`, `agentic_highlight_analysis`, and `injury_output_alt` to avoid large TOAST reads on list loads, and clears `WorkoutProgress` to `nil` after deriving `InputStatus`.
- **Do NOT omit `sensor_processing`** in `GetHistory`: `normalizeHighlightResultsForResponseWithSchema` (`evaluateSensorSummaryFreshness`) reads `AnalysisResult.SensorProcessing` (`request_id`, `target_generation`, `calculation_inputs`) to validate `SensorSummary`.
- `GetHistory` embeds completed `highlights` (`status = 'COMPLETED'`, ordered `created_at DESC`) and compact `activity_summary` (`reviews: []`; `chunk_analysis_results` batch query MUST select `exercise_type` so `activity.Build` labels `Unassessed` gaps accurately).
- Mobile `HistoryList` passes `item.highlights` to `useHighlightResults` and `item.activity_summary` to `ActivitySummaryCard` to avoid per-card mount requests (`20 × 2 = 40` fan-out), falling back to per-session endpoints only when those fields are `undefined` or non-terminal (`provisional` / `queued` / `running`).
