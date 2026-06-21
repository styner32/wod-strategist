ALTER TABLE analysis_results
  ADD COLUMN IF NOT EXISTS wod_description TEXT NOT NULL DEFAULT '';
