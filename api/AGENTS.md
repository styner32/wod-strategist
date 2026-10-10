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
* **Framework:** Every test is a Ginkgo spec (`Describe` / `It`, or `DescribeTable` + `Entry` for table-driven cases) with Gomega assertions (`Expect(...)`), including pure-function unit tests. The only `func TestXxx(t *testing.T)` allowed is the `RunSpecs` suite runner in `*_suite_test.go`; `Benchmark...` functions are the only other exception.
* **Existing `testing.T` tests are violations, not precedent.** Some test files still use `func TestXxx(t *testing.T)`. When adding or changing a test in such a file, convert that file to Ginkgo in the same change. This is in scope, not an unrelated refactor (same rule as pulling handlers out of `handlers.go`). Never append another `TestXxx` or mix both styles in one file.
* **Running specs:** From `api/`: `go test ./internal/<pkg> -count=1 -ginkgo.focus='<Describe text>'` (add `-p 1` for multiple packages). A package's `BeforeSuite` may need PostgreSQL/Redis even when the focused spec is pure. If the suite cannot run locally, report that the tests were not run. Never rewrite a test as `testing.T` to get a run that skips the suite.
* **Package Init:** Add a `*_suite_test.go` file with `RunSpecs(...)` when adding tests to a new package.
* **Mocking:** Do **NOT** create `fake*` structs for interfaces (e.g., `fakeStorage`). Use real clients backed by `api/internal/testhelpers.MockTransport`.
* **Outbound HTTP unit tests:** Prefer `testhelpers.MockTransport` at the transport layer before falling back to ad-hoc servers.

```go
// ❌ Don't
func TestEscapeLikePattern(t *testing.T) {
	if got := escapeLikePattern("dead_lift"); got != `dead\_lift` {
		t.Errorf("got %q", got)
	}
}

// ✅ Do
var _ = Describe("escapeLikePattern", func() {
	DescribeTable("escapes LIKE wildcards",
		func(input, want string) {
			Expect(escapeLikePattern(input)).To(Equal(want))
		},
		Entry("percent", "50% snatch", `50\% snatch`),
		Entry("underscore", "dead_lift", `dead\_lift`),
	)
})
```

## Worker Task Integration Testing (`internal/worker/*_test.go`)
Follow the **4-layer real-client strategy** (see [backend-testing.md](../docs/agent-memory/backend-testing.md)):
1. **Database:** Use real PostgreSQL (`wod_test`) via `testhelpers.InitDB()`. Truncate in `BeforeEach`.
2. **Gemini API:** Use real client + `MockTransport` (verify the upload/poll/generation/cleanup behavior of the specific path, not a universal request count).
3. **GCS Storage:** Use real client + `testhelpers.NewStorageClient` and `MockGCS*` helpers.
4. **Queue:** Use real asynq client (Redis DB 15) + `QueueInspector`.
* *Note: Wrap ffmpeg-dependent tests with `if !hasFfmpeg() { Skip(...) }`.*

## Database Migrations
* Use `golang-migrate`. Files live in `internal/db/migrations/`, named `000NNN_description.{up,down}.sql`.
* Commands (from `api/`): `make migrate-create NAME=...`, `make migrate-up`, `make migrate-down`, `make migrate-up-remote`, `make migrate-test-redo`.
* When adding or modifying a column, always create a matching migration pair. Use `ALTER TABLE ... ADD COLUMN ... DEFAULT`; `down.sql` must use `DROP COLUMN IF EXISTS`.

## Video Analysis Architecture (Two-Pass)
* **Pass 1 (Indexing):** Prefer verified `media_start_secs` and `media_end_secs` from `chunk_analysis_results`. Never apply capture-clock `start_secs`/`end_secs` to merged media. If no usable verified segments remain, fallback to `IndexVideo` with strict video duration constraints.
* **Pass 2 (Deep Analysis):** Analyze segments independently using `VideoMetadata` (Start/End offset) to prevent hallucination.
* **File Lifecycle:** Two-pass analysis uploads once and reuses the file URI. Current success paths retain file metadata for reuse; explicit cleanup is not guaranteed on every success/injury path. Follow the current lifecycle and remaining COST-07 work in [video-analysis.md](../docs/agent-memory/video-analysis.md#file-lifecycle).

## Error Handling & Initialization
* Validate runtime environment variables in `internal/config` during startup, NOT lazily.
* Return `error` values in `internal/` packages. Process termination (`panic` / `os.Exit`) belongs only in `cmd/server` and `cmd/worker`.

## 🚫 CRITICAL CONSTRAINTS (Never do these)
* **NEVER use `db.AutoMigrate()`**. All schema changes must go through versioned `golang-migrate` SQL files (`up.sql`/`down.sql`).
* **NEVER** drop columns without `IF EXISTS` in `down.sql`. Use `ALTER TABLE ... ADD COLUMN ... DEFAULT` to protect existing rows.
* **NEVER** use the Gemini `CachedContent` API with `VideoMetadata` (they are incompatible). Use the Files API upload as the cache layer.
* **NEVER** write `func TestXxx(t *testing.T)` test functions. Write Ginkgo specs with Gomega `Expect`. The only exceptions are the `RunSpecs` suite runner in `*_suite_test.go` and `Benchmark...` functions.
