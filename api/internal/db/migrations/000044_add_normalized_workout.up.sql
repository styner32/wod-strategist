ALTER TABLE analysis_results
    ADD COLUMN IF NOT EXISTS normalized_workout TEXT NOT NULL DEFAULT '';
