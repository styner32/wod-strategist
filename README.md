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

3. Test uploading video to API

   ```bash
   curl -X POST http://localhost:8088/api/v1/upload -F "session_id=workout-session-002" -F "file=@./tmp/wod_1.MP4"
   ```

## Local Video QA Workbench

Run the API locally, then start the browser QA page from the repo root:

```bash
npm run qa:video
```

Open [http://localhost:3000/video.html](http://localhost:3000/video.html) and enter:

- `API Base URL`: usually `http://localhost:8088/api/v1`
- `API Key`: your local `API_SECRET`
- `Profile ID`: an existing profile ID used for chunk upload, merge, and highlight actions

The page can upload chunk files, trigger `merge-chunks`, trigger `generate-highlight`, inspect session assets, and cache playable videos in the browser for side-by-side comparison.

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
