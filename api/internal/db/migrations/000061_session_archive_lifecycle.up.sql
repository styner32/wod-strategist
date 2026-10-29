ALTER TABLE profiles ADD COLUMN workout_history_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE analysis_results ADD COLUMN automatic_processing_cancelled_at TIMESTAMPTZ DEFAULT NULL;

ALTER TABLE analysis_results DROP CONSTRAINT analysis_results_sensor_state_valid;
ALTER TABLE analysis_results ADD CONSTRAINT analysis_results_sensor_state_valid CHECK (sensor_state IN ('NONE','UPLOADING','PENDING','RUNNING','COMPLETED','FAILED','EXPIRED','CANCELLED'));
ALTER TABLE chunk_reanalysis_runs DROP CONSTRAINT chunk_reanalysis_runs_status_check;
ALTER TABLE chunk_reanalysis_runs ADD CONSTRAINT chunk_reanalysis_runs_status_check CHECK (status IN ('QUEUED','RUNNING','COMPLETED','FAILED','VIDEO_UNAVAILABLE','INTERVAL_UNAVAILABLE','CANCELLED'));
ALTER TABLE session_reanalysis_runs DROP CONSTRAINT session_reanalysis_runs_status_check;
ALTER TABLE session_reanalysis_runs ADD CONSTRAINT session_reanalysis_runs_status_check CHECK (status IN ('QUEUED','RUNNING','COMPLETED','FAILED','VIDEO_UNAVAILABLE','CONTEXT_UNAVAILABLE','CANCELLED'));
ALTER TABLE recognition_review_runs DROP CONSTRAINT recognition_review_runs_status_check;
ALTER TABLE recognition_review_runs ADD CONSTRAINT recognition_review_runs_status_check CHECK (status IN ('QUEUED','RUNNING','COMPLETED','PARTIAL','FAILED','CANCELLED'));

-- Previously archived rows must not resume after their first restore.
UPDATE analysis_results SET automatic_processing_cancelled_at = archived_at WHERE archived_at IS NOT NULL;

-- Run-ID workflows intentionally permit NEW manual runs after restore. Retire
-- legacy queued runs as well as original work before making that possible.
UPDATE analysis_results
SET status = CASE WHEN UPPER(status) IN ('QUEUED','PENDING','UPLOADED','PROCESSING','MERGING','RUNNING') THEN 'CANCELLED' ELSE status END,
    sensor_version = sensor_version + CASE WHEN sensor_state IN ('UPLOADING','PENDING','RUNNING') THEN 1 ELSE 0 END,
    sensor_next_attempt_at = CASE WHEN sensor_state IN ('UPLOADING','PENDING','RUNNING') THEN NULL ELSE sensor_next_attempt_at END,
    sensor_processing = CASE WHEN sensor_state IN ('UPLOADING','PENDING','RUNNING') THEN (sensor_processing - 'lease_token' - 'lease_started_at' - 'retry_not_before') || '{"last_error_code":"SESSION_ARCHIVED"}'::jsonb ELSE sensor_processing END,
    sensor_state = CASE WHEN sensor_state IN ('UPLOADING','PENDING','RUNNING') THEN 'CANCELLED' ELSE sensor_state END,
    analysis_summary = CASE WHEN analysis_summary->>'status' IN ('pending','preparing','running','stale') THEN analysis_summary || '{"status":"interrupted","error":"SESSION_ARCHIVED"}'::jsonb ELSE analysis_summary END,
    agentic_highlight_analysis = CASE WHEN agentic_highlight_analysis->>'status' IN ('pending','preparing','running','stale') THEN agentic_highlight_analysis || jsonb_build_object(
        'status','interrupted', 'error','SESSION_ARCHIVED',
        'cleanup_pending',COALESCE((agentic_highlight_analysis->>'owned_upload')::boolean,FALSE),
        'items', COALESCE((SELECT jsonb_agg(CASE WHEN item->>'status' IN ('pending','preparing','running','stale') THEN item || '{"status":"interrupted","error":"SESSION_ARCHIVED"}'::jsonb ELSE item END)
            FROM jsonb_array_elements(CASE WHEN jsonb_typeof(agentic_highlight_analysis->'items') = 'array' THEN agentic_highlight_analysis->'items' ELSE '[]'::jsonb END) item),'[]'::jsonb)) ELSE agentic_highlight_analysis END
WHERE archived_at IS NOT NULL;

UPDATE chunk_analysis_results c SET status = 'CANCELLED'
FROM analysis_results a WHERE a.session_id=c.session_id AND a.profile_id=c.profile_id AND a.archived_at IS NOT NULL AND UPPER(c.status) IN ('QUEUED','PENDING','UPLOADED','PROCESSING','MERGING','RUNNING');
UPDATE highlight_results h SET status = 'CANCELLED'
FROM analysis_results a WHERE a.session_id=h.session_id AND a.profile_id=h.profile_id AND a.archived_at IS NOT NULL AND UPPER(h.status) IN ('QUEUED','PENDING','UPLOADED','PROCESSING','MERGING','RUNNING');
UPDATE chunk_reanalysis_runs r SET status = 'CANCELLED', safe_error = 'SESSION_ARCHIVED', completed_at=NOW()
FROM analysis_results a WHERE a.session_id=r.session_id AND a.profile_id=r.profile_id AND a.archived_at IS NOT NULL AND r.status IN ('QUEUED','RUNNING');
UPDATE session_reanalysis_runs r SET status = 'CANCELLED', safe_error = 'SESSION_ARCHIVED', completed_at=NOW()
FROM analysis_results a WHERE a.session_id=r.session_id AND a.profile_id=r.profile_id AND a.archived_at IS NOT NULL AND r.status IN ('QUEUED','RUNNING');
UPDATE recognition_review_runs r SET status = 'CANCELLED', safe_error = 'SESSION_ARCHIVED', completed_at=NOW()
FROM analysis_results a WHERE a.session_id=r.session_id AND a.profile_id=r.profile_id AND a.archived_at IS NOT NULL AND r.status IN ('QUEUED','RUNNING');
UPDATE sessions s SET activity_summary = jsonb_set(s.activity_summary,'{review_state}','"unavailable"'::jsonb)
FROM analysis_results a WHERE a.session_id=s.session_id AND a.profile_id=s.profile_id AND a.archived_at IS NOT NULL AND s.activity_summary->>'review_state' IN ('provisional','queued','running');

-- Older personal calibrations have no source-session lineage. Invalidate only
-- those profiles with an archived source; frozen per-session summaries survive.
-- A subsequent archive/restore or valid sensor completion recalculates from the
-- remaining raw percentiles, while old uncertain hybrid values are withheld.
UPDATE profiles p SET workout_history_revision=workout_history_revision+1,
    sensor_enmo_rest_g=NULL, sensor_enmo_ref_g=NULL, sensor_calibration_version=NULL,
    sensor_calibrated_at=NULL, sensor_calibration_sessions=0
WHERE EXISTS (SELECT 1 FROM analysis_results a WHERE a.profile_id=p.id AND a.archived_at IS NOT NULL);
