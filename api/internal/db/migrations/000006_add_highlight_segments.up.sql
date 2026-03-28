ALTER TABLE analysis_results ADD COLUMN highlight_segments TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS highlight_results (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    profile_id BIGINT,
    status TEXT NOT NULL DEFAULT 'PENDING',
    gcs_uri TEXT NOT NULL DEFAULT '',
    segments TEXT NOT NULL DEFAULT '[]',
    duration_sec DOUBLE PRECISION NOT NULL DEFAULT 0,
    output TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_highlight_results_session_id ON highlight_results(session_id);
