# Repository Instructions

## Working principles
- Plan explicitly before modifying code.
- Prefer small, reviewable diffs.
- Follow existing project patterns before introducing new abstractions.
- Avoid unrelated refactors.
- Do not rename unrelated symbols or reformat unrelated files.
- Reuse existing helpers, clients, and package boundaries.

## Output requirements
- When running `gcloud` commands (via MCP or otherwise), always show the exact command in the response.
- When finishing a task, report:
  - files changed
  - tests added or updated
  - commands run
  - risks or follow-up work

## Session ID format
- **Current:** `WOD-YYYYMMDD-{ULID}` (e.g., `WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF`)
- **Generation:** Client-side using the `ulid` npm package.
- **Backward compatibility:** Must always handle the old format (`P{id}-WOD-YYYY-MM-DD-HH-MM`).

## GCS storage layout
- **Path pattern:** `videos/{profileId}/{sessionId}/{filename}`
- **API `profile_id` requirements:**
  - Must be included in JSON body for `POST /upload-url`, `POST /chunk-complete`, `POST /merge-chunks`.
  - Must be included as a query parameter for `GET /video-download/:session_id`.

## Scope control
Never do these unless explicitly requested:
- broad refactors
- changing schema or migrations outside the requested task
- changing dependency versions
- modifying CI/linter/tooling config
- adding new generic frameworks, helper layers, or policy engines

## 🚫 CRITICAL CONSTRAINTS
- **NEVER** embed the `profile_id` directly inside the Session ID string — use it only as a GCS path prefix.
- **NEVER** place new session file types in a separate top-level prefix — always under `videos/{pid}/{sid}/`.

## Scoped rules
- [api/AGENTS.md](api/AGENTS.md) — Backend Go rules (testing, migrations, error handling, video analysis)
- [app/AGENTS.md](app/AGENTS.md) — Frontend React Native rules (Android perf, BLE, pose detection, i18n)

## Project memory docs
When introducing a new integration, architectural pattern, non-obvious gotcha, or platform-specific workaround, create or update a memory doc in [docs/agent-memory/](docs/agent-memory/). Each doc should be concise and rule-oriented — include exact param names, default values, schema columns, and constraints that a future agent would need to make correct edits.

Consult these when working on the relevant domain:
- [docs/agent-memory/backend-testing.md](docs/agent-memory/backend-testing.md) — Worker integration test patterns and helpers
- [docs/agent-memory/migrations.md](docs/agent-memory/migrations.md) — `golang-migrate` workflow, authoring rules
- [docs/agent-memory/video-analysis.md](docs/agent-memory/video-analysis.md) — Two-pass architecture, anti-hallucination rules
- [docs/agent-memory/storage-and-session-format.md](docs/agent-memory/storage-and-session-format.md) — GCS layout details, backward compatibility
- [docs/agent-memory/mobile-runtime.md](docs/agent-memory/mobile-runtime.md) — Android performance flags, BLE heart rate, i18n
- [docs/agent-memory/auth.md](docs/agent-memory/auth.md) — JWT auth, ownership model, account deletion, hardening TODO
