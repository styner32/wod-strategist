-- Keep this predicate identical to enrichmentOpenPredicate in
-- internal/worker/analysis_enrichment.go so the 30-second recovery scan uses it.
CREATE INDEX idx_analysis_results_enrichment_open
    ON analysis_results (session_id)
    WHERE analysis_summary->>'status' IN ('pending', 'running', 'stale')
       OR agentic_highlight_analysis->>'status' IN ('pending', 'preparing', 'running', 'stale')
       OR agentic_highlight_analysis->>'cleanup_pending' = 'true';
