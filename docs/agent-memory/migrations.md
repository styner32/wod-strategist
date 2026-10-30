# Database Migrations Memory

## Tool
- `golang-migrate` (CLI `migrate`).
- Migration files live in `api/internal/db/migrations/`.
- Naming: sequential `000NNN_description.{up,down}.sql` (use `migrate create -seq`).

## Commands (run from `api/`)

| Command | Purpose |
|---|---|
| `make migrate-create NAME=description` | Create a new up/down migration pair |
| `make migrate-up` | Apply all pending migrations against `DATABASE_URL` |
| `make migrate-down` | Roll back one step against `DATABASE_URL` |
| `make migrate-up-remote` | Execute the Cloud Run Jobs migrate service |
| `make migrate-test-up` | Apply all pending migrations against `TEST_DATABASE_URL` |
| `make migrate-test-redo` | Down one applied migration, then up; use for deliberate rollback/reapply validation |

## Authoring rules
- When adding or modifying a column on a model in `internal/db/`, always create a matching migration pair.
- Prefer `ALTER TABLE ... ADD COLUMN ... DEFAULT <value>` so existing rows are not broken.
- `down.sql` must use `DROP COLUMN IF EXISTS` for safe rollback. For `CREATE TABLE` migrations, `down.sql` must use `DROP TABLE IF EXISTS`.
- Never call `db.AutoMigrate()` — it bypasses version history and makes rollbacks impossible.

## Schema authority — migrations, not struct tags
The migration SQL is the **single source of truth** for schema constraints. Do not rely on gorm struct tags (`gorm:"not null;default:..."`, `gorm:"uniqueIndex"`) to enforce anything — they are advisory and silently diverge from the real schema.

- Put `NOT NULL`, `DEFAULT`, `UNIQUE`, foreign keys, and indexes in the `.up.sql` file.
- Keep gorm struct tags minimal: `primaryKey`, `json:"..."`, and pointer types for nullable columns. Treat the struct as a Go-side row shape, not a schema definition.
- Use `BIGSERIAL` / `BIGINT` for ID columns to match `uint` in Go without overflow.

## Migration vs. code order
- The migration must be authored before or alongside the Go model change — running code against a pre-migration schema will fail at startup.
- Before worker tests, run `make migrate-test-up` after adding a migration. Use `make migrate-test-redo` only to test rollback/reapply of the latest pair; it can roll back the previously applied migration if the new pair has not yet been applied.
- Archive lifecycle code requires migration `000061` before API/worker use: missing `automatic_processing_cancelled_at` or `workout_history_revision` fails ingress/worker/source-state queries with SQLSTATE `42703`. The current `/health` endpoint does not verify schema compatibility.
- Remote migration uses a separately built migration image. Confirm that image contains the required SQL and that the job completed; API/worker deployment alone does not run migrations.
- Applying a missed migration does not reset queued task retry counts or revive terminal/archived work. Inspect retained source and task/result state before explicit recovery; preserve an owner's archive decision and never auto-requeue those records.
