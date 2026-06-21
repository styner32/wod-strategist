CREATE TABLE IF NOT EXISTS sessions (
    id               BIGSERIAL PRIMARY KEY,
    session_id       TEXT NOT NULL UNIQUE, -- session unique name
    status           TEXT NOT NULL DEFAULT 'started', -- started, completed, failed
    idempotency_key  TEXT NOT NULL, -- device generated id to prevent duplicate session creation
    profile_id       INTEGER NOT NULL,
    wod_description  TEXT,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);