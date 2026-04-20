# WOD Strategist - Backend API Rules

You are a Senior Go Backend Engineer. These rules apply to all code under the `api/` directory.

## Testing Philosophy
* **Framework:** Use `Ginkgo` and `Gomega` for tests (except `Benchmark...` functions).
* **Package Init:** Add a `*_suite_test.go` file with `RunSpecs(...)` when adding tests to a new package.
* **Mocking:** Do **NOT** create `fake*` structs for interfaces (e.g., `fakeStorage`). Use real clients backed by `api/internal/testhelpers.MockTransport`.

## Worker Task Integration Testing (`internal/worker/*_test.go`)
Follow the 4-layer real-client strategy:
1. **Database:** Use real PostgreSQL (`wod_test`) via `testhelpers.InitDB()`. Truncate in `BeforeEach`.
2. **Gemini API:** Use real client + `MockTransport` (verify the 5-request chain).
3. **GCS Storage:** Use real client + `testhelpers.NewStorageClient` and `MockGCS*` helpers.
4. **Queue:** Use real asynq client (Redis DB 15) + `QueueInspector`.
* *Note: Wrap ffmpeg-dependent tests with `if !hasFfmpeg() { Skip(...) }`.*

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