DROP INDEX IF EXISTS idx_token_usages_profile_created;

ALTER TABLE token_usages
    DROP COLUMN IF EXISTS unmeasured_calls,
    DROP COLUMN IF EXISTS cached_tokens,
    DROP COLUMN IF EXISTS tool_use_tokens,
    DROP COLUMN IF EXISTS thinking_tokens;
