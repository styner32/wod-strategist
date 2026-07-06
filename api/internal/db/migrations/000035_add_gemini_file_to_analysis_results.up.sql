ALTER TABLE analysis_results
  ADD COLUMN gemini_file_uri text NOT NULL DEFAULT '',
  ADD COLUMN gemini_file_name text NOT NULL DEFAULT '',
  ADD COLUMN gemini_mime_type text NOT NULL DEFAULT '',
  ADD COLUMN gemini_file_expires_at timestamptz;
