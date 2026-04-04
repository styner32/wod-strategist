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

### Testing philosophy: prefer real clients over interface fakes

Do **not** create `fake*` structs that implement an interface solely for testing (e.g. `fakeStorage`, `fakeQueue`). Instead, use real clients backed by `testhelpers.MockTransport` to intercept HTTP at the transport layer.

**Why:**
1. **IDE navigation breaks** — "Go to Definition" on a fake's method lands on the *interface* signature, not the fake implementation. This makes tests harder to navigate and debug.
2. **Behavior drift is invisible** — if the real client changes how it parses URIs, retries, or encodes requests, a fake won't catch the regression because it's a completely separate implementation.

**Pattern:** wire real clients with `&http.Client{Transport: mockTransport}` and register expected HTTP calls. This tests the actual code path end-to-end while keeping tests deterministic.

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

The two-pass test mock chain is: `upload-start → upload-finalize → poll → analyzeSegment (Pro) → deleteFile`. When chunks exist in the DB, there is no `IndexVideo` call — segments come from `chunk_analysis_results`.

Seed chunk data in `BeforeEach` for two-pass tests:

```go
seedChunks := func(sessionID string) {
    start0 := 0.0; end0 := 45.0
    dbConn.Create(&db.ChunkAnalysisResult{
        SessionID: sessionID, Status: "COMPLETED",
        ExerciseType: "Snatch",
        StartSecs: &start0, EndSecs: &end0,
        Output: "좋은 스내치 자세입니다",
    })
}
```

**Important:** Defer cleanup (`DeleteFile`) is registered **before** any early-return check (e.g. empty analysis). This prevents Gemini file leaks and is what the `MockTransport.Verify()` assertion catches.

### Layer 3 — GCS Storage: real client + MockTransport

Use `testhelpers.NewStorageClient(bucketName, transport)` for all tests. Returns a real `storage.Client` backed by `MockTransport`. Register GCS expectations with the `MockGCS*` helpers:
  - `testhelpers.MockGCSDownload(transport, "gs://bucket/object")` — 1-byte sentinel response
  - `testhelpers.MockGCSDownloadWithBody(transport, "gs://bucket/object", body)` — custom body (use for ffmpeg tests that need a real mp4)
  - `testhelpers.MockGCSListObjects(transport, bucket, prefix, objects)` — list objects response
  - `testhelpers.MockGCSUpload(transport, bucket, objectName)` — upload response

```go
// Standard pattern (most tests):
storageTransport = testhelpers.NewMockTransport()
storageClient, _ := testhelpers.NewStorageClient("my-bucket", storageTransport)
w.StorageClient = storageClient

// Register per-test expectations:
testhelpers.MockGCSDownload(storageTransport, "gs://my-bucket/videos/session/chunk.mp4")
testhelpers.MockGCSListObjects(storageTransport, "my-bucket", "videos/session", []string{"chunk_001.mp4"})

// When ffmpeg needs a real mp4 file on disk:
mp4Bytes, _ := os.ReadFile(createTinyMP4(GinkgoT()))
testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://my-bucket/chunk.mp4", mp4Bytes)
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

All shared utilities live in one file:

| Symbol | Purpose |
|---|---|
| `makeVideoAnalysisTask(p)` | Marshals a payload into an `*asynq.Task` |
| `hasFfmpeg()` | Returns true if ffmpeg is in PATH |
| `createTinyMP4(t)` | Creates a 1-second black mp4 via ffmpeg; skips test if unavailable |

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

## Video Analysis Architecture (Two-Pass)

The merged-video analysis uses a **two-pass** architecture to avoid temporal hallucination.

### Pipeline overview

```
Chunk Recording (mobile) → Chunk Analysis (per-chunk, real-time) → Merge Chunks → Two-Pass Analysis (merged video)
```

### Pass 1 — Segment Indexing (chunk-based, preferred)

Segments are built from `chunk_analysis_results` rows in the DB. Each chunk has **app-recorded** `start_secs` and `end_secs` — zero hallucination risk. Adjacent chunks within 10 seconds are merged via `mergeAdjacentSegments()` to reduce Pro model calls.

If no chunks exist (e.g. direct video upload without chunking), the handler falls back to a model-based index call (`IndexVideo`). This fallback includes anti-hallucination guards:
- Video duration injected as a hard constraint in the prompt
- `MediaResolution: High` forces actual frame processing
- `filterSegments()` discards any timestamps beyond the video's real length

### Pass 2 — Deep Analysis (per-segment, Pro model)

Each segment is analyzed independently with `AnalyzeSegment()` using `VideoMetadata` (`StartOffset`, `EndOffset`) to constrain the model to specific time ranges in the merged video. This prevents the model from confusing timestamps across a long video.

### Gemini Files API lifecycle

```
UploadVideo → [file ACTIVE] → IndexVideo / AnalyzeSegment (reuse fileURI) → DeleteFile
```

- The video is uploaded **once** via `UploadVideo()`. The returned `fileURI` is reused across all passes.
- `UploadResult` includes `VideoDuration` captured from `File.VideoMetadata` during ACTIVE polling. Note: `File.VideoMetadata` is `map[string]any` (not a struct), so duration is extracted via `file.VideoMetadata["videoDuration"].(string)` and parsed with `time.ParseDuration`.
- **Do NOT use `CachedContent` API** with `VideoMetadata` — they are incompatible. The Files API upload serves as the cache layer.

### File ownership and cleanup

- If **no injuries**: the video analysis handler defers `DeleteFile` and cleans up after all segments are analyzed.
- If **injuries exist**: the file URI is passed to the `InjuryAnalysisPayload` (`GeminiFileURI`, `GeminiFileName`, `GeminiMIMEType`). The injury handler becomes the last consumer and calls `DeleteFile` after its analysis.

### Key types in `video_analysis.go`

| Type | Purpose |
|---|---|
| `Segment` | `{Start, End, Type, Description}` — parsed from chunks or model output |
| `buildSegmentsFromChunks(sessionID)` | Queries `chunk_analysis_results` → `[]Segment` |
| `buildIndexPrompt(p, videoDuration)` | Builds Flash prompt with profile, movements, and duration constraint |
| `buildSegmentAnalysisPrompt(p, seg)` | Builds per-segment Pro prompt |
| `parseSegments(text)` | Extracts `[]Segment` from JSON in code fences |
| `filterSegments(segs, duration)` | Drops segments with timestamps beyond video length |
| `mergeAdjacentSegments(segs)` | Merges chunks ≤10s apart into larger segments |
| `convertToSeconds(input)` | Parses "MM:SS" or "Ns" → `time.Duration` |
| `formatDuration(d)` | Formats `time.Duration` as "MM:SS" |

### `chunk_analysis_results` schema

| Column | Type | Purpose |
|---|---|---|
| `session_id` | TEXT | Links chunks to a session |
| `file_path` | TEXT | GCS URI of the individual chunk video |
| `exercise_type` | TEXT | Detected movement (e.g. "Snatch", "Pull-up") |
| `start_secs` | REAL | Start offset in merged timeline (app-recorded) |
| `end_secs` | REAL | End offset in merged timeline (app-recorded) |
| `output` | TEXT | Short coaching feedback from chunk analysis |
| `status` | TEXT | PENDING / COMPLETED / FAILED |

### Anti-hallucination patterns

1. **Prefer chunk data over model indexing** — app-recorded timestamps are ground truth.
2. **Inject video duration** — tell the model "this video is exactly MM:SS long" and "do NOT generate timestamps beyond this."
3. **Require visual evidence** — prompt says "describe the equipment, stance, and movement you see" instead of "describe the exercise."
4. **Post-filter** — `filterSegments()` removes any segments exceeding video duration (5s tolerance).
5. **Profile context** — include height/weight/gender so the model can identify the correct person in a multi-person gym.

## Android Recording Performance

Android devices crash with OOM when camera recording, TFLite inference, and Skia rendering run concurrently at full FPS. Optimizations are controlled by **user-configurable toggles** in the setup page — do NOT hardcode `Platform.OS === 'android'` checks.

### Configurable flags (setup.tsx → visionTestPage.tsx via route params)

| Flag | Param | Android default | iOS default |
|---|---|---|---|
| Skeleton Overlay | `showSkeleton` | OFF | ON |
| Low FPS (24fps) | `lowFps` | ON | OFF |
| Force 720p | `force720p` | ON | OFF |
| Skip Chunk Compression | `skipCompression` | ON | OFF |

### Non-configurable (hardcoded per platform)

| Optimization | File | Android | iOS |
|---|---|---|---|
| Inference FPS throttle | `usePoseDetection.ts` | 5 fps | 15 fps |
| `android:largeHeap` | `AndroidManifest.xml` | `true` | N/A |

### Pattern for reading flags in visionTestPage.tsx

```typescript
const flag = flagParam !== undefined
  ? flagParam === 'true'
  : IS_ANDROID; // or !IS_ANDROID depending on semantics
```

### Rules for AI assistants

- **Never hardcode `IS_ANDROID`** for performance gating in `visionTestPage.tsx` — always use the configurable param pattern above.
- The `usePoseDetection.ts` throttle uses `runAtTargetFps()` from `react-native-vision-camera` — this is the single most impactful fix for Android OOM. Do not remove it.
- The recording dashboard shows **OPT FLAGS** during recording for debugging — keep this in sync when adding new flags.

