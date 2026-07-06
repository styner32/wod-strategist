ALTER TABLE analysis_results
  DROP COLUMN IF EXISTS gemini_file_uri,
  DROP COLUMN IF EXISTS gemini_file_name,
  DROP COLUMN IF EXISTS gemini_mime_type,
  DROP COLUMN IF EXISTS gemini_file_expires_at;
