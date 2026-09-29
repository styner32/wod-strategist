# Token usage accounting

## Persistence (2026-09-28)
- Migration `000054_token_usage_details` adds `usage_metadata JSONB DEFAULT NULL`, `request_key TEXT DEFAULT NULL` (unique), and `user_id BIGINT DEFAULT NULL REFERENCES users(id) ON DELETE CASCADE`. The three legacy counters are widened to BIGINT. Down removes the new columns/indexes but deliberately retains BIGINT to avoid overflowing recorded counts.
- `gemini.Client.generateContent` captures raw nullable usage before SDK validation. Empty/blocked/malformed answers keep returned usage. An attempted generation with no usage returns model + missing details, not a measured zero. Upload/poll failures before inference still return nil usage.
- `cost.RecordUsage` is the shared DB write. Call it immediately after generation, before any error/JSON validation/fingerprint return. No request, answer, thought text, headers, or credentials enter `token_usages`.
- `usage_metadata` preserves nullable prompt/candidate/thinking/tool/cache/total fields. New unknown usage is `{}`; legacy rows keep SQL NULL. Scalar zeros remain a backward-compatible aggregate projection, not proof of measured zero. `unmeasured_calls` flags new rows missing prompt/output/total. Historical rows without details cannot retrospectively expose unknown thinking/tool use.
- Streaming uses the final reported usage snapshot, never the sum of cumulative SSE snapshots. API errors or truncated streams retain any usage already reported.
- Agentic key: `agentic:<run_id>:<zero-based-item-index>:<highlight-key>`. Index distinguishes identical highlight entries. Summary key: `summary:<run_id>`. Same persistence delivery is ignored; manual reruns get a new run ID. Stale observation rejection does not discard actual consumption.
- Worker writes remain best effort and log DB errors, following the existing tracking policy. A process death before receiving/persisting usage cannot be reconstructed from this ledger alone.

## Coverage and ownership
- Agentic `highlight:agentic`, summary `analysis:summary`, existing indexing/triage/segment/reanalysis/injury/normalization/stretch/TTS calls are recorded before response validation.
- `highlight:music` records Lyria token usage when supplied. Lyria uses a non-token price; the calculator excludes its monetary estimate and exposes `unpriced_calls` rather than applying Flash fallback prices.
- `image:workout` and `image:appearance` run before session/profile selection: `session_id=''`, `profile_id=0`, authenticated `user_id`. Do not fabricate a session/profile. Account totals include these rows; profile-filtered totals exclude them.
- `strategy:pre-wod` records authenticated user and validated profile, with no session. User deletion cascades account-only records.
- Local static/agentic comparison CLI remains intentionally read-only with respect to production analysis and token ledgers; its reports preserve usage independently.

## Cost estimates and UI
- Existing configured model rates and FX are unchanged. Estimate inputs are prompt + tool-use, outputs are candidate + thinking. Cached tokens are already a subset of prompt; never add them again. Current estimates do not model cache discounts, service tiers, promotional dates, or provider invoice adjustments.
- See [Google video token categories](https://ai.google.dev/gemini-api/docs/generate-content/video-understanding#technical-details-about-videos) and [pricing](https://ai.google.dev/gemini-api/docs/pricing). Current published rates differ from some configured rates: rate-policy refresh is separate from this accounting correction. UI labels the values as configured-rate estimates, not actual billing.
- Session and account APIs expose thinking/tool/cache totals plus `unmeasured_calls` and `unpriced_calls`. Breakdown subtotals include the same categories.
- `AnalysisOverview` invalidates `session-cost` and `total-cost` when run/status/summary timestamp or per-highlight status changes, including initial load. Query changes unrelated to enrichment do not cause continuous cost polling.

## Recoverable historical usage
- After migration, run `api/scripts/backfill-agentic-token-usages.sql` through the normal DB access workflow. It makes no model calls. Repeating it is idempotent and its keys match live recording.
- Only latest retained `agentic_highlight_analysis.items[].metrics.usage` is recoverable, including failed items. Missing counters stay null in metadata; no usage is invented for discarded summary calls, overwritten runs, or missing responses.
- This change does not apply migrations/backfill to production or trigger paid generation automatically.
