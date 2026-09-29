DROP INDEX IF EXISTS idx_token_usages_request_key;
DROP INDEX IF EXISTS idx_token_usages_user_id;
ALTER TABLE token_usages
    DROP COLUMN IF EXISTS usage_metadata,
    DROP COLUMN IF EXISTS request_key,
    DROP COLUMN IF EXISTS user_id;
-- Keep widened counters: narrowing would overflow already recorded Agentic usage.
