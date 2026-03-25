ALTER TABLE chunk_analysis_results DROP COLUMN IF EXISTS profile_id;
ALTER TABLE analysis_results DROP COLUMN IF EXISTS profile_id;
DROP TABLE IF EXISTS profiles;
