# Wod Strategist

## Get started

1. Install dependencies

   ```bash
   npm install
   ```

2. Start the app

   ```bash
   npx expo start
   ```

   ```bash
   npx expo prebuild --clean
   ```

   ```bash
   npx expo run:ios --device
   ```

3. Test uploading video to API (requires a valid JWT after login)

   ```bash
   curl -X POST http://localhost:8088/api/v1/upload \
     -H "Authorization: Bearer <jwt>" \
     -F "session_id=workout-session-002" \
     -F "file=@./tmp/wod_1.MP4"
   ```

Note: `scripts/test-upload.js` / `scripts/test-chunk-upload.js` still send a legacy `X-API-Key` and do not obtain a JWT, so they cannot call protected routes against the current API until they are updated or retired. Session upload, merge, highlight, and playback QA is covered by the web app under `web/`.

## Cloud SQL (Dev)

Connect to the dev PostgreSQL instance via [Cloud SQL Auth Proxy](https://cloud.google.com/sql/docs/postgres/connect-auth-proxy):

```bash
cloud-sql-proxy gen-lang-client-0826771503:asia-northeast3:wod-strategist-db-dev --port=15432
```

Once the proxy is running, connect with any Postgres client:

```bash
psql "postgresql://<DB_USER>:<DB_PASS>@localhost:15432/wod-strategist_dev?sslmode=disable"
```

Or use the proxy for admin cli:

```bash
make run-admin DATABASE_URL="postgresql://<DB_USER>:<DB_PASS>@localhost:15432/wod-strategist_dev?sslmode=disable" CMD="reparse-highlights --apply --session-id=<SESSION_ID>"
```

## API Typings & Schema

The React Native app shares interface typings generated directly from the Go backend to ensure the API contracts remain strictly aligned. 

### Syncing Changes
Whenever you update a request or response struct in the Go API (e.g. `api/internal/controllers/dto.go`), update the Swagger comments on your Gin handlers and run the following command from the root directory to immediately sync the changes to your frontend:

```bash
npm run sync-api
```
*(This automatically runs `swag init` in the backend and regenerates `features/wod/schema.d.ts` for the frontend)*

### Verifying Type Mismatches
After regenerating the API schema, you can verify if your React Native code requires updating by running the TypeScript typechecker:

```bash
npm run typecheck
```
If your frontend code tries to use a response property that was renamed or deleted in your API structs, the compiler will catch it instantly.
