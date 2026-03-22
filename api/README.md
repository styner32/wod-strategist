# Wod Strategist API

## Get started

## Configuration

The API and worker load `.env` via `internal/config` during startup and fail fast if required environment variables are missing.

- `cmd/server` requires `DATABASE_URL`, `REDIS_URL`, `GCS_BUCKET_NAME`, and `API_SECRET`. `PORT` is optional and defaults to `8080`.
- `cmd/worker` requires `DATABASE_URL`, `REDIS_URL`, `GCS_BUCKET_NAME`, and `GEMINI_API_KEY`.
- Runtime packages under `internal/` should return `error` values to the caller instead of calling `panic` or exiting the process. Startup failure handling belongs in `cmd/server` and `cmd/worker`.

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
