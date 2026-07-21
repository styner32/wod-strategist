# Wod Strategist API

## Get started

## Configuration

The API and worker load `.env` via `internal/config` during startup and fail fast if required environment variables are missing.

- `cmd/server` requires `DATABASE_URL`, `REDIS_URL`, `GCS_BUCKET_NAME`, and `JWT_SIGNING_SECRET`. `PORT` is optional and defaults to `8080`.
- `cmd/worker` requires `DATABASE_URL`, `REDIS_URL`, `GCS_BUCKET_NAME`, and `GEMINI_API_KEY`.
- Runtime packages under `internal/` should return `error` values to the caller instead of calling `panic` or exiting the process. Startup failure handling belongs in `cmd/server` and `cmd/worker`.
- Application auth is JWT-only. There is no `API_SECRET` / `X-API-Key` gate. Gemini credentials (`GEMINI_API_KEY`) are separate.

## Database

```bash
brew install cloud-sql-proxy
```

```bash
cd infra && terraform output db_instance_connection_name
```

```bash
cloud-sql-proxy --port 5433 <db_instance_connection_name>
```

- Access to database from local machine

```bash
psql "postgres://DB_USER:DB_PASS@localhost:5433/wod_dev?sslmode=disable"
```

- Run migration remotely

```bash
gcloud run jobs execute $(MIGRATE_SERVICE_NAME) --region $(REGION)
```

## API Documentation (Swagger UI)

This project uses `swaggo` to automatically generate OpenAPI documentation from the Go source code comments. 

To view and interact with the Swagger docs:

1. Start the API server:
   ```bash
   go run cmd/server/main.go
   ```
2. Navigate to the generated Swagger UI in your browser:
   **[http://localhost:8088/swagger/index.html](http://localhost:8088/swagger/index.html)**
   *(Or replace `8088` with your custom `PORT`).*

To update the Swagger schema specs after modifying structs or handler comments, run:
```bash
swag init -g cmd/server/main.go
```

## Local Chunk Upload Replay

If you want to reuse an existing workout video instead of recording from the phone every time, you can replay the chunk upload flow locally from `api/`.

Prerequisites:

- API server and worker running locally
- `ffmpeg` and `ffprobe` installed
- Valid storage configuration in `api/.env` or your shell environment

Run:

```bash
cd api
make test-chunk-upload VIDEO=./tmp/test_wod.mp4 CHUNK_SECS=10
```

Optional variables:

- `MOVEMENTS=Burpee,Pull-up`
- `INJURIES=Left Knee`
- `WORKOUT_TYPE=rehab`
- `PROFILE_ID=1`
- `AUTO_MERGE=0` to upload/analyze chunks without merging
- `KEEP_CHUNKS=1` to keep the generated local chunk files for inspection

The replay script uses the real chunk flow:

1. Split one saved `.mp4` into ordered chunk files with `ffmpeg`
2. Call `POST /api/v1/upload-url` for each chunk
3. Upload each chunk to GCS with the signed URL
4. Call `POST /api/v1/chunk-complete` with `start_secs` and `end_secs`
5. Optionally call `POST /api/v1/merge-chunks`

This is not a fully offline path yet. The current API still uploads chunk files to the configured GCS bucket because the repo does not include a local storage emulator flow.
