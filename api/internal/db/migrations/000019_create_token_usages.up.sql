CREATE TABLE IF NOT EXISTS token_usages (
    id               BIGSERIAL PRIMARY KEY,
    session_id       TEXT NOT NULL,
    profile_id       INTEGER,
    task_type        TEXT NOT NULL,
    model            TEXT NOT NULL,
    prompt_tokens    INTEGER NOT NULL DEFAULT 0,
    candidate_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens     INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_token_usages_session_id ON token_usages(session_id);
