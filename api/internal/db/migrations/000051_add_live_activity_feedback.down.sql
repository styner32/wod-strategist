ALTER TABLE sessions DROP COLUMN IF EXISTS activity_summary;
ALTER TABLE chunk_analysis_results
    DROP COLUMN IF EXISTS movement_observations,
    DROP COLUMN IF EXISTS contextual_coaching,
    DROP COLUMN IF EXISTS capture_assessment;
