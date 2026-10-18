ALTER TABLE session_reanalysis_runs DROP COLUMN IF EXISTS wod_start_request;
ALTER TABLE sessions DROP COLUMN IF EXISTS effort;
ALTER TABLE sessions DROP COLUMN IF EXISTS wod_evaluation_version;
ALTER TABLE profiles DROP COLUMN IF EXISTS wod_tracking_started_at;
