# Wod Strategist API

## Get started

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
