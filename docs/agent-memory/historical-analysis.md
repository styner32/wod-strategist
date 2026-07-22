# Historical Analysis: WOD Descriptor + Session Scoring

> **Current implementation, not target design:** the active remediation plan is
> [`docs/video-analysis-improvement/04-phase-3-accuracy-and-contracts.md`](../video-analysis-improvement/04-phase-3-accuracy-and-contracts.md),
> especially ACC-07. The current last-segment scoring approach cannot see all
> session evidence. New work must move scoring to a final synthesis over every
> successful validated segment, compute the current absolute score before
> historical comparison, and validate/recompute `overall` on the server.

## Overview

Each completed session stores two new fields on `analysis_results`:
- `wod_description TEXT NOT NULL DEFAULT ''` — user-supplied WOD descriptor (migrations 000028)
- `session_score TEXT NOT NULL DEFAULT '{}'` — JSON score block parsed from model output (migration 000029)

Both are injected into future analysis prompts for personalized comparison.

---

## `session_score` Schema

```json
{
  "overall": 74,
  "form": 68,
  "intensity": 82,
  "consistency": 72,
  "movements": {
    "Snatch": {"form": 65, "intensity": 80}
  },
  "summary": "스내치 풀 익스텐션이 개선되었으나 팔꿈치 조기 굽힘이 관찰됩니다."
}
```

| Field | Range | Notes |
|---|---|---|
| `overall` | 0–100 | `form×0.5 + intensity×0.3 + consistency×0.2` (skill WODs: `form×0.7`) |
| `form` | 0–100 | Absolute CrossFit standard for fitness level |
| `intensity` | 0–100 | Absolute, NOT relative to user's own history |
| `consistency` | 0–100 | Set/round-to-round uniformity |
| `movements` | map | Only movements observed on video |
| `summary` | string | One Korean sentence |

**CRITICAL**: Scores are anchored to **absolute CrossFit standards for the user's fitness level** — never inflate because the user tried hard. This keeps scores stable and comparable over time.

---

## Score Output Prompt

The constant `ScoreOutputPrompt` (in `internal/worker/history.go`) is appended **only to the last segment's prompt** in `buildSegmentAnalysisPrompt`. It instructs the model to emit a fenced ` ```score ``` ` block at the end of the analysis.

**Do NOT inject `ScoreOutputPrompt` into chunk analysis prompts** — chunk scores are too noisy (10s clips).

---

## Score Parsing

```go
// scoreBlockRegex matches ```score ... ``` fenced blocks.
var scoreBlockRegex = regexp.MustCompile("(?is)```score\\s*(\\{.*?\\})\\s*```")

func parseSessionScore(output string) string
```

- Returns `"{}"` if no block found or JSON is malformed.
- Re-marshals to produce compact, consistent output.
- Called in `handleVideoAnalysisTwoPass` after assembling the full analysis string.

---

## `buildWODContext`

```go
func buildWODContext(wodDescription string) string
```

- Returns `""` if `wodDescription` is empty/whitespace.
- For named WODs (`"fran"`, `"grace"`, `"helen"`, `"murph"`, `"cindy"`, `"annie"`): injects known benchmark times and Rx standards.
- For custom WODs: detects type from keywords (`for time`, `amrap`, `emom`, `1rm`, `skill`, `max`) and adjusts guidance accordingly.
- Injected into **both** segment prompts (`video_analysis.go`) and chunk prompts (`chunk_analysis.go`).

---

## `buildHistoryContext`

```go
func (w *Worker) buildHistoryContext(profileID uint, limit int) string
```

- Queries `analysis_results` where `profile_id = ? AND status = 'COMPLETED' AND archived_at IS NULL AND session_score != '{}'`.
- Orders by `created_at DESC`, limits to `limit` (default 5).
- Skips sessions with all-zero scores.
- Returns a Korean markdown table with columns: 날짜, WOD, 종합, 자세, 강도, 일관성.
- Returns `""` when DB is nil, profileID is 0, or no qualifying sessions found.
- **Called once per two-pass analysis**, before the segment loop. Injected into the last segment's prompt together with `ScoreOutputPrompt`.

---

## Named WOD Benchmark Table

| Name (lowercase) | Benchmark |
|---|---|
| fran | 21-15-9 Thrusters + Pull-ups, For Time |
| grace | 30 Clean & Jerks, For Time |
| helen | 3 rounds: 400m Run + 21 KB Swings + 12 Pull-ups, For Time |
| murph | 1mi Run + 100 Pull-ups + 200 Push-ups + 300 Air Squats + 1mi Run |
| cindy | AMRAP 20: 5 Pull-ups + 10 Push-ups + 15 Air Squats |
| annie | 50-40-30-20-10 Double-Unders + Sit-ups, For Time |

To add more named WODs: add a new `case` in the `switch lower` block in `buildWODContext`.

---

## Data Flow Summary

```
User submits session with wod_description="Fran"
  → VideoAnalysisPayload.WODDescription = "Fran"
  → buildWODContext("Fran") → benchmark hint string
  → buildHistoryContext(profileID, 5) → last N scores table
  → [last segment prompt] += wodContext + historyContext + ScoreOutputPrompt
  → Gemini outputs analysis with ```score {...}``` block
  → parseSessionScore(analysis) → compact score JSON
  → DB: AnalysisResult{WODDescription:"Fran", SessionScore:"{...}"}
```

---

## Migrations

| # | Column | Type | Default |
|---|---|---|---|
| 000028 | `wod_description` | TEXT NOT NULL | `''` |
| 000029 | `session_score` | TEXT NOT NULL | `'{}'` |

Run after adding migrations:
```bash
make migrate-test-redo
```

---

## Anti-Patterns to Avoid

- **Do NOT** inject `ScoreOutputPrompt` into every segment — only the last one.
- **Do NOT** use relative scoring ("better than the user's last session") — always use absolute CrossFit standards.
- **Do NOT** call `buildHistoryContext` inside chunk analysis — chunk analysis is real-time and has no historical comparison need.
- **Do NOT** parse scores from chunk analysis output — `parseSessionScore` is called only in `handleVideoAnalysisTwoPass`.

---

## Whiteboard Image Parsing (`POST /parse-workout-image`)

### Flow
```
Mobile captures whiteboard photo
  → POST /api/v1/parse-workout-image (multipart: image)
  → Server: DetectImageMIME → NormalizeImage (max 1024px, JPEG q85)
  → Gemini Flash (gemini-3.6-flash) with inline InlineData part
  → Parse ```workout { ... } ``` block
  → Response: { wod_description, movements[], raw_text }
```

### Key Design Decisions
- **Inline, not file upload**: Images are small (<10MB). Uses `genai.Blob{InlineData}` — no upload/poll/delete cycle needed.
- **Flash model**: Image OCR doesn't need Pro. Uses the `flashModel` const already in `client.go`.
- **Server-side resize**: `NormalizeImage()` in `gemini/image.go` resizes to max 1024px longest side, re-encodes as JPEG quality 85. Reduces token cost and latency.
- **Bilingual prompt**: Korean + English support since gym whiteboards may use either language.
- **Typo correction**: Prompt includes a comprehensive abbreviation/typo table (DL→Deadlift, PU→Pull-up, etc.)
- **No persistence**: Image is NOT stored; sent to Gemini and discarded.

### Files
| File | Purpose |
|---|---|
| `gemini/image.go` | `NormalizeImage()`, `DetectImageMIME()`, `resizeToFit()` |
| `gemini/client.go` | `ParseImage()` method |
| `controllers/handlers.go` | `ParseWorkoutImage` handler, `parseWorkoutBlock()`, `wodParsePrompt` |
| `controllers/dto.go` | `ParseWorkoutImageResponse` |
| `controllers/controller.go` | `ImageParser` interface |
| `config/config.go` | `GeminiAPIKey` on `Server` config |
| `cmd/server/main.go` | Gemini client initialization for API server |
| `server/router.go` | Route registration |

### Response → Session Setup Mapping
- `response.wod_description` → `CompleteUploadRequest.wod_description`
- `response.movements` → pre-select in movement picker UI
- `response.raw_text` → display for user review (optional)
