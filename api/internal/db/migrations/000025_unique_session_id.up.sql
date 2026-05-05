-- Remove any duplicate session_ids (keep the newest row by id).
DELETE FROM analysis_results
WHERE id NOT IN (
    SELECT MAX(id) FROM analysis_results GROUP BY session_id
);

-- Replace the non-unique index with a unique constraint.
DROP INDEX IF EXISTS idx_analysis_results_session_id;
CREATE UNIQUE INDEX idx_analysis_results_session_id ON analysis_results(session_id);
