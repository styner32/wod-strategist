ALTER TABLE chunk_analysis_results
    ADD COLUMN capture_assessment JSONB DEFAULT NULL,
    ADD COLUMN contextual_coaching JSONB DEFAULT NULL,
    ADD COLUMN movement_observations JSONB DEFAULT NULL;
ALTER TABLE sessions ADD COLUMN activity_summary JSONB DEFAULT NULL;
