# Repository Instructions

- For Go tests in `/Users/sunjinlee/workspace/wod-strategist/api`, use `Ginkgo` and `Gomega` instead of plain `testing`-style test cases.
- When adding tests to a new Go package in `api`, add a `*_suite_test.go` file with `RunSpecs(...)`.
- For outbound HTTP unit tests in `api`, prefer `api/internal/testhelpers.MockTransport` to mock requests at the transport layer before falling back to ad-hoc servers.
- Standard Go benchmarks such as `Benchmark...` functions can stay in the normal `testing` package style.
- In `api`, required runtime environment variables must be validated in `internal/config` during startup initialization, not lazily inside request handlers or workers.
- In `api`, packages under `internal/` should return `error` values instead of calling `panic` or terminating the process directly. Process termination belongs in `cmd/server` and `cmd/worker`.

## Database Migrations

**Never use GORM AutoMigrate.** All schema changes must go through versioned SQL migration files.

### Migration files

- Location: `api/internal/db/migrations/`
- Tool: [golang-migrate](https://github.com/golang-migrate/migrate) (`migrate` CLI)
- Naming: sequential `000NNN_description.{up,down}.sql`

### Commands (run from `api/`)

| Command | Description |
|---------|-------------|
| `make migrate-create NAME=description` | Create a new up/down migration pair |
| `make migrate-up` | Apply all pending migrations locally |
| `make migrate-down` | Roll back the last migration locally |
| `make migrate-up-remote` | Run migrations on the remote (Cloud Run Jobs) |

### Rules for AI assistants

- When adding or modifying a column on a DB model in `internal/db/`, always create a corresponding migration file pair (`up.sql` + `down.sql`).
- Use `ALTER TABLE ... ADD COLUMN` with a `DEFAULT` value so existing rows are not broken.
- The `down.sql` must use `DROP COLUMN IF EXISTS` for safe rollback.
- Do **not** call `db.AutoMigrate()` anywhere — it bypasses version history and makes rollbacks impossible.

## Integration Testing for Worker Tasks

Worker handler tests (`internal/worker/*_test.go`) follow a **three-layer real-client** strategy.
No SQL mocking, no in-memory queues, no ad-hoc HTTP servers.

### Infrastructure (local, required to run worker tests)

| Dependency | Default | Override env var |
|---|---|---|
| PostgreSQL | `localhost:5432/wod_test` | `TEST_DATABASE_URL` |
| Redis (asynq) | `localhost:6379` DB **15** | `TEST_REDIS_URL` |

Run `make migrate-test-redo` from `api/` (see Makefile) to (re-)apply migrations against `wod_test`.

### Layer 1 — Database: real PostgreSQL

Use `testhelpers.InitDB()` to get a `*gorm.DB` connected to the `wod_test` database.
Call `testhelpers.CleanupDB(db)` in `BeforeEach` to truncate all tables (panics if DB name doesn't end in `_test` — a safety guard).

```go
BeforeEach(func() {
    dbConn, err = testhelpers.InitDB()
    Expect(err).NotTo(HaveOccurred())
    testhelpers.CleanupDB(dbConn)
})
```

### Layer 2 — Gemini API: real client + MockTransport

Use `testhelpers.NewMockTransport()` and wire it into a real `gemini.Client` via `gemini.NewClientWithOptions`.
Register the standard 5-request chain per test, then call `transport.Verify()` afterwards.

```go
transport := testhelpers.NewMockTransport()
realClient, _ := gemini.NewClientWithOptions(ctx, zap.NewNop(), gemini.Options{
    APIKey:       "test-api-key",
    BaseURL:      "https://generativelanguage.googleapis.com",
    HTTPClient:   &http.Client{Transport: transport},
    PollInterval: time.Millisecond,
    Sleep:        func(time.Duration) {},
})
w.GeminiClient = realClient

// Register expectations: upload-start → upload-finalize → poll → generateContent → deleteFile
transport.New(geminiBaseURL).Post("/upload/v1beta/files"). ...

Expect(w.HandleVideoAnalysisTask(ctx, task)).To(Succeed())
Expect(transport.Verify()).To(Succeed())
Expect(transport.Requests()).To(HaveLen(5))
```

The `setupGeminiTransport(text)` helper inside each `Describe` block abstracts the 5-step sequence for the happy path.

**Important:** Defer cleanup (`DeleteFile`) is registered **before** any early-return check (e.g. empty analysis). This prevents Gemini file leaks and is what the `MockTransport.Verify()` assertion catches.

### Layer 3 — GCS Storage: fakeStorage or real client + MockTransport

- **`fakeStorage`** (in `worker_test_helpers_test.go`): use when storage is incidental to the test (e.g. the handler downloads a file but GCS behaviour is not what you're verifying). `DownloadFile` writes a 1-byte sentinel; `ListObjects` returns nil.
- **`testhelpers.NewStorageClient(bucketName, transport)`**: use when you need to verify exact GCS HTTP interactions. Returns a real `storage.Client` backed by `MockTransport`.

```go
// When GCS is incidental:
w.StorageClient = &fakeStorage{}

// When GCS calls need explicit verification:
transport := testhelpers.NewMockTransport()
transport.New("https://storage.googleapis.com").Get("/bucket/object").Reply(200).BodyString("data")
client, _ := testhelpers.NewStorageClient("my-bucket", transport)
w.StorageClient = client
```

### Layer 4 — Queue (asynq / Redis): real client + Inspector

Use `testhelpers.NewQueueClient()` as the worker's `QueueClient`. Use `testhelpers.NewQueueInspector()` to read back what was enqueued. Always call `testhelpers.CleanupQueue(inspector)` in `BeforeEach`.

```go
BeforeEach(func() {
    queueClient = testhelpers.NewQueueClient()
    inspector   = testhelpers.NewQueueInspector()
    testhelpers.CleanupQueue(inspector)
    w.QueueClient = queueClient
})

It("enqueues an injury follow-up task", func() {
    Expect(w.HandleVideoAnalysisTask(ctx, task)).To(Succeed())

    pending, err := inspector.ListPendingTasks("default")
    Expect(err).NotTo(HaveOccurred())
    Expect(pending).To(HaveLen(1))
    Expect(pending[0].Type).To(Equal(TypeInjuryAnalysis))

    var payload InjuryAnalysisPayload
    Expect(json.Unmarshal(pending[0].Payload, &payload)).To(Succeed())
    Expect(payload.FocusTimestamps).To(ContainSubstring("0:32"))
})
```

The real queue uses **Redis DB 15** — separate from the app's DB 5 — so tests never interfere with a running local server.

### Shared test helpers (`worker_test_helpers_test.go`)

All shared fakes and utilities live in one file so future migration to exported `testhelpers` is a single-file move:

| Symbol | Purpose |
|---|---|
| `fakeStorage` | Incidental storage stub (DownloadFile → sentinel byte) |
| `listableStorage` | Storage stub with configurable `ListObjects` + optional real file serving (used by merge/highlight tests) |
| `fakeGemini` | Gemini stub for tests where Gemini is NOT the subject (e.g. merge_chunks) |
| `makeVideoAnalysisTask(p)` | Marshals a payload into an `*asynq.Task` |
| `hasFfmpeg()` | Returns true if ffmpeg is in PATH |
| `createTinyMP4(t)` | Creates a 1-second black mp4 via ffmpeg; skips test if unavailable |
| `copyFile / writeFile` | File system helpers used by `listableStorage` |

### FFmpeg-dependent tests

Wrap ffmpeg-dependent `It` blocks inside `Context("when ffmpeg is available")` with a `BeforeEach` that calls `Skip(...)` if `hasFfmpeg()` is false. Use `createTinyMP4(GinkgoT())` to get a real playable video without any external fixtures.

```go
Context("when ffmpeg is available", func() {
    BeforeEach(func() {
        if !hasFfmpeg() {
            Skip("ffmpeg not found in PATH")
        }
    })
    It("merges and enqueues", func() { ... })
})
```
