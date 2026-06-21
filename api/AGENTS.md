# WOD Strategist - Backend API Rules

You are a Senior Go Backend Engineer. These rules apply to all code under the `api/` directory.

For detailed patterns see:
- [../docs/agent-memory/go-conventions.md](../docs/agent-memory/go-conventions.md)
- [../docs/agent-memory/backend-testing.md](../docs/agent-memory/backend-testing.md)
- [../docs/agent-memory/migrations.md](../docs/agent-memory/migrations.md)
- [../docs/agent-memory/video-analysis.md](../docs/agent-memory/video-analysis.md)
- [../docs/agent-memory/storage-and-session-format.md](../docs/agent-memory/storage-and-session-format.md)

## Architecture direction (apply when writing or touching code)
* **Schema lives in migrations, not gorm tags.** `NOT NULL`, defaults, `UNIQUE`, FKs, and indexes go in `.up.sql`. Gorm structs are Go-side row shapes only. See [migrations.md](../docs/agent-memory/migrations.md#schema-authority--migrations-not-struct-tags).
* **Split handler files by domain.** `handlers.go` is legacy and should not grow. New endpoints get their own file (`session_handlers.go`, etc.). Pull existing handlers out when touched.
* **No repository pattern.** Handlers call `ctl.db` directly. Existing `*Repository` interfaces are being removed; don't add new ones.
* **One `Describe` per route in integration tests**, named after the route. No omnibus `Describe` blocks.
* **Use factories in `testhelpers/factory.go`** (`CreateUser`, `CreateProfile`, ...) for test setup. No inline `dbConn.Create(&db.Foo{...})`.

## Testing Philosophy
* **Framework:** Use `Ginkgo` and `Gomega` for tests (except `Benchmark...` functions).
* **Package Init:** Add a `*_suite_test.go` file with `RunSpecs(...)` when adding tests to a new package.
* **Mocking:** Do **NOT** create `fake*` structs for interfaces (e.g., `fakeStorage`). Use real clients backed by `api/internal/testhelpers.MockTransport`.
* **Outbound HTTP unit tests:** Prefer `testhelpers.MockTransport` at the transport layer before falling back to ad-hoc servers.

## Worker Task Integration Testing (`internal/worker/*_test.go`)
Follow the **4-layer real-client strategy** (see [backend-testing.md](../docs/agent-memory/backend-testing.md)):
1. **Database:** Use real PostgreSQL (`wod_test`) via `testhelpers.InitDB()`. Truncate in `BeforeEach`.
2. **Gemini API:** Use real client + `MockTransport` (verify the 5-request chain).
3. **GCS Storage:** Use real client + `testhelpers.NewStorageClient` and `MockGCS*` helpers.
4. **Queue:** Use real asynq client (Redis DB 15) + `QueueInspector`.
* *Note: Wrap ffmpeg-dependent tests with `if !hasFfmpeg() { Skip(...) }`.*

## Database Migrations
* Use `golang-migrate`. Files live in `internal/db/migrations/`, named `000NNN_description.{up,down}.sql`.
* Commands (from `api/`): `make migrate-create NAME=...`, `make migrate-up`, `make migrate-down`, `make migrate-up-remote`, `make migrate-test-redo`.
* When adding or modifying a column, always create a matching migration pair. Use `ALTER TABLE ... ADD COLUMN ... DEFAULT`; `down.sql` must use `DROP COLUMN IF EXISTS`.

## Video Analysis Architecture (Two-Pass)
* **Pass 1 (Indexing):** Prefer `start_secs` and `end_secs` from `chunk_analysis_results`. If no chunks exist, fallback to `IndexVideo` with strict video duration constraints.
* **Pass 2 (Deep Analysis):** Analyze segments independently using `VideoMetadata` (Start/End offset) to prevent hallucination.
* **File Lifecycle:** `UploadVideo` is called once. The analysis handler defers `DeleteFile` unless injuries exist, in which case the Injury handler calls `DeleteFile` at the end.

## Error Handling & Initialization
* Validate runtime environment variables in `internal/config` during startup, NOT lazily.
* Return `error` values in `internal/` packages. Process termination (`panic` / `os.Exit`) belongs only in `cmd/server` and `cmd/worker`.

## 🚫 CRITICAL CONSTRAINTS (Never do these)
* **NEVER use `db.AutoMigrate()`**. All schema changes must go through versioned `golang-migrate` SQL files (`up.sql`/`down.sql`).
* **NEVER** drop columns without `IF EXISTS` in `down.sql`. Use `ALTER TABLE ... ADD COLUMN ... DEFAULT` to protect existing rows.
* **NEVER** use the Gemini `CachedContent` API with `VideoMetadata` (they are incompatible). Use the Files API upload as the cache layer.
