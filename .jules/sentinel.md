## 2024-03-14 - Fix SSRF and Arbitrary File Read/Delete in GCS Worker

**Vulnerability:** The `/upload-complete` API endpoint accepted a `gcs_uri` string which was subsequently passed to the worker `api/internal/worker/handler.go`. The worker checked if the path began with `gs://` to treat it as a GCS URI, but otherwise treated it as a local file path. It then read this file for processing and eventually executed `os.Remove(localFilePath)`.
**Learning:** Because external input at the API boundary wasn't validated as strictly adhering to the expected format (`gs://`), users could provide local file paths (like `/app/gcp-key.json` or `/etc/passwd`) to arbitrary file read and delete them. The background worker implicitly trusted the path provided by the API.
**Prevention:** Always strictly validate and normalize external resource URIs (e.g., ensuring a `gs://` prefix) at the first API boundary (e.g., `router.go`) before enqueuing them to a background worker. Don't rely on workers to sanitize input when they fall back to a less secure default (like local file access).

## 2024-06-11 - Prevent Arbitrary File Read and Deletion in Worker

**Vulnerability:** Arbitrary file read and deletion via the `/upload-complete` endpoint and Asynq worker. `GcsURI` in the request was not validated to ensure it starts with `gs://`. The worker treated any path without the `gs://` prefix as a local file, directly uploading it to Gemini and then deleting it via `os.Remove`.
**Learning:** When passing file paths or URIs across system boundaries (from API to background worker), validation must be enforced at both the API boundary and before the worker accesses the filesystem. Also, `defer os.Remove(localFilePath)` is dangerous if `localFilePath` can be controlled by user input.
**Prevention:** Always validate and sanitize user-provided URIs (e.g., enforce `gs://` prefix). Use `filepath.Base` for any dynamic components used in local temporary file generation. Ensure proper separation between external URIs and internal filesystem paths.

## 2024-10-27 - Fix Path Traversal in GCS Worker

**Vulnerability:** The worker in `api/internal/worker/handler.go` constructs a temporary file path for downloaded GCS files using `filepath.Join` with an unsanitized `p.SessionID`. This could allow a malicious user to manipulate the `SessionID` (e.g., `../../../etc/passwd`) to write files outside the intended `/tmp` directory.
**Learning:** Even if `FilePath` is sanitized using `filepath.Base()`, other dynamic components of the path, such as `SessionID`, must also be sanitized if they can be influenced by user input.
**Prevention:** Always sanitize all user-provided components (e.g., using `filepath.Base()`) before using them in file path construction.
