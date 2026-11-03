-- Preserve a terminal state understood by the previous version before restoring
-- its constraints. No completed result is rewritten.
UPDATE analysis_results SET sensor_state = 'EXPIRED' WHERE sensor_state = 'CANCELLED';
UPDATE analysis_results SET status = 'FAILED' WHERE status = 'CANCELLED';
UPDATE chunk_analysis_results SET status = 'FAILED' WHERE status = 'CANCELLED';
UPDATE highlight_results SET status = 'FAILED' WHERE status = 'CANCELLED';
UPDATE chunk_reanalysis_runs SET status = 'FAILED' WHERE status = 'CANCELLED';
UPDATE session_reanalysis_runs SET status = 'FAILED' WHERE status = 'CANCELLED';
UPDATE recognition_review_runs SET status = 'FAILED' WHERE status = 'CANCELLED';
ALTER TABLE analysis_results DROP CONSTRAINT analysis_results_sensor_state_valid;
ALTER TABLE analysis_results ADD CONSTRAINT analysis_results_sensor_state_valid CHECK (sensor_state IN ('NONE','UPLOADING','PENDING','RUNNING','COMPLETED','FAILED','EXPIRED'));
ALTER TABLE chunk_reanalysis_runs DROP CONSTRAINT chunk_reanalysis_runs_status_check;
ALTER TABLE chunk_reanalysis_runs ADD CONSTRAINT chunk_reanalysis_runs_status_check CHECK (status IN ('QUEUED','RUNNING','COMPLETED','FAILED','VIDEO_UNAVAILABLE','INTERVAL_UNAVAILABLE'));
ALTER TABLE session_reanalysis_runs DROP CONSTRAINT session_reanalysis_runs_status_check;
ALTER TABLE session_reanalysis_runs ADD CONSTRAINT session_reanalysis_runs_status_check CHECK (status IN ('QUEUED','RUNNING','COMPLETED','FAILED','VIDEO_UNAVAILABLE','CONTEXT_UNAVAILABLE'));
ALTER TABLE recognition_review_runs DROP CONSTRAINT recognition_review_runs_status_check;
ALTER TABLE recognition_review_runs ADD CONSTRAINT recognition_review_runs_status_check CHECK (status IN ('QUEUED','RUNNING','COMPLETED','PARTIAL','FAILED'));
ALTER TABLE analysis_results DROP COLUMN IF EXISTS automatic_processing_cancelled_at;
ALTER TABLE profiles DROP COLUMN IF EXISTS workout_history_revision;
