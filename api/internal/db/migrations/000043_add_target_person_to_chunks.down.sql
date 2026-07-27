ALTER TABLE chunk_analysis_results
    DROP COLUMN IF EXISTS target_confidence,
    DROP COLUMN IF EXISTS target_cues;
