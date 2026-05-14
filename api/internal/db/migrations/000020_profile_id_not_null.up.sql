-- Backfill orphan analysis_results to the seed profile (id=1).
UPDATE analysis_results SET profile_id = 1 WHERE profile_id IS NULL;

-- Delete orphan rows in leaf tables that reference missing analyses.
DELETE FROM chunk_analysis_results WHERE profile_id IS NULL;
DELETE FROM highlight_results WHERE profile_id IS NULL;
DELETE FROM token_usages WHERE profile_id IS NULL;

-- Now enforce NOT NULL on all four tables.
ALTER TABLE analysis_results       ALTER COLUMN profile_id SET NOT NULL;
ALTER TABLE chunk_analysis_results ALTER COLUMN profile_id SET NOT NULL;
ALTER TABLE highlight_results      ALTER COLUMN profile_id SET NOT NULL;
ALTER TABLE token_usages           ALTER COLUMN profile_id SET NOT NULL;
