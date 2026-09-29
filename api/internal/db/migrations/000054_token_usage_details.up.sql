ALTER TABLE token_usages
    ALTER COLUMN prompt_tokens TYPE BIGINT,
    ALTER COLUMN candidate_tokens TYPE BIGINT,
    ALTER COLUMN total_tokens TYPE BIGINT,
    ADD COLUMN usage_metadata JSONB DEFAULT NULL,
    ADD COLUMN request_key TEXT DEFAULT NULL,
    ADD COLUMN user_id BIGINT DEFAULT NULL REFERENCES users(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX idx_token_usages_request_key ON token_usages(request_key);
CREATE INDEX idx_token_usages_user_id ON token_usages(user_id);
