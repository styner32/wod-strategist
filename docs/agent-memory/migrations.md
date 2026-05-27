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
| `make migrate-test-redo` | Down-1 then up against `TEST_DATABASE_URL` (used by worker tests) |

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
- For worker tests, `make migrate-test-redo` must be run after adding a migration, otherwise `wod_test` will be out of date.
