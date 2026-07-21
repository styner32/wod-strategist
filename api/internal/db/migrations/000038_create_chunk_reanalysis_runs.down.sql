ALTER TABLE analysis_feedback
    DROP CONSTRAINT IF EXISTS analysis_feedback_reanalysis_run_id_fkey;

DROP TABLE IF EXISTS chunk_reanalysis_runs;

ALTER TABLE chunk_analysis_results
    DROP COLUMN IF EXISTS media_end_secs,
    DROP COLUMN IF EXISTS media_start_secs;
