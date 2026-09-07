ALTER TABLE analysis_results
    ADD COLUMN sensor_version BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN sensor_state TEXT NOT NULL DEFAULT 'NONE',
    ADD COLUMN sensor_processing JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN sensor_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN sensor_next_attempt_at TIMESTAMPTZ,
    ADD COLUMN workout_at TIMESTAMPTZ,
    ADD COLUMN workout_at_source TEXT;

ALTER TABLE analysis_results
    ADD CONSTRAINT analysis_results_sensor_version_nonnegative
        CHECK (sensor_version >= 0),
    ADD CONSTRAINT analysis_results_sensor_state_valid
        CHECK (sensor_state IN
            ('NONE', 'UPLOADING', 'PENDING', 'RUNNING', 'COMPLETED', 'FAILED', 'EXPIRED')),
    ADD CONSTRAINT analysis_results_sensor_json_objects
        CHECK (jsonb_typeof(sensor_processing) = 'object'
           AND jsonb_typeof(sensor_summary) = 'object');

CREATE INDEX idx_analysis_results_sensor_due
    ON analysis_results (sensor_next_attempt_at, id)
    WHERE sensor_state IN ('UPLOADING', 'PENDING', 'RUNNING');

CREATE INDEX idx_analysis_results_profile_workout_at
    ON analysis_results (profile_id, workout_at DESC, id DESC)
    WHERE status = 'COMPLETED' AND archived_at IS NULL;
