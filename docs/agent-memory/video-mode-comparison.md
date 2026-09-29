# Static / Agentic video comparison

For production implementation guidelines, streaming patterns, and anti-patterns, see
[`agentic-video-guidelines.md`](agentic-video-guidelines.md).

## Purpose and invocation

`api/cmd/compare-video` is a local experiment, not a production worker mode.
Run from `api/` with the existing `DATABASE_URL`, `GCS_BUCKET_NAME`,
`GEMINI_API_KEY`, and GCS application credentials. `.env` is loaded without
overriding exported variables.

```sh
go run ./cmd/compare-video --latest-completed-wod --output-dir /private/tmp/wod-video-comparison
go run ./cmd/compare-video --session-id WOD-YYYYMMDD-ULID --output-dir /private/tmp/wod-video-comparison-specific
```

- Exactly one selector is required; the output directory must be new.
- Selection uses non-archived, `COMPLETED`, `analysis_type=wod` records with
  current `WOD-` or legacy `P…-WOD-` session IDs, ordered by
  `COALESCE(workout_at, created_at) DESC, id DESC`. This is latest workout
  chronology, not latest reanalysis completion. Missing source videos are skipped
  only in latest mode; source access errors stop the experiment.
- Reuse the existing canonical/legacy merged-video resolver. Download the exact
  GCS generation once; record generation, size, SHA-256, and ffprobe duration.
- DB work runs inside read-only transactions. No analysis updates, queue tasks,
  token usage inserts, migrations, or UI changes occur.

## Request contract

- SDK is pinned to `google.golang.org/genai v1.71.0`.
- Use `GenerateContent` and native `Part.MediaProcessing`: `STATIC` then `AGENTIC`,
  one request each, model `gemini-3.8-flash`, HIGH media resolution and HIGH thinking.
- Both calls share one freshly uploaded Files URI and the same production
  indexing prompt snapshot. This experiment inherits that prompt's existing
  limitations; it is not a new coaching or counting accuracy evaluation.
- Neither mode sets `VideoMetadata` or fixed FPS; STATIC uses API-default sampling.
  Agentic does not support custom FPS or clipping offsets. Do not replace the
  production 5 FPS `AnalyzeSegment` path with this experiment.
- Both modes have a ten-minute deadline. Explicitly set SDK `Attempts=1`:
  v1.71 defaults to five attempts. Existing `NewClientWithOptions` also sets one
  attempt to preserve worker-owned retries after the upgrade.
- Keep `exactSegmentOffsetsTransport`: v1.71 still rounds fractional offsets.

## Evidence and cleanup

- `static/agentic.request.json` and `static/agentic.response.json` are exact HTTP
  bodies, not reserialized SDK structs. The dot-separated filenames are
  `static.request.json`, `agentic.response.json`, etc.
- Capture no headers, API keys, upload payloads, or authentication URLs. Artifacts
  use private directory/file permissions and should stay outside Git.
- Generation latency runs from HTTP dispatch through response body EOF; exclude
  parsing, GCS download, Files upload, and readiness polling. Those preparation
  durations are recorded separately in `summary.json` and `report.md`.
- Missing token fields remain null/N/A, not zero. Preserve thinking, tool-use,
  cache, input, output, and total counts separately; do not add possibly overlapping
  usage categories to reconstruct a bill.
- Timestamp differences pair exact exercise labels by occurrence order. These
  provisional pairs are not verified event identities; missed detections can
  shift pairs. Unmatched intervals remain N/A.
- `AgenticObserved` requires both MEDIA_PROCESSING tool calls and responses.
  HTTP 200 / a final answer alone is insufficient. Raw observations are not proof
  of workout accuracy. Preserve errors, truncated output, and empty replies.
- Each mode's artifacts are saved before starting the next. A failed STATIC call
  does not discard its evidence or suppress the one AGENTIC call.
- Delete only the experiment-created Gemini file, including after polling or
  generation failure. Record cleanup failure and file name for recovery. Never
  delete pre-existing Gemini files or GCS source objects.
- One call per mode, fixed order and uncontrolled implicit caching do not establish
  general latency, cost savings, counting accuracy, or deployment readiness.

## Streaming diagnostic (2026-09-28)

- `NewComparisonClient` also supports `CompareVideoModeStream` with the same
  model, prompt, resolution, configured thinking and single-attempt deadline.
  The existing comparison CLI remains unary; the streaming diagnostic is a
  separate explicit experiment, not an automatic retry or production switch.
- Record the exact body as `.response.sse` via `StreamComparisonOptions.RawResponse`.
  The transport must wrap body reads, never `ReadAll` before yielding to the SDK.
  `OnEvent` reports chunk timing and tool counts without exposing thought text.
- Preserve partial SSE, final-text fragments and tool observations after an
  error. Completion requires body EOF, a final `STOP`, and non-empty answer.
  Count MEDIA_PROCESSING calls even on parts marked `thought`; exclude thought
  text from the final answer. Retain unknown JSON fields in the raw stream.
- The last raw usage snapshot is retained with nullable fields; do not sum chunk
  usage or replace missing values with the SDK's numeric zero defaults.
- Streaming may expose earlier evidence but does not guarantee periodic data or
  prevent a context/idle timeout. Official video documentation also has unary
  examples; a streaming-only support requirement is not established there.
  Likewise the prior 600-second client deadlines do not prove a proxy idle timeout.
- Hold thinking at HIGH for the first streaming diagnostic so only transport
  changes; any MEDIUM experiment is a separately identified condition.
- The 2026-09-28 live stream emitted `toolCall`/`toolResponse` with matching `id`
  but no `toolType`. Preserve these as `untyped_tool_calls`/`untyped_tool_responses`.
  Do not silently label them MEDIA_PROCESSING or treat their absence from the
  typed counters as proof that no tools ran. Keep the raw SSE and `responseId`
  for server-side correlation. Missing metadata is not a decoding error.
- The subsequent 520.9-second local-video test terminated with
  `finishReason=TOO_MANY_TOOL_CALLS` and body EOF after 24 ID-only call/response
  pairs; no final text. This is an observed run, not evidence of a universal
  24-call limit. Its final response was in an additional unindexed candidate.
  Count tool parts across all candidates, while retaining first-candidate
  answer/finish semantics. A clean SSE EOF or HTTP 200 alone is not success.

Streaming experiments can set `StreamComparisonOptions.MediaResolution` explicitly (for example `genai.MediaResolutionLow`); omitted values preserve HIGH. This does not change production analysis defaults. DB focus windows use completed `chunk_analysis_results` and verified `media_start_secs`/`media_end_secs`, not capture-clock offsets. Prompt-only Agentic focus does not hard-clip the input video.
