CREATE TABLE gemini_thought_logs (
    id BIGSERIAL PRIMARY KEY,
    token_usage_id BIGINT DEFAULT NULL REFERENCES token_usages(id) ON DELETE CASCADE,
    user_id BIGINT DEFAULT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile_id BIGINT NOT NULL DEFAULT 0,
    session_id TEXT NOT NULL DEFAULT '',
    task_type TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    request_key TEXT DEFAULT NULL,
    thinking_tokens BIGINT NOT NULL DEFAULT 0,
    thought_summary TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_gemini_thought_logs_request_key ON gemini_thought_logs(request_key);
CREATE INDEX idx_gemini_thought_logs_session_id ON gemini_thought_logs(session_id, created_at);
CREATE INDEX idx_gemini_thought_logs_token_usage_id ON gemini_thought_logs(token_usage_id);
CREATE INDEX idx_gemini_thought_logs_user_id ON gemini_thought_logs(user_id);
