CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    token_version INTEGER NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

-- Case-insensitive unique index on username, only for active (non-deleted) users
CREATE UNIQUE INDEX idx_users_username_lower
    ON users (LOWER(username))
    WHERE deleted_at IS NULL;
