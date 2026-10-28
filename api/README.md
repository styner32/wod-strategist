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

## Upload and live replay QA

Use the [web app](../web/README.md) for authenticated saved-video upload and the mobile app for recording/chunk-finalization QA. Both use real configured GCS storage; no local storage emulator flow is provided.

For retained benchmark video/H10 replays through authenticated APIs, use [`cmd/live-replay`](cmd/live-replay) via `make live-test-prepare`, `make live-test-list`, and `make live-test` (`WOD=<name>` or interactive selection) from the repository root. Live analysis incurs model costs and retains the resulting profile 10 session. See the [live replay guide](../docs/agent-memory/live-replay.md) for fixture setup, credentials, and result checks.

## Tests and migrations

```bash
make migrate-test-up
make test
```

`make migrate-test-up` applies every pending test migration. `make migrate-test-redo` rolls back one applied migration and reapplies pending migrations; use it only when deliberately testing the latest pair's rollback. Do not run backend test invocations concurrently. See [backend-testing.md](../docs/agent-memory/backend-testing.md).
