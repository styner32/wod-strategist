## 2024-03-14 - Fix SSRF and Arbitrary File Read/Delete in GCS Worker

**Vulnerability:** The `/upload-complete` API endpoint accepted a `gcs_uri` string which was subsequently passed to the worker `api/internal/worker/handler.go`. The worker checked if the path began with `gs://` to treat it as a GCS URI, but otherwise treated it as a local file path. It then read this file for processing and eventually executed `os.Remove(localFilePath)`.
**Learning:** Because external input at the API boundary wasn't validated as strictly adhering to the expected format (`gs://`), users could provide local file paths (like `/app/gcp-key.json` or `/etc/passwd`) to arbitrary file read and delete them. The background worker implicitly trusted the path provided by the API.
**Prevention:** Always strictly validate and normalize external resource URIs (e.g., ensuring a `gs://` prefix) at the first API boundary (e.g., `router.go`) before enqueuing them to a background worker. Don't rely on workers to sanitize input when they fall back to a less secure default (like local file access).

## 2024-06-11 - Prevent Arbitrary File Read and Deletion in Worker

**Vulnerability:** Arbitrary file read and deletion via the `/upload-complete` endpoint and Asynq worker. `GcsURI` in the request was not validated to ensure it starts with `gs://`. The worker treated any path without the `gs://` prefix as a local file, directly uploading it to Gemini and then deleting it via `os.Remove`.
**Learning:** When passing file paths or URIs across system boundaries (from API to background worker), validation must be enforced at both the API boundary and before the worker accesses the filesystem. Also, `defer os.Remove(localFilePath)` is dangerous if `localFilePath` can be controlled by user input.
**Prevention:** Always validate and sanitize user-provided URIs (e.g., enforce `gs://` prefix). Use `filepath.Base` for any dynamic components used in local temporary file generation. Ensure proper separation between external URIs and internal filesystem paths.

## 2024-11-06 - Prevent Arbitrary File Read and Deletion in Worker (Queue Trust)

**Vulnerability:** The `api/internal/worker/handler.go` worker blindly trusted the `p.FilePath` payload from the Redis Asynq queue to be a valid file. If the path did not start with `gs://`, it would default to treating it as a local file, reading it, uploading it to Gemini, and then deleting it with `os.Remove`. Furthermore, `p.SessionID` was used directly in a `filepath.Join` without sanitization when constructing a local temporary file path for downloading GCS objects. This allowed an attacker manipulating the background queue to perform arbitrary file reads and deletions, and path traversal during GCS downloads.
**Learning:** Do not implicitly trust payloads from a task queue, even if they originated from your own API. The API boundary validation (like checking `gs://` on `/upload-complete`) can be bypassed if an attacker can enqueue tasks directly, or if there is another unvalidated path. Workers must enforce their own strict validation and sanitization (Defense in Depth).
**Prevention:** Always strictly validate and normalize URIs (enforce `gs://`) within background workers before accessing them. Always sanitize identifiers like `SessionID` using `filepath.Base` before using them in `filepath.Join` to construct local filesystem paths.

## 2025-03-20 - Prevent Path Traversal in Worker Temp File Creation

**Vulnerability:** A path traversal vulnerability existed in `api/internal/worker/handler.go` where `p.SessionID` was directly used in `filepath.Join("/tmp", fmt.Sprintf("%s_%s", p.SessionID, filepath.Base(p.FilePath)))` to construct a temporary file path when downloading a file from GCS. An attacker could potentially supply a malicious `SessionID` (e.g., `../../etc/cron.d/`) to write downloaded files to unintended locations on the worker's filesystem.
**Learning:** Even if `SessionID` is sanitized at the API boundary, it should be re-sanitized when constructing local file paths inside background workers. The payload is re-created from JSON by the worker and may come from a manipulated queue or bypass API validation.
**Prevention:** Always apply `filepath.Base()` to any dynamically generated string (like IDs or filenames) that is used as a path segment in `filepath.Join()`, especially before creating or opening local files.

## 2024-05-20 - Prevent Information Exposure in Analysis Errors

**Vulnerability:** In `api/internal/worker/handler.go`, when a video or chunk analysis failed (e.g., due to downstream Gemini API errors, GCS download errors, or local filesystem permission issues), the raw `err.Error()` was saved to the database in the `Output` field of the results table. These results are exposed to the user via the `/analysis` and `/history` endpoints.
**Learning:** Returning or saving unhandled raw error strings from backend components (like file system errors containing `/tmp/` paths, or third-party service errors) directly to user-facing payloads leaks internal system details. This information exposure (CWE-209) violates the principle of failing securely.
**Prevention:** Always catch raw internal errors, securely log them on the server side using the application's logging framework, and replace the user-facing output with safe, generic error messages (e.g., "An internal error occurred during analysis.").

## 2025-04-09 - Path Traversal Validation Bypass via filepath.Base()
**Vulnerability:** Path Traversal vulnerability check bypass in background workers.
**Learning:** `filepath.Base()` strips path separators from strings. Calling `filepath.Base()` on user input and then checking the *result* for path separators using `strings.ContainsRune(val, filepath.Separator)` will always silently pass, bypassing the validation logic meant to reject malicious input. The validation must occur *before* sanitizing the input or checking the original input string.
**Prevention:** Always validate and check for path separators on the *raw* user input before applying any path transformation or sanitization functions.

## 2025-05-13 - [Path Traversal in Session ID Handlers]
**Vulnerability:** Path parameters such as `session_id` in API handlers (e.g. `GetAnalysis`, `GetHighlight`) were being read directly via `c.Param("session_id")` and used in database lookups or file path constructions without sanitization.
**Learning:** Even though ORMs like GORM provide some injection protection, unvalidated strings read directly from HTTP paths can lead to path traversal if they are subsequently used to fetch or construct storage URIs (e.g., interacting with local disk or GCS).
**Prevention:** Always wrap path or query parameters that function as identifiers with a dedicated sanitization function (like `sanitizeIdentifier`) before using them in further logic.

<<<<<<< HEAD
## 2024-05-27 - Path Traversal in File Paths
**Vulnerability:** The `validateSessionID` function merely checks for `filepath.Separator` (e.g. `/` or `\` depending on OS) to prevent path traversal issues. However, if paths are built using simple concatenation or other means, `filepath.Base` is recommended per memory guidelines: "Do not trust payloads from the Asynq task queue implicitly; always re-sanitize and validate inputs (e.g., applying `filepath.Base` to identifiers) within background workers, as the queue could potentially be manipulated or bypass API validation."
**Learning:** Checking for separators is insufficient, especially when components like `..` can be manipulated and bypass simple checks. Using `filepath.Base` ensures that only the final element of the path is used, inherently preventing directory traversal attacks.
**Prevention:** Always use `filepath.Base` on user-supplied identifiers intended to be part of file paths or object names before using them.

## 2025-05-24 - Path Traversal Check OS-Dependency Bypass
**Vulnerability:** The `validateSessionID` function in `api/internal/worker/worker.go` used `strings.ContainsRune(sessionID, filepath.Separator)` to check for path separators. Because `filepath.Separator` is determined by the operating system the code is compiled for/running on (`/` on Linux, `\` on Windows), this check would fail to detect a backslash (`\`) when running on a Linux system, allowing potential path traversal bypasses if the payload later reached a Windows-based system or a loosely-parsed filesystem implementation.
**Learning:** Security validation logic should not rely on OS-specific path separators. Attackers can supply cross-platform payloads (like Windows-style paths `..\`) to applications running on Unix, bypassing validation checks that only look for Unix separators.
**Prevention:** When explicitly denying path characters to prevent traversal or arbitrary file operations, always check for both Unix (`/`) and Windows (`\`) separators manually (e.g., using `strings.ContainsAny(sessionID, "/\\")`), rather than relying on the single OS-dependent `filepath.Separator`.

## 2024-05-24 - Missing Authorization (IDOR) on Upload Endpoint
**Vulnerability:** The `UploadDebugTelemetry` endpoint accepted a JSON payload containing `profileId` without validating that the authenticated user actually owned that profile. This is an Insecure Direct Object Reference (IDOR) that allowed any user to upload debug telemetry data on behalf of other users' profiles.
**Learning:** Endpoints that handle debug, telemetry, or seemingly low-value data are often overlooked for authorization checks, but parameter injection in the payload allows a malicious actor to inject or corrupt data for other users.
**Prevention:** All endpoints that perform state changes or storage operations tied to a specific resource (e.g., `profileId`) must validate ownership by explicitly calling the `assertOwnsProfile(c, req.ProfileID)` utility before processing the payload.

## 2026-06-08 - [High] Insecure Direct Object Reference (IDOR) / Missing Authorization in handlers
**Vulnerability:** Found multiple IDOR and missing authorization vulnerabilities in endpoints serving highlight data (`GetHighlight`, `GetHighlightDownloadURL`, `VerifyHighlights`) and initiating uploads (`Upload`) in the Go backend. Attackers could guess highlight IDs or use known session IDs to access or modify data belonging to other users.
**Learning:** `ctl.assertOwnsProfile` and `ctl.assertOwnsSession` were implemented as utility functions but were not consistently called early in the request lifecycle (fail-fast principle) across all endpoints handling user-specific data. Some endpoints relied on implicit checking or no checking at all. In the Go backend, the database teardown utility (`CleanupDB` in `api/internal/testhelpers/factory.go`) strictly requires the test database name to end with `_test` (e.g., `wod_test`); otherwise, it will panic to prevent the accidental deletion of non-test data.
**Prevention:** In the Go backend API controllers (`api/internal/controllers`), endpoints handling requests tied to user-specific resources must explicitly validate ownership using `ctl.assertOwnsProfile()` or `ctl.assertOwnsSession()` immediately after parsing and sanitizing the request input.

## 2024-07-17 - Go filepath.Base Path Traversal Bypass
**Vulnerability:** Path traversal and local file write vulnerabilities were possible because `filepath.Base(".")` returns `"."` and `filepath.Base("..")` returns `".."`. When validating identifiers, inequality checks like `input != filepath.Base(input)` alone failed to block these inputs.
**Learning:** In Go, relying solely on `filepath.Base()` for path traversal protection without explicit equality checks against `"."`, `".."`, and empty strings `""` leads to bypasses.
**Prevention:** Always explicitly check for `""`, `"."`, and `".."` before using `filepath.Base()` for path sanitization or validation.
