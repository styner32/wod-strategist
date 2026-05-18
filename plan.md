1. **Identify the Security Issue**:
   The memory file `.jules/sentinel.md` states:
   "To prevent information exposure (CWE-209), do not expose raw internal error strings (e.g., `err.Error()`) in client-facing outputs, such as API JSON responses or user-facing database fields (like `Output` in `AnalysisResult`). Log actual errors strictly server-side and return generic error messages."

   In `api/internal/worker/split_video.go:143`:
   `w.saveChunkResult(p, gcsURI, start, end, "FAILED", "", "Analysis failed: "+analysisErr.Error())`
   The raw `analysisErr.Error()` is being written into the user-facing `Output` field of the `AnalysisResult` table for the chunk.

2. **Fix**:
   In `api/internal/worker/split_video.go:143`, modify the code to prevent information exposure by returning a generic, safe error message:
   `w.saveChunkResult(p, gcsURI, start, end, "FAILED", "", "An internal error occurred during chunk analysis.")`

3. **Verify**:
   Run `cd api && go build ./...` and `go test ./...`
   Lint `cd api && go fmt ./...`

4. **Complete pre commit steps**
   Complete pre-commit steps to ensure proper testing, verification, review, and reflection are done.

5. **Submit**:
   Submit PR with security formatting.
