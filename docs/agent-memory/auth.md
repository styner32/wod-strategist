# Auth System Memory

## Architecture overview
- **Scheme:** JWT (HS256) with DB-side `token_version` check on every request (via 30s in-memory cache).
- **Single token** — no refresh token. Revocation is instant via `token_version` bump.
- **Password hashing:** bcrypt (`DefaultCost`).
- **User ID:** auto-increment `uint` (SERIAL). Originally planned as petname TEXT, migrated in `000026`.
- **API key gate:** `X-API-Key` remains in front of all routes including `/auth/*`.
- **Data scoping:** `users` → `profiles` (1:N), all leaf data scoped via `profile_id` ownership join.

## Key files
| Area | Files |
|------|-------|
| Auth service | `api/internal/auth/service.go`, `jwt.go`, `password.go` |
| Auth handlers | `api/internal/controllers/auth_handlers.go` |
| Ownership checks | `api/internal/controllers/ownership.go` |
| JWT middleware | `api/internal/server/middleware.go` → `AuthMiddleware` |
| Route wiring | `api/internal/server/router.go` → `SetupRouter` |
| Mobile auth store | `features/auth/useAuthStore.ts` |
| Mobile auth API | `features/auth/api.ts` |
| Token storage | `features/auth/storage.ts` (expo-secure-store) |
| Config | `api/internal/config/config.go` → `JWTSigningSecret` |

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
- When `userID == 0` (no auth context), ownership checks pass through — allows running without auth middleware during dev

## Signup flow
1. Server validates username (`^[a-z0-9_]{3,20}$`) and password (≥8 chars)
2. Server creates `users` row, auto-creates a default profile with `name = username`
3. Server returns `{ token, user_id }`
4. Mobile stores token + user_id in SecureStore
- **Do NOT** create a profile client-side after signup — the server already does it

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
- CORS `allowMethods` does not include `DELETE` — browser-based `DELETE /auth/account` will fail (mobile-only for now)
- No rate limiting exists on `/auth/login` or `/auth/signup`
- Workers do not check `deleted_at` — in-flight tasks continue after account deletion

## Remaining hardening TODO
- [ ] Implement rate limiting on `/auth/login` and `/auth/signup` (5 req/IP/15min)
- [ ] Wire up GCS cleanup on account deletion (resolve TODO in `auth_handlers.go:113`)
- [ ] Add `DELETE` to CORS `allowMethods` or document as mobile-only
- [ ] Decide username scrub-on-delete policy (currently blocks reuse)
- [ ] Add worker-side `deleted_at` check before processing tasks
- [ ] Add `JWT_SIGNING_SECRET` to infra/deployment configs
