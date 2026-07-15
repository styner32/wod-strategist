CREATE TABLE analysis_feedback (
    id BIGSERIAL PRIMARY KEY,
    feedback_key TEXT NOT NULL,
    profile_id BIGINT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    target_type TEXT NOT NULL
        CHECK (target_type IN ('session', 'chunk')),
    chunk_analysis_result_id BIGINT REFERENCES chunk_analysis_results(id) ON DELETE CASCADE,
    category TEXT NOT NULL
        CHECK (category IN ('session_accuracy', 'movement', 'activity', 'fatigue', 'other')),
    original_prediction JSONB NOT NULL DEFAULT '{}'::jsonb,
    correction JSONB NOT NULL DEFAULT '{}'::jsonb,
    note TEXT NOT NULL DEFAULT ''
        CHECK (char_length(note) <= 500),
    consent_to_improve BOOLEAN NOT NULL DEFAULT FALSE,
    client_request_id VARCHAR(128) NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1
        CHECK (revision > 0),
    supersedes_feedback_id BIGINT REFERENCES analysis_feedback(id) ON DELETE CASCADE,
    retracted BOOLEAN NOT NULL DEFAULT FALSE,
    reanalysis_run_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT analysis_feedback_target_check CHECK (
        (target_type = 'session' AND chunk_analysis_result_id IS NULL)
        OR
        (target_type = 'chunk' AND chunk_analysis_result_id IS NOT NULL)
    ),
    CONSTRAINT analysis_feedback_category_target_check CHECK (
        (target_type = 'session' AND category IN ('session_accuracy', 'other'))
        OR
        (target_type = 'chunk' AND category IN ('movement', 'activity', 'fatigue', 'other'))
    ),
    UNIQUE (profile_id, client_request_id),
    UNIQUE (feedback_key, revision)
);

CREATE INDEX idx_analysis_feedback_session_created
    ON analysis_feedback (profile_id, session_id, created_at DESC);

CREATE INDEX idx_analysis_feedback_chunk_created
    ON analysis_feedback (chunk_analysis_result_id, created_at DESC)
    WHERE chunk_analysis_result_id IS NOT NULL;
