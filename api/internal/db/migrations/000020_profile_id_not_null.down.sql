ALTER TABLE analysis_results       ALTER COLUMN profile_id DROP NOT NULL;
ALTER TABLE chunk_analysis_results ALTER COLUMN profile_id DROP NOT NULL;
ALTER TABLE highlight_results      ALTER COLUMN profile_id DROP NOT NULL;
ALTER TABLE token_usages           ALTER COLUMN profile_id DROP NOT NULL;
