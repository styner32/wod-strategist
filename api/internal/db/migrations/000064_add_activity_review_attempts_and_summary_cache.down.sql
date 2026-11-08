DROP TRIGGER IF EXISTS trg_chunk_analysis_invalidate_summary_update ON chunk_analysis_results;
DROP TRIGGER IF EXISTS trg_chunk_analysis_invalidate_summary_insert_delete ON chunk_analysis_results;
DROP FUNCTION IF EXISTS invalidate_session_activity_summary_cache();

ALTER TABLE sessions
    DROP COLUMN IF EXISTS activity_summary_cache_version,
    DROP COLUMN IF EXISTS activity_review_attempts;
