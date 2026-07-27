ALTER TABLE profiles ADD COLUMN appearance JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE profiles ADD CONSTRAINT profiles_appearance_object_check
    CHECK (jsonb_typeof(appearance) = 'object');

CREATE TABLE IF NOT EXISTS session_appearance_hints (
    id          BIGSERIAL PRIMARY KEY,
    session_id  TEXT NOT NULL UNIQUE,
    profile_id  INTEGER NOT NULL REFERENCES profiles(id),
    hints       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT session_appearance_hints_object_check
        CHECK (jsonb_typeof(hints) = 'object')
);
CREATE INDEX IF NOT EXISTS idx_session_appearance_hints_profile_id
    ON session_appearance_hints(profile_id);
