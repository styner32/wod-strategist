# Backend Testing Memory

## Core testing philosophy
- Prefer real clients over interface-only fakes.
- Use `api/internal/testhelpers.MockTransport` for outbound HTTP tests.
- Reuse existing test harnesses and helpers before inventing new ones.

**Why no `fake*` structs:** IDE "Go to Definition" lands on the interface, not the fake, which slows debugging. Fakes also drift from real client behavior silently (URI parsing, retries, encoding). Real client + `MockTransport` tests the actual code path.

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

## Local test dependencies
- PostgreSQL default: `localhost:5432/wod_test`
- Redis default: `localhost:6379`, DB 15
- Overrides: `TEST_DATABASE_URL`, `TEST_REDIS_URL`

Run from `api/`:
- `make migrate-test-redo`

See also: [migrations.md](migrations.md).

## Layer 1 — Database
```go
BeforeEach(func() {
    dbConn, err = testhelpers.InitDB()
    Expect(err).NotTo(HaveOccurred())
    testhelpers.CleanupDB(dbConn) // panics if DB name doesn't end in "_test"
})
```

## Layer 2 — Gemini API
Wire a real `gemini.Client` to a `MockTransport` and register the 5-request chain (`upload-start → upload-finalize → poll → generateContent → deleteFile`):

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

transport.New(geminiBaseURL).Post("/upload/v1beta/files"). ...

Expect(w.HandleVideoAnalysisTask(ctx, task)).To(Succeed())
Expect(transport.Verify()).To(Succeed())
Expect(transport.Requests()).To(HaveLen(5))
```

**Two-pass variant:** when `chunk_analysis_results` rows exist, there is no `IndexVideo` call — the chain becomes `upload-start → upload-finalize → poll → analyzeSegment (Pro) → deleteFile`. Seed chunks in `BeforeEach`.

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
Uses Redis **DB 15** (app uses DB 5) so tests cannot collide with a running local server.

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
})
```

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
