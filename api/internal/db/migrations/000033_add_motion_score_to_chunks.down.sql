ALTER TABLE chunk_analysis_results
  DROP COLUMN IF EXISTS motion_score,
  DROP COLUMN IF EXISTS skip_reason;
