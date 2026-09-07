# WOD Strategist Web

React/TypeScript review app for authenticated video upload, workout history, session playback, feedback, re-analysis, and cost inspection. This app is separate from the root Expo web target.

## Local development

Start the API and worker using [api/README.md](../api/README.md). The Vite proxy targets `http://localhost:8088`, so set `PORT=8088` on the API.

From `web/`:

```bash
npm install
npm run dev
```

Open [http://localhost:5173](http://localhost:5173). Requests to `/api/v1` are proxied to the API; authentication uses the `jwt` httpOnly cookie. An existing account is required because self-service signup is disabled. For local HTTP development, configure the API with `COOKIE_SECURE=false`; retain secure cookies in HTTPS environments.

Optional API feature flags (both default to false):

- `ENABLE_CHUNK_REANALYSIS=true`
- `ENABLE_SESSION_REANALYSIS=true`

Re-analysis creates a candidate. Explicit whole-session apply changes the production result; feedback alone does not. See [video-analysis.md](../docs/agent-memory/video-analysis.md).

## Validation

```bash
npm run build
npm run lint
```

There is no web `test` script. `npm run preview` serves the built frontend; it is not a production API deployment.

## Known behavior to review

The upload page currently defaults to `compare` and labels it as two parallel pipelines, while the video worker routes it only through two-pass analysis. This remains OBS-05 in the [improvement plan](../docs/video-analysis-improvement/03-phase-2-observability-and-evaluation.md). Use the actual worker path and recorded model IDs when interpreting output or cost.
