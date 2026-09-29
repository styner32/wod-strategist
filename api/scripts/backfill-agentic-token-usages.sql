-- Run after migration 000054. No model calls. Idempotent by run/index/key.
-- Only retained metrics can be recovered, not overwritten runs or summary usage.
INSERT INTO token_usages (session_id, profile_id, task_type, model,
    prompt_tokens, candidate_tokens, total_tokens, usage_metadata, request_key, created_at)
SELECT a.session_id, a.profile_id, 'highlight:agentic',
    COALESCE(NULLIF(a.agentic_highlight_analysis->>'model', ''), 'gemini-3.8-flash'),
    COALESCE((item->'metrics'->'usage'->>'promptTokenCount')::bigint, 0),
    COALESCE((item->'metrics'->'usage'->>'candidatesTokenCount')::bigint, 0),
    COALESCE((item->'metrics'->'usage'->>'totalTokenCount')::bigint, 0),
    item->'metrics'->'usage',
    'agentic:' || (a.agentic_highlight_analysis->>'run_id') || ':' || (ordinality-1)::text || ':' || (item->>'key'),
    COALESCE((item->'metrics'->>'started_at')::timestamptz, a.created_at)
FROM analysis_results a
CROSS JOIN LATERAL jsonb_array_elements(
    CASE WHEN jsonb_typeof(a.agentic_highlight_analysis->'items') = 'array'
      THEN a.agentic_highlight_analysis->'items' ELSE '[]'::jsonb END
) WITH ORDINALITY AS items(item, ordinality)
WHERE NULLIF(a.agentic_highlight_analysis->>'run_id', '') IS NOT NULL
  AND NULLIF(item->>'key', '') IS NOT NULL
  AND jsonb_typeof(item->'metrics'->'usage') = 'object'
ON CONFLICT (request_key) DO NOTHING;
