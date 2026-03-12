# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

WOD Strategist is an AI-powered CrossFit coaching app providing real-time pose estimation, rep counting, and form correction. The app uses on-device Vision AI for real-time feedback and a Go backend for video analysis with Gemini AI.

## Tech Stack

**Frontend (React Native/Expo):**

- React Native 0.81+ with Expo SDK 54+ (Prebuild/CNG - NOT Expo Go)
- TypeScript strict mode
- Expo Router for file-based navigation
- Vision Camera v4+ with TFLite for on-device pose detection
- Worklets Core for frame processor threading (CRITICAL)
- React Native Skia for 60fps skeleton overlays
- Zustand for global state

**Backend (Go):**

- Go 1.24+ with Gin (HTTP), GORM (ORM), Asynq (async tasks)
- PostgreSQL + Redis
- Google Generative AI (Gemini) for video analysis
- Google Cloud Storage for video uploads

## Common Commands

### Frontend

```bash
npm install                    # Install dependencies
npx expo start                 # Start Expo dev server
npx expo prebuild --clean      # Generate native projects
npx expo run:ios --device      # Build and run on iOS device
npx expo run:android           # Build and run on Android
npm run lint                   # Run ESLint
```

### Backend (from /api directory)

```bash
make run                       # Run API server (port 8080)
make run-worker                # Run async worker
make migrate-up                # Run database migrations
make migrate-down              # Rollback 1 migration
make migrate-create NAME=desc  # Create new migration
make test-upload               # Test video upload locally
make deploy-api                # Deploy API to Cloud Run
make deploy-worker             # Deploy worker to Cloud Run
```

## Architecture

### Frontend (Feature-Sliced Design)

- `app/` - Expo Router pages (file-based routing)
- `features/` - Domain logic separated from UI
  - `ai-coach/` - Vision AI inference and frame processors
  - `wod/` - WOD management and API client
  - `health/` - HealthKit and BLE integrations
- `components/` - Shared UI components
- `assets/models/` - TFLite model files (MoveNet Thunder)

### Backend

- `cmd/server/` - API server entry point
- `cmd/worker/` - Async worker entry point
- `internal/server/` - HTTP routes and handlers
- `internal/db/` - GORM models and migrations
- `internal/worker/` - Asynq task handlers
- `internal/gemini/` - AI model integration
- `internal/storage/` - GCS client

### Data Flow (Video Analysis)

1. App records workout video
2. Backend provides signed GCS URL
3. App uploads directly to GCS
4. Backend enqueues async analysis task (Asynq/Redis)
5. Worker processes video with Gemini AI
6. Results stored in PostgreSQL
7. App polls for results

## Critical Configuration

### Babel Plugin Order (babel.config.js)

Worklets plugin MUST come before Reanimated:

```js
plugins: [
  "react-native-worklets-core/plugin", // First
  "react-native-reanimated/plugin", // Last
];
```

### Frame Processors

Code running on Vision Camera thread must use `'worklet'` pragma. Use `useSharedValue()` for values crossing Worklet/JS boundaries.

### Environment Variables

- Frontend: `EXPO_PUBLIC_*` prefix for client-accessible vars
- Backend: Configure via `api/.env` (DATABASE_URL, REDIS_URL, GCS_BUCKET_NAME, API_SECRET)

## Testing

### Backend (Ginkgo/Gomega)

Tests in `api/internal/server/server_test.go` use BDD-style syntax:

```bash
cd api && go test ./...
```

Note: Redis must be running for full test suite.

## API Endpoints

- `POST /api/v1/upload-url` - Get signed URL for GCS upload
- `POST /api/v1/upload-complete` - Notify upload complete, start analysis
- `GET /api/v1/analysis/:session_id` - Fetch analysis results
- `GET /api/v1/history` - Last 20 analyses
- `GET /api/v1/movements` - Valid CrossFit movements
- `GET /api/v1/injuries` - Valid injuries
