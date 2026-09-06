# WOD Strategist

React Native workout recording, Go video analysis, and a separate React web review app. See [API setup](api/README.md), [web setup](web/README.md), and the [documentation review](docs/documentation-review.md).

## Get started

1. Install dependencies

   ```bash
   npm install
   ```

2. Build and start the native app (camera/BLE dependencies require a native build)

   ```bash
   npm run ios
   ```

   ```bash
   npm run android
   ```

   ```bash
   npm start
   ```

   Use `npm start` for subsequent bundler sessions after installing the native build. For a physical device, set `EXPO_PUBLIC_API_URL` to a reachable API address including `/api/v1`.

3. Test the legacy multipart upload (API/worker and storage must be configured; use a valid JWT and an owned profile)

   ```bash
   curl -X POST http://localhost:8088/api/v1/upload \
     -H "Authorization: Bearer <jwt>" \
     -F "session_id=WOD-20260906-01JQXYZ3K4M5N6P7Q8R9ABCDEF" \
     -F "profile_id=<owned_profile_id>" \
     -F "file=@./tmp/wod_1.MP4"
   ```

Note: `scripts/test-upload.js` / `scripts/test-chunk-upload.js` still send a legacy `X-API-Key` and do not obtain a JWT, so they cannot call protected routes against the current API until they are updated or retired. Use the web app for authenticated upload, analysis review, highlights, and playback; use the mobile app to exercise recording and stop/merge behavior. The local API examples use `PORT=8088`; the server default without that override is `8080`.

## Cloud SQL (Dev)

Connect to the dev PostgreSQL instance via [Cloud SQL Auth Proxy](https://cloud.google.com/sql/docs/postgres/connect-auth-proxy):

```bash
cloud-sql-proxy gen-lang-client-0826771503:asia-northeast3:wod-strategist-db-dev --port=15432
```

Once the proxy is running, connect with any Postgres client:

```bash
psql "postgresql://<DB_USER>:<DB_PASS>@localhost:15432/wod-strategist_dev?sslmode=disable"
```

Or use the proxy for the admin CLI from the repository root:

```bash
make -C api run-admin DATABASE_URL="postgresql://<DB_USER>:<DB_PASS>@localhost:15432/wod-strategist_dev?sslmode=disable" CMD="reparse-highlights --apply --session-id=<SESSION_ID>"
```

## API Typings & Schema

The React Native app uses interface typings generated from backend Swagger definitions. Some request/response types are handwritten in `features/wod/api.ts`, and the separate web app keeps types under `web/src/api/`; review those callers as well.

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
The compiler checks usages of regenerated types. It cannot prove alignment for handwritten types, incomplete Swagger declarations, or runtime JSON; inspect affected callers and run the relevant contract tests.
