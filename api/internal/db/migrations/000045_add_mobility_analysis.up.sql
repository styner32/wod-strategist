ALTER TABLE analysis_results ADD COLUMN IF NOT EXISTS mobility_observations TEXT NOT NULL DEFAULT '[]';
ALTER TABLE analysis_results ADD COLUMN IF NOT EXISTS stretch_recommendations TEXT NOT NULL DEFAULT '[]';
