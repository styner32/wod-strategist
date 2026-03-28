1. **Fix Information Exposure in Chunk Analysis Errors (`api/internal/worker/handler.go`)**
   - The `HandleChunkAnalysisTask` function currently sets the `Output` field of the failed `ChunkAnalysisResult` to `err.Error()`. This exposes raw internal errors to the user.
   - The fix is to replace `err.Error()` with a safe, generic error message like `"An internal error occurred during chunk analysis."` and ensure the raw error is only logged on the server side (which is already happening).

2. **Run Tests to Verify Fix**
   - Run `go test ./...` in the `api/` directory (with a dummy database URL as required by tests) to ensure the API still works correctly.

3. **Pre Commit Steps**
   - Complete pre-commit steps to make sure proper testing, verifications, reviews and reflections are done.

4. **Submit Change**
   - Submit the PR with the fix.
