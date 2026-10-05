# Token usage completion — 2026-09-28

## Changes
- `api/internal/gemini/{usage.go,client.go,video_comparison.go}`: nullable response usage extraction shared across production generation; preserve usage on API/empty-content/audio-output errors. Lyria returns usage too.
- `api/internal/cost/{record.go,cost.go}`, `api/internal/db/db.go`, migration `000054_token_usage_details`: shared ledger insert, run-step deduplication, BIGINT counters, raw nullable usage categories, account attribution, thinking/tool-inclusive configured-rate estimates, incomplete/unpriced request counts.
- `api/internal/worker/{analysis_enrichment,video_analysis,verify_highlights,normalize_workout,stretch_recommendations,generate_hardsub,generate_highlight,injury_analysis,chunk_debug_reanalysis,session_debug_reanalysis,worker}.go`: record consumption before result rejection; Agentic/summary run keys; reruns accumulate.
- `api/internal/controllers/{image_handlers,token_usage,strategy_handlers,cost_handlers}.go`: image/strategy tracking and owned account totals. Existing image handlers moved out of the legacy `handlers.go` per repository rules.
- `web/src/api/{history,enrichment}.ts`, `web/src/history/components/{AnalysisOverview,SessionCostCard}.tsx`, `web/src/history/HistoryListPage.tsx`: cost refresh on enrichment progress/completion, token categories and incomplete-estimate notices.
- `api/scripts/backfill-agentic-token-usages.sql`: explicit, idempotent recovery of retained metrics, no inference.

Existing unrelated working-tree changes were preserved. No deployment, production migration/backfill, or live Gemini generation was performed.

## Verification
Run from `api/`, using isolated local PostgreSQL on port 55481 (`wod_test`) and Redis on port 6391 (DB 15):

```sh
TEST_DATABASE_URL='postgres://sunjinlee@127.0.0.1:55481/wod_test?sslmode=disable' \
TEST_REDIS_URL='127.0.0.1:6391' \
go test -p 1 ./internal/gemini ./internal/cost ./internal/worker ./internal/controllers

go build ./cmd/server ./cmd/worker

migrate -path internal/db/migrations -database 'postgres://sunjinlee@127.0.0.1:55481/wod_test?sslmode=disable' up
migrate -path internal/db/migrations -database 'postgres://sunjinlee@127.0.0.1:55481/wod_test?sslmode=disable' down 1
migrate -path internal/db/migrations -database 'postgres://sunjinlee@127.0.0.1:55481/wod_test?sslmode=disable' up
```

From `web/`: `npm run build`. From repository root: `git diff --check`.

All four Go packages passed (initial combined run followed by affected-package reruns as failures were corrected). Server/worker/web builds and migration roundtrip passed. Web build retains its existing large-bundle warning.

New tests cover:
- Empty replies and HTTP failures retain usage/missingness without hidden retries; missing counters differ from explicit zero; music response errors retain usage.
- Two summaries plus successful/invalid Agentic highlights each recorded once; duplicate deliveries excluded; actual reruns included; stale results still count consumption.
- Backfill repeated twice is idempotent; partial metadata and 5 billion tokens survive without int32 ledger overflow.
- Thinking/tool costs aggregate consistently; cache is not double-counted; unknown usage flagged; Lyria tokens retained with monetary estimate explicitly unpriced.
- Image/strategy failures record usage; images attributable to authenticated users with no profiles; account totals isolate other users and respect profile filters.
- Existing session-reanalysis test cleanup now accepts a missing empty Redis queue, instead of failing when isolated Redis starts without that queue.

## Application and limits
Apply migration 54 before starting updated services. Deploy API, worker and web. For historical recovery, run the SQL script once against the intended database via the normal operational workflow; it is safe to repeat without new inference. The rollback drops request keys, so do not recover historical records after such a rollback without first reconciling the existing ledger.

Only retained per-highlight metrics can be backfilled. Overwritten runs and discarded summary usage cannot be reconstructed. Existing model rates/FX remain configured-rate estimates; cache discounts, promotional pricing and non-token model prices are not provider-invoice reconciliation. See [accounting rules](agent-memory/token-usage.md).
