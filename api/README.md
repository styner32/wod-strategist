# WOD Strategist API

All commands below run from `api/` unless stated otherwise.

## Get started

Configure the variables below, start PostgreSQL and Redis, and provide GCS credentials. Install `ffmpeg`/`ffprobe` for worker media processing. Apply pending local migrations with `make migrate-up`. In separate terminals run:

```bash
PORT=8088 make run
```

```bash
make run-worker
```

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
terraform -chdir=../infra output db_instance_connection_name
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
make migrate-up-remote
```

## API Documentation (Swagger UI)

This project uses `swaggo` to automatically generate OpenAPI documentation from the Go source code comments. 

To view and interact with the Swagger docs:

1. Start the API server:
   ```bash
   PORT=8088 go run cmd/server/main.go
   ```
2. Navigate to the generated Swagger UI in your browser:
   **[http://localhost:8088/swagger/index.html](http://localhost:8088/swagger/index.html)**
   *(The examples set `PORT=8088` to match the mobile default and Vite proxy. Without an override, the server defaults to `8080`.)*

To update the Swagger schema specs after modifying structs or handler comments, run:
```bash
swag init -g cmd/server/main.go
```

## Legacy upload replay scripts

`make test-upload` and `make test-chunk-upload` invoke the root `scripts/test-*.js` scripts and load the root `.env`. They currently send `X-API-Key` without a JWT and cannot call the protected API.

The chunk script also hardcodes its input path, session ID, workout type, chunk duration, and merge options. `VIDEO=...`, `CHUNK_SECS=...`, and the previously documented optional Make variables do not configure those constants. Treat this script as legacy until its authentication and arguments are updated.

Use the [web app](../web/README.md) for authenticated saved-video upload and the mobile app for recording/chunk-finalization QA. Both use real configured GCS storage; no local storage emulator flow is provided.

## Tests and migrations

```bash
make migrate-test-up
make test
```

`make migrate-test-up` applies every pending test migration. `make migrate-test-redo` rolls back one applied migration and reapplies pending migrations; use it only when deliberately testing the latest pair's rollback. Do not run backend test invocations concurrently. See [backend-testing.md](../docs/agent-memory/backend-testing.md).
