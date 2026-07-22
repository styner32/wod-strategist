# Auth System Memory

## Architecture overview
- **Scheme:** JWT (HS256) with DB-side `token_version` check on every request (via 30s in-memory cache).
- **Single token** — no refresh token. Revocation is instant via `token_version` bump.
- **Password hashing:** bcrypt (`DefaultCost`).
- **User ID:** auto-increment `uint` (SERIAL). Originally planned as petname TEXT, migrated in `000026`.
- **No application API-key gate.** Authorization is JWT only (`Authorization: Bearer` for mobile, `jwt` httpOnly cookie for web). Gemini uses a separate `GEMINI_API_KEY` / `X-Goog-Api-Key` unrelated to app auth.
- **Data scoping:** `users` → `profiles` (1:N), all leaf data scoped via `profile_id` ownership join.

## Key files
| Area | Files |
|------|-------|
| Auth service | `api/internal/auth/service.go`, `jwt.go`, `password.go` |
| Auth handlers | `api/internal/controllers/auth_handlers.go` |
| Ownership checks | `api/internal/controllers/ownership.go` |
| JWT middleware | `api/internal/server/middleware.go` → `AuthMiddleware` |
| Route wiring | `api/internal/server/router.go` → `SetupRouter` (fails closed if `authSvc == nil`) |
| Mobile auth store | `features/auth/useAuthStore.ts` |
| Mobile auth API | `features/auth/api.ts` |
| Token storage | `features/auth/storage.ts` (expo-secure-store) |
| Config | `api/internal/config/config.go` → `JWTSigningSecret` |
| Infra | `infra/compute.tf` → `JWT_SIGNING_SECRET` from Secret Manager |

## Migrations (auth-related)
- `000020` — `profile_id` NOT NULL on all leaf tables
- `000021` — `CREATE TABLE users`
- `000022` — `ADD COLUMN user_id` to profiles
- `000023` — seed user + assign orphan profiles
- `000024` — `user_id NOT NULL` on profiles
- `000026` — convert `users.id` from TEXT to SERIAL

## Ownership model
- `assertOwnsProfile(c, profileID)` — checks `profiles.user_id` matches authed user
- `assertOwnsSession(c, sessionID)` — joins through `analysis_results` / `chunk_analysis_results` → `profiles`
- `assertOwnsAnalysis(c, analysisID)` — joins `analysis_results` → `profiles`
- All return `403 Forbidden` on failure (not 404)
- When `userID == 0` (no auth context), ownership checks pass through — only relevant if AuthMiddleware is not applied (production router always applies it)

## Public vs protected routes
- **Public (rate-limited):** `/auth/login`, `/auth/signup`, `/auth/web/login`, `/auth/web/signup`, plus `/health`
- **Protected (JWT required):** all other `/api/v1/*` routes including logout, account deletion, and data APIs
- Login limiter is in-memory / per-instance (distributed limiter is a separate hardening task)

## Signup flow
1. Server validates username (`^[a-z0-9_]{3,20}$`) and password (≥8 chars)
2. Server creates `users` row, auto-creates a default profile with `name = username`
3. Server returns `{ token, user_id }`
4. Mobile stores token + user_id in SecureStore
- **Do NOT** create a profile client-side after signup — the server already does it
- Self-service signup endpoints currently reject with `403` (invite-only)

## Account deletion
1. Handler requires password re-confirmation
2. Transaction deletes: feedback, chunk/session re-analysis runs, `analysis_results`, `chunk_analysis_results`, `highlight_results`, `token_usages` (by profile_id), then profiles, then soft-deletes user (`deleted_at`, scrubs `password_hash`, bumps `token_version`)
3. Returns `gcsPrefixes` for async GCS cleanup — **currently not wired up** (see TODO)

## 401 handling (mobile)
- `apiClient` in `features/wod/api.ts` intercepts 401 responses
- Calls `useAuthStore.getState().handleUnauthorized()` which clears SecureStore + resets state
- `_layout.tsx` redirects to `/auth/login` when `isLoggedIn` is false

## Gotchas
- Username unique index is partial: `WHERE deleted_at IS NULL` — deleted usernames are NOT freed for reuse (username is not scrubbed on delete)
- CORS `allowMethods` includes `DELETE` and `PATCH`; `allowHeaders` is `Content-Type, Authorization` (no `X-API-Key`)
- Workers do not check `deleted_at` — in-flight tasks continue after account deletion
- Old mobile clients may still send an ignored `X-API-Key` header; that is harmless after server removal
- Legacy CLI scripts (`scripts/test-upload.js` / `scripts/test-chunk-upload.js`) still send only an API key and will fail JWT middleware until they gain a real login flow or are retired (the browser QA page was removed in favor of the web app)

## Remaining hardening TODO
- [ ] Replace in-memory login rate limiter with a distributed limiter
- [ ] Wire up GCS cleanup on account deletion (resolve TODO in `auth_handlers.go`)
- [ ] Decide username scrub-on-delete policy (currently blocks reuse)
- [ ] Add worker-side `deleted_at` check before processing tasks
