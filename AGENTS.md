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
