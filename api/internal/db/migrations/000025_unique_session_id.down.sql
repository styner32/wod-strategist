-- Revert to a non-unique index.
DROP INDEX IF EXISTS idx_analysis_results_session_id;
CREATE INDEX idx_analysis_results_session_id ON analysis_results(session_id);
