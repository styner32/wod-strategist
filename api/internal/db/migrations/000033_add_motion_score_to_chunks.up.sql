ALTER TABLE chunk_analysis_results
  ADD COLUMN motion_score double precision,
  ADD COLUMN skip_reason text NOT NULL DEFAULT '';
