ALTER TABLE token_usages
    ADD COLUMN thinking_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN tool_use_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN cached_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN unmeasured_calls SMALLINT NOT NULL DEFAULT 0;

UPDATE token_usages
SET
    thinking_tokens = CASE
        WHEN jsonb_typeof(usage_metadata) = 'object' AND (usage_metadata->>'thoughtsTokenCount') ~ '^-?[0-9]+$'
        THEN (usage_metadata->>'thoughtsTokenCount')::bigint
        ELSE 0
    END,
    tool_use_tokens = CASE
        WHEN jsonb_typeof(usage_metadata) = 'object' AND (usage_metadata->>'toolUsePromptTokenCount') ~ '^-?[0-9]+$'
        THEN (usage_metadata->>'toolUsePromptTokenCount')::bigint
        ELSE 0
    END,
    cached_tokens = CASE
        WHEN jsonb_typeof(usage_metadata) = 'object' AND (usage_metadata->>'cachedContentTokenCount') ~ '^-?[0-9]+$'
        THEN (usage_metadata->>'cachedContentTokenCount')::bigint
        ELSE 0
    END,
    unmeasured_calls = CASE
        WHEN usage_metadata IS NOT NULL AND (
            jsonb_typeof(usage_metadata) <> 'object'
            OR usage_metadata->>'promptTokenCount' IS NULL
            OR usage_metadata->>'candidatesTokenCount' IS NULL
            OR usage_metadata->>'totalTokenCount' IS NULL
        )
        THEN 1
        ELSE 0
    END
WHERE usage_metadata IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_token_usages_profile_created
    ON token_usages (profile_id, created_at);
