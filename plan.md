1. **Fix `injuryTimestampBlockRegex` to support both `injury_timestamps` and `json` as fenced code block types:**
   - The test `parseInjuryTimestamps [It] extracts valid JSON from fenced code block` fails because it expects a `json` block, but the regex `(?is)```injury_timestamps\s*(\[.*?\])\s*``` ` only matches `injury_timestamps`.
   - Update `injuryTimestampBlockRegex` in `api/internal/worker/handler.go` to `(?is)```(?:injury_timestamps|json)\s*(\[.*?\])\s*``` ` to pass the test and be robust against Gemini's potential output variations.
2. **Complete pre-commit steps to ensure proper testing, verification, review, and reflection are done:**
   - Call `pre_commit_instructions` and follow its instructions.
3. **Submit the change:**
   - Use `submit` to commit and push changes with the appropriate message for the Sentinel PR.
