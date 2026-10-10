## 2026-03-14 - Fix Arbitrary File Read and Deletion in Background Workers

**Vulnerability:** API endpoints (such as `/upload-complete`) accepted `gcs_uri` strings that were passed to background workers (`api/internal/worker/handler.go`). Paths without a `gs://` prefix were treated as local files, uploaded to Gemini, and deleted via `os.Remove()`. Furthermore, workers implicitly trusted payload paths from task queues.
**Learning:** External inputs and queue payloads must never be implicitly trusted across system boundaries. Workers must enforce their own strict validation and not fall back to local filesystem operations for remote storage URIs.
**Prevention:** Always validate and normalize URIs (enforcing `gs://` prefix) at both the API boundary and inside background workers (Defense in Depth). Never execute `os.Remove()` on arbitrary user- or payload-controlled paths.

## 2026-03-23 - Prevent Information Exposure in Analysis Errors

**Vulnerability:** In `api/internal/worker/handler.go`, when video or chunk analysis failed, raw error messages (`err.Error()`) containing internal filesystem paths (`/tmp/...`) and third-party error details were stored directly in the database `Output` field and exposed via user-facing endpoints (`/analysis`, `/history`).
**Learning:** Storing and returning raw backend error strings directly to client-facing payloads leaks internal architecture and infrastructure details (CWE-209).
**Prevention:** Always catch and log raw internal errors securely on the server side using the application logger, and return generic, safe error messages to clients (e.g., "An internal error occurred during analysis.").

## 2026-06-08 - Insecure Direct Object Reference (IDOR) on User-Specific Endpoints

**Vulnerability:** Endpoints handling telemetry uploads (`UploadDebugTelemetry`), highlights (`GetHighlight`, `GetHighlightDownloadURL`, `VerifyHighlights`), and video uploads lacked consistent profile and session ownership validation, allowing authenticated users to access or mutate resources belonging to other profiles.
**Learning:** Ownership checks must not rely on implicit assumptions or be skipped on telemetry/debug endpoints. Parameter injection in payloads allows cross-account manipulation if ownership is not explicitly verified early in the request lifecycle.
**Prevention:** In Go API controllers (`api/internal/controllers`), all endpoints handling user-scoped resources must explicitly call `ctl.assertOwnsProfile()` or `ctl.assertOwnsSession()` immediately after parsing the request.

## 2026-07-19 - Fix API Key Bypass on Mobile Auth Endpoints

**Vulnerability:** `APIKeyMiddleware` skipped the API key check whenever a `jwt` cookie was present, assuming `AuthMiddleware` would validate it downstream. However, mobile auth endpoints (`/auth/login`, `/auth/signup`) were unauthenticated and not protected by `AuthMiddleware`, allowing attackers to bypass API key enforcement with a dummy cookie.
**Learning:** Middleware skip logic based on unverified credentials introduces critical authorization bypasses if subsequent routes lack matching downstream validation.
**Prevention:** Never treat unverified credentials as a skip condition unless they are verified within the same middleware, or ensure the skip logic explicitly excludes public/unprotected routes.

## 2026-09-01 - Prevent Timing Attacks in Authentication Flows

**Vulnerability:** `Login` and `DeleteAccount` in `api/internal/auth/service.go` returned immediately when a user lookup failed, skipping the bcrypt password verification. This allowed attackers to enumerate valid usernames by measuring the significant response time difference between existing and non-existing accounts.
**Learning:** Early returns on authentication failures without matching computational cost leak account existence through side-channel timing analysis.
**Prevention:** Perform a dummy bcrypt comparison using a precomputed hash when a user lookup fails, ensuring constant-time response behavior for both valid and invalid usernames.

## 2026-10-09 - Comprehensive Path Traversal and Identifier Sanitization

**Vulnerability:** Across multiple controllers and worker routines, identifiers (`session_id`, environment names, storage paths) extracted from path parameters (`c.Param`), JSON payloads, or task queues were directly used in GCS URIs, local temporary files (`filepath.Join`), or deletion checks without cross-platform path traversal validation.
**Learning:**
1. Simple checks like `strings.Contains(path, "/")` or OS-specific `filepath.Separator` fail against Windows-style `\` separators or cross-platform traversal payloads (`..\`).
2. Calling `filepath.Base()` before validating raw input strips separators and silently bypasses validation.
3. Task queue payloads and URL parameters must be sanitized at both the API boundary and worker execution points.
**Prevention:**
1. Always validate raw input for path traversal before transformation using cross-platform checks (`strings.ContainsAny(input, "/\\")`).
2. Wrap path parameters functioning as identifiers with `sanitizeIdentifier()` and enforce strict format validation (e.g., `isValidSessionID()`) immediately upon extraction.
3. In workers, apply `filepath.Base()` to any dynamic components before constructing local filesystem paths in `filepath.Join()`.
