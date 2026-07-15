ALTER TABLE chunk_analysis_results
    ADD COLUMN IF NOT EXISTS media_start_secs DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS media_end_secs DOUBLE PRECISION;

-- Server-split uploads already use offsets relative to the uploaded session
-- video. Mobile capture-clock offsets are intentionally not backfilled.
UPDATE chunk_analysis_results
SET media_start_secs = start_secs,
    media_end_secs = end_secs
WHERE file_path LIKE '%/split_chunk_%'
  AND start_secs IS NOT NULL
  AND end_secs IS NOT NULL
  AND end_secs > start_secs
  AND media_start_secs IS NULL
  AND media_end_secs IS NULL;

CREATE TABLE chunk_reanalysis_runs (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    profile_id BIGINT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    chunk_analysis_result_id BIGINT NOT NULL REFERENCES chunk_analysis_results(id) ON DELETE CASCADE,
    client_request_id VARCHAR(128) NOT NULL,
    task_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'QUEUED'
        CHECK (status IN (
            'QUEUED',
            'RUNNING',
            'COMPLETED',
            'FAILED',
            'VIDEO_UNAVAILABLE',
            'INTERVAL_UNAVAILABLE'
        )),
    source_kind TEXT NOT NULL DEFAULT ''
        CHECK (source_kind IN ('', 'session_video', 'chunk')),
    source_gcs_uri TEXT NOT NULL DEFAULT '',
    source_context_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    original_prediction_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    media_start_secs DOUBLE PRECISION,
    media_end_secs DOUBLE PRECISION,
    model TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    prompt_hash TEXT NOT NULL DEFAULT '',
    schema_version TEXT NOT NULL DEFAULT '',
    raw_output TEXT NOT NULL DEFAULT '',
    structured_candidate JSONB NOT NULL DEFAULT '{}'::jsonb,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    candidate_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    safe_error TEXT NOT NULL DEFAULT '',
    gemini_file_uri TEXT NOT NULL DEFAULT '',
    gemini_file_name TEXT NOT NULL DEFAULT '',
    gemini_mime_type TEXT NOT NULL DEFAULT '',
    gemini_file_expires_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (profile_id, client_request_id)
);

CREATE INDEX idx_chunk_reanalysis_runs_session_chunk
    ON chunk_reanalysis_runs (session_id, chunk_analysis_result_id, created_at DESC);

CREATE INDEX idx_chunk_reanalysis_runs_profile_created
    ON chunk_reanalysis_runs (profile_id, created_at DESC);

CREATE UNIQUE INDEX idx_chunk_reanalysis_runs_one_active_per_chunk
    ON chunk_reanalysis_runs (chunk_analysis_result_id)
    WHERE status IN ('QUEUED', 'RUNNING');

ALTER TABLE analysis_feedback
    ADD CONSTRAINT analysis_feedback_reanalysis_run_id_fkey
    FOREIGN KEY (reanalysis_run_id)
    REFERENCES chunk_reanalysis_runs(id)
    ON DELETE SET NULL;
