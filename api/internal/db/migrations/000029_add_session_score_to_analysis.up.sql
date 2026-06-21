ALTER TABLE analysis_results
  ADD COLUMN IF NOT EXISTS session_score TEXT NOT NULL DEFAULT '{}';
