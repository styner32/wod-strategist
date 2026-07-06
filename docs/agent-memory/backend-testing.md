# Backend Testing Memory

## Core testing philosophy
- Prefer real clients over interface-only fakes.
- Use `api/internal/testhelpers.MockTransport` for outbound HTTP tests.
- Reuse existing test harnesses and helpers before inventing new ones.
- Use factories in `testhelpers/factory.go` for entity setup
  (`CreateUser`, `CreateProfile`, ...). Don't inline `dbConn.Create(&db.Foo{...})`
  in tests — add a factory and call it.

**Why no `fake*` structs:** IDE "Go to Definition" lands on the interface, not the fake, which slows debugging. Fakes also drift from real client behavior silently (URI parsing, retries, encoding). Real client + `MockTransport` tests the actual code path.

**One exception:** faking a *stdlib* interface (e.g. `multipart.File`) is fine; faking internal clients/wrappers is not.

## Test tiers (what goes where)
- **Pure functions** (validators, sanitizers, parsers): plain table-driven
  unit tests, no DB/Redis/transport. See `handlers_test.go`. Mock-free unit
  tests of pure logic are encouraged — the "no mocks" rule bans *layer
  isolation via fakes*, not fast tests of pure code.
- **Everything with I/O**: sociable tests through the real entry point
  (router `ServeHTTP` / worker handler) per the 4-layer strategy below.

## Local test dependencies & setup
- PostgreSQL default: `localhost:5432/wod_test`
- Redis default: `localhost:6379`, DB 15
- Overrides: `TEST_DATABASE_URL`, `TEST_REDIS_URL`

Run from `api/`:
- `make migrate-test-up` — builds the test schema from scratch (fresh clone).
- `make migrate-test-redo` — re-runs only the **latest** migration; use it
  when iterating on a new migration, not for bootstrap.

See also: [migrations.md](migrations.md).

## Parallelism & isolation (load-bearing!)
- `make test` uses `go test -p 1` because ALL packages share one
  `wod_test` DB and Redis DB 15, and `CleanupDB` truncates every table.
- Never run two test invocations concurrently (IDE runners, second
  worktree) — they will corrupt each other's state.
- DB connections are **suite-scoped**: open in `BeforeSuite`, clean with
  `CleanupDB` in `BeforeEach`. Do not call `InitDB()` per spec — each call
  opens a GORM pool that is never closed. (Some worker suites still do
  this; migrate them opportunistically when touching those files.)

## Controller (route) tests

Integration tests for HTTP routes live in
`internal/controllers/handlers_integration_test.go`.

- **One `Describe` block per route**, named after the route
  (`Describe("POST /api/v1/sessions")`, `Describe("GET /api/v1/analysis/:session_id")`).
  Do not group multiple routes under an omnibus `Describe` like
  "Controller handlers" or "session_id handlers" — those are legacy.
- Setup inside `BeforeEach` uses factories from `testhelpers/`. Avoid
  inline `dbConn.Create(...)` for entities a factory exists for.
- Use the real `controllers.Controller` wired to the test DB
  (`config.DB = dbConn`) — no repository fakes. Requests go through
  `server.SetupRouter` + `router.ServeHTTP`, so middleware (auth,
  ownership) is exercised too.
- Never leave `FDescribe` / `FIt` / `FContext` in committed code; they
  cause Ginkgo to silently skip every non-focused spec in the file, and
  `go test` does **not** fail on focused specs — check the diff before
  committing.

## Worker integration testing
Worker handler tests in `internal/worker/*_test.go` follow a **4-layer real-client** strategy:
1. **Database** — real PostgreSQL test DB
2. **Gemini API** — real client + `MockTransport`
3. **GCS storage** — real client + `MockTransport`
4. **Queue** — real asynq client (Redis DB 15) + `QueueInspector`

Do not introduce:
- SQL mocking
- in-memory queue substitutes
- ad-hoc HTTP servers unless existing helpers cannot cover the case

## Layer 1 — Database
Suite-scoped connection, per-spec cleanup:

```go
var dbConn *gorm.DB

BeforeSuite(func() {
    var err error
    dbConn, err = testhelpers.InitDB()
    Expect(err).NotTo(HaveOccurred())
})

BeforeEach(func() {
    testhelpers.CleanupDB(dbConn) // panics if DB name doesn't end in "_test"
})
```

## Layer 2 — Gemini API
Wire a real `gemini.Client` to a `MockTransport` and register the 5 expected
requests (`upload-start`, `upload-finalize`, `poll`, `generateContent`,
`deleteFile` — matching is unordered, see
[Assertions on outbound HTTP](#assertions-on-outbound-http)):

```go
transport := testhelpers.NewMockTransport()
realClient, _ := gemini.NewClientWithOptions(ctx, zap.NewNop(), gemini.Options{
    APIKey:       "test-api-key",
    BaseURL:      "https://generativelanguage.googleapis.com",
    HTTPClient:   &http.Client{Transport: transport},
    PollInterval: time.Millisecond,
    Sleep:        func(time.Duration) {}, // no-op sleep: deterministic polling
})
w.GeminiClient = realClient

transport.New(geminiBaseURL).Post("/upload/v1beta/files"). ...

// Match on the request body where the wire contract matters — e.g. the
// generateContent call must reference the URI returned by the upload chain:
transport.New(geminiBaseURL).
    Post("/v1beta/models/"+gemini.ModelPro31Preview+":generateContent").
    MatchBodyContains(`"fileUri":"`+geminiBaseURL+`/files/mock-file"`).
    Reply(http.StatusOK).JSON(...)

Expect(w.HandleVideoAnalysisTask(ctx, task)).To(Succeed())
Expect(transport.Verify()).To(Succeed())

// Assert on captured bodies for payload-specific content (prompt text etc.):
var genBody string
for _, r := range transport.Requests() {
    if strings.Contains(r.URL, ":generateContent") {
        genBody = string(r.Body)
    }
}
Expect(genBody).To(ContainSubstring("## 운동 종목: Burpee, Pull-up"))
```

**Two-pass variant:** when `chunk_analysis_results` rows exist, there is no `IndexVideo` call — the flow becomes `upload-start → upload-finalize → poll → analyzeSegment (Pro) → deleteFile`. Seed chunks in `BeforeEach` via a factory (add `CreateChunkAnalysisResult` if it doesn't exist yet — don't inline `dbConn.Create`).

**Important:** register the deferred `DeleteFile` expectation **before** any early-return path (e.g. empty analysis). This prevents Gemini file leaks and is what `transport.Verify()` catches.

## Layer 3 — GCS storage
```go
storageTransport = testhelpers.NewMockTransport()
storageClient, _ := testhelpers.NewStorageClient("my-bucket", storageTransport)
w.StorageClient = storageClient

testhelpers.MockGCSDownload(storageTransport, "gs://my-bucket/videos/session/chunk.mp4")
testhelpers.MockGCSListObjects(storageTransport, "my-bucket", "videos/session", []string{"chunk_001.mp4"})

// When ffmpeg needs a real mp4 file on disk:
mp4Bytes, _ := os.ReadFile(createTinyMP4(GinkgoT()))
testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://my-bucket/chunk.mp4", mp4Bytes)
```

Helpers:
- `MockGCSDownload` — 1-byte sentinel response
- `MockGCSDownloadWithBody` — custom body (for ffmpeg tests)
- `MockGCSListObjects` — list-objects response
- `MockGCSUpload` — upload response

## Layer 4 — Queue (asynq / Redis)
Uses Redis **DB 15** (app uses DB 5) so tests don't collide with the running
local app. Concurrent test runs still collide — see
[Parallelism & isolation](#parallelism--isolation-load-bearing).

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

    // Always unmarshal with the CONSUMER's payload struct — a type-only
    // assertion misses producer/consumer contract breaks.
    var injPayload InjuryAnalysisPayload
    Expect(json.Unmarshal(pending[0].Payload, &injPayload)).To(Succeed())
    Expect(injPayload.SessionID).To(Equal("sess-injury-001"))
})
```

## Assertions on outbound HTTP
- Rely on `transport.Verify()` (every registered expectation consumed) +
  the transport's built-in failure on unexpected requests.
- Do NOT assert exact request counts (`Requests()` + `HaveLen(n)`) —
  retries and extra polls are behavior-neutral and must not break tests.
- Expectation matching is **unordered**; ordering is not verified.
- Verify request bodies where the wire contract matters: use
  `MatchBodyContains(substr)` on the expectation, or assert on
  `transport.Requests()[i].Body` after the call. Substrings must be
  single-line (JSON escapes newlines/quotes).

## Shared helpers
All shared worker test utilities live in `worker_test_helpers_test.go`:

| Symbol | Purpose |
|---|---|
| `makeVideoAnalysisTask(p)` | Marshals a payload into `*asynq.Task` |
| `hasFfmpeg()` | Returns true if `ffmpeg` is in `PATH` |
| `createTinyMP4(t)` | Creates a 1-second black mp4 via ffmpeg; skips the test if unavailable |

## FFmpeg-dependent tests
Guard ffmpeg-dependent `It` blocks behind a `Context` with a skip:

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

Skips are a false green: a machine without ffmpeg silently never runs these
specs. Once a CI pipeline exists, `Fail` instead of `Skip` when `CI` is set.

## Known blind spots (accepted trade-offs)
- **No CI pipeline yet** — the suite runs only on developer machines.
  Nothing enforces a pinned PostgreSQL version (so "catch DB upgrade
  breaks" is aspirational), `--fail-on-focused`, or `-race`.
- Canned responses can drift from the real Gemini/GCS APIs; the real
  client only protects the decode path. Proposed mitigation (not yet
  built): nightly live smoke test against the real Gemini API.
- Transport-level failures (timeouts, resets) are untestable until
  `MockTransport` grows `ReplyError` — retry paths are NOT covered.
- Queue tests cover the producer + direct handler calls; asynq server
  wiring (mux registration, retry config) is not exercised.
- GCS sentinel bodies skip content-handling code outside ffmpeg tests.
