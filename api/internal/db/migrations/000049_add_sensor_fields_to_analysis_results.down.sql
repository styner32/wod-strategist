DROP INDEX IF EXISTS idx_analysis_results_profile_workout_at;
DROP INDEX IF EXISTS idx_analysis_results_sensor_due;

ALTER TABLE analysis_results
    DROP CONSTRAINT IF EXISTS analysis_results_sensor_json_objects,
    DROP CONSTRAINT IF EXISTS analysis_results_sensor_state_valid,
    DROP CONSTRAINT IF EXISTS analysis_results_sensor_version_nonnegative;

ALTER TABLE analysis_results
    DROP COLUMN IF EXISTS workout_at_source,
    DROP COLUMN IF EXISTS workout_at,
    DROP COLUMN IF EXISTS sensor_next_attempt_at,
    DROP COLUMN IF EXISTS sensor_summary,
    DROP COLUMN IF EXISTS sensor_processing,
    DROP COLUMN IF EXISTS sensor_state,
    DROP COLUMN IF EXISTS sensor_version;
