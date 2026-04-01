ALTER TABLE chunk_analysis_results ADD COLUMN IF NOT EXISTS file_path TEXT NOT NULL DEFAULT '';
