1. **Fix API Key Timing Attack Vulnerability in Go Backend (`api/internal/server/router.go`)**
   - The current code compares the provided `X-API-Key` to the `API_SECRET` environment variable using a direct string comparison (`apiKey != apiSecret`).
   - This makes the API key comparison vulnerable to a timing attack, where an attacker could theoretically guess the API key character by character based on the time it takes for the comparison to fail.
   - The fix is to use `crypto/subtle.ConstantTimeCompare` instead of `!=`.

2. **Run Tests to Verify Fix**
   - Run `go test ./...` in the `api/` directory (with a dummy database URL as required by tests) to ensure the API still works correctly.

3. **Pre Commit Steps**
   - Complete pre-commit steps to make sure proper testing, verifications, reviews and reflections are done.

4. **Submit Change**
   - Submit the PR with the fix.
