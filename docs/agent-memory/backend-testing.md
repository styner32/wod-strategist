# Backend Testing Memory

## Core testing philosophy
- Prefer real clients over interface-only fakes.
- Use `api/internal/testhelpers.MockTransport` for outbound HTTP tests.
- Reuse existing test harnesses and helpers before inventing new ones.

## Worker integration testing
Worker handler tests in `internal/worker/*_test.go` follow a real-client strategy:
- real PostgreSQL test DB
- real Redis/asynq queue
- real clients backed by `MockTransport` for external HTTP services

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

## Database pattern
Use:
- `testhelpers.InitDB()`
- `testhelpers.CleanupDB(db)` in `BeforeEach`

## Gemini API pattern
Use:
- `testhelpers.NewMockTransport()`
- real `gemini.Client` via `gemini.NewClientWithOptions`

Typical sequence:
- upload-start
- upload-finalize
- poll
- analyze / generateContent
- deleteFile

Always verify:
- `transport.Verify()`
- expected request count when relevant

## GCS testing pattern
Use:
- `testhelpers.NewStorageClient(bucketName, transport)`
- `MockGCSDownload`
- `MockGCSDownloadWithBody`
- `MockGCSListObjects`
- `MockGCSUpload`

## Queue testing pattern
Use:
- `testhelpers.NewQueueClient()`
- `testhelpers.NewQueueInspector()`
- `testhelpers.CleanupQueue(inspector)`

## FFmpeg-dependent tests
Wrap ffmpeg-dependent cases in a guarded context.
Skip when ffmpeg is unavailable.
Use `createTinyMP4(GinkgoT())` when a real mp4 is required.

## Shared helpers
Common worker test helpers live in:
- `worker_test_helpers_test.go`