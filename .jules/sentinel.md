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

## 2024-11-20 - Fix Silent Truncation of Malicious SessionIDs (Path Traversal)

**Vulnerability:** Validation of `SessionID` checked for path separators *after* applying `filepath.Base()`. Because `filepath.Base()` strips separators, the validation check was dead code, silently truncating malicious input rather than explicitly rejecting it, which could lead to unexpected behavior or bypasses in other logic.
**Learning:** When sanitizing inputs to prevent path traversal, validate the raw input *before* sanitizing it. Checking the sanitized output for bad characters is ineffective if the sanitization function removes them.
**Prevention:** Validate `strings.ContainsRune(rawInput, filepath.Separator)` before calling `filepath.Base(rawInput)`.
