CREATE TABLE session_reanalysis_runs (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    profile_id BIGINT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    client_request_id VARCHAR(128) NOT NULL,
    task_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'QUEUED'
        CHECK (status IN (
            'QUEUED',
            'RUNNING',
            'COMPLETED',
            'FAILED',
            'VIDEO_UNAVAILABLE',
            'CONTEXT_UNAVAILABLE'
        )),
    source_gcs_uri TEXT NOT NULL DEFAULT '',
    source_context_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    original_analysis_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    output TEXT NOT NULL DEFAULT '',
    highlight_segments TEXT NOT NULL DEFAULT '',
    session_score TEXT NOT NULL DEFAULT '{}',
    workout_type TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    prompt_hash TEXT NOT NULL DEFAULT '',
    schema_version TEXT NOT NULL DEFAULT '',
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

CREATE INDEX idx_session_reanalysis_runs_session_created
    ON session_reanalysis_runs (profile_id, session_id, created_at DESC);

CREATE INDEX idx_session_reanalysis_runs_profile_created
    ON session_reanalysis_runs (profile_id, created_at DESC);

CREATE UNIQUE INDEX idx_session_reanalysis_runs_one_active_per_session
    ON session_reanalysis_runs (profile_id, session_id)
    WHERE status IN ('QUEUED', 'RUNNING');
