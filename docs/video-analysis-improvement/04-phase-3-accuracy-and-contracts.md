# Phase 3: Accuracy, Evidence, and Output Contracts

Priority: P0 for target/safety issues; P1 for broader quality
Depends on: trustworthy media timeline and the Phase 2 baseline

Goal: replace prompt-only guarantees and regex extraction with evidence-bound, validated contracts, then synthesize a session result from all successful segments.

## Design principles

1. User-entered movements are candidates/context, not proof that a movement appears.
2. Unknown profile attributes remain unknown; never invent demographics.
3. Person-specific feedback requires visible, unambiguous target evidence.
4. Injury output describes visible mechanics and uncertainty, not pain, diagnosis, prognosis, or return-to-play readiness.
5. Structured output guarantees syntax only. Server-side semantic validation is still mandatory.
6. An invalid timestamp is not converted to zero and silently accepted.
7. Session conclusions must be derived from all successful evidence, not only the last segment.

## PR 3A — Structured output and validation

Work item: ACC-01

### Files to inspect/change

- `api/internal/gemini/client.go`
- `api/internal/worker/chunk_analysis.go`
- `api/internal/worker/video_analysis.go`
- `api/internal/worker/injury_analysis.go`
- `api/internal/worker/verify_highlights.go`
- `api/internal/worker/history.go`
- New narrow files such as `analysis_contracts.go` and `analysis_validation.go` in `api/internal/worker/` are acceptable
- Unit tests for schemas/validators plus existing real-client worker integration tests

### Use SDK structured output

For classification/extraction calls, set JSON response MIME type and a supported response schema in `GenerateContentConfig`. Keep schemas shallow and use explicit enums. Google documents that structured output provides syntactically valid schema-shaped JSON but still requires semantic application validation.

Reference: [Gemini structured output](https://ai.google.dev/gemini-api/docs/structured-output).

Do not ask for Markdown plus fenced JSON in the same call. Model calls should return a typed evidence object; user-facing Markdown is rendered later.

### Chunk evidence contract

Use a versioned Go DTO similar to:

```json
{
  "schema_version": 1,
  "target_state": "visible",
  "target_confidence": 0.91,
  "movement": {
    "canonical_id": "snatch",
    "display_name": "Snatch",
    "confidence": 0.88
  },
  "observations": [
    {
      "category": "mechanic",
      "start_secs": 2.1,
      "end_secs": 4.3,
      "claim": "hips rise before the chest",
      "confidence": 0.82
    }
  ],
  "rep_count": 2,
  "coaching_cue": "Keep the chest and hips rising together.",
  "abstain_reason": ""
}
```

Contract rules:

- `target_state`: `visible`, `ambiguous`, or `not_visible`.
- `movement` is nullable; unknown is not an empty alias for a known movement.
- Evidence timestamps are relative to the clip sent to the model. The worker adds the validated media offset when creating session-level evidence.
- `rep_count` is nullable when counting is not assessable.
- `coaching_cue` is empty unless target and movement evidence are sufficient.
- `abstain_reason` uses a low-cardinality enum/string such as `target_ambiguous`, `target_not_visible`, `movement_unclear`, `view_occluded`, or `insufficient_frames`.

### Deep-segment evidence contract

Each Pro segment call returns compact evidence, not final session prose:

```json
{
  "schema_version": 1,
  "segment_id": "media:12.400-27.900",
  "canonical_movement_id": "snatch",
  "target_state": "visible",
  "assessability": "assessable",
  "positive_mechanics": [],
  "corrections": [
    {
      "evidence_start_secs": 15.2,
      "evidence_end_secs": 16.8,
      "claim": "early arm bend before full extension",
      "severity": "moderate",
      "confidence": 0.86,
      "cue": "Finish leg and hip extension before pulling with the arms."
    }
  ],
  "visible_risks": [],
  "rep_count": 3,
  "notes": ""
}
```

All timestamps are absolute media seconds or clearly documented relative seconds; choose one representation and enforce it in the type. Absolute media seconds are recommended after the server validates the clip range.

### Semantic validator

Validate before persistence or user rendering:

- schema version supported;
- finite timestamps with `0 <= start < end <= actual media duration`;
- segment evidence stays inside the requested `VideoMetadata` interval, allowing only the documented encoder tolerance;
- confidence in `[0,1]`;
- score values in `[0,100]`;
- enums from allowlists;
- nonnegative/null rep counts;
- canonical movement IDs from the registry or `unknown`;
- no duplicate near-identical evidence intervals/claims;
- maximum string/array lengths to control output and storage;
- target ambiguity suppresses person-specific cues and risk conclusions;
- injury/safety text passes the prohibited-claim rules in ACC-04.

On validation failure:

1. Record category and model usage.
2. Allow at most one bounded repair call using only the invalid JSON and validation errors, with no video resend.
3. If still invalid, mark the item failed/abstained. Do not regex-extract a plausible substring or coerce timestamps to zero.

### Compatibility rendering

Keep public response fields initially by rendering validated DTOs into the current Markdown/text format. Persist the structured JSON in additive columns so future UI can consume it directly. Do not require a simultaneous app rewrite.

Remove regex/fenced-block parsers only after both paths have compatibility tests.

### Tests

- valid schema payload for every call type;
- malformed JSON, missing required field, unknown enum;
- NaN/negative/reversed/out-of-range timestamps;
- target ambiguous with a coaching cue is rejected/suppressed;
- scores outside 0–100;
- duplicate highlights/evidence;
- repair succeeds once and never loops;
- rendering old public output from validated DTOs;
- transport body asserts `application/json` and the correct response schema.

## PR 3B — Profile, target, and real-time context safety

Work items: ACC-02, ACC-03

### ACC-02 — Remove fabricated profile defaults

`lookupProfileString` in `api/internal/worker/worker.go` currently returns a fictional birth year, gender, height, weight, and other attributes when data is absent. Delete that behavior.

Required formatter behavior:

- Include only fields actually present in `db.Profile`.
- Compute age only when sufficient birth-date data exists; otherwise omit age.
- Never replace a missing value with an average or invented person.
- If every optional field is absent, return a neutral sentence equivalent to “profile attributes not provided.”
- Fitness-level policy may use the persisted fitness level, but do not infer body mechanics from it.
- Add table-driven tests for complete, partial, and empty profiles.

Profile context is a weak aid, not an identity guarantee. Do not describe it as proof of which person is the user.

### ACC-03 — Target attribution and abstention

Update chunk, index, segment, injury, and highlight prompts/contracts:

- Require `target_state` and confidence.
- If more than one plausible athlete is visible and the target cannot be distinguished, return `ambiguous` and no person-specific coaching.
- Background-person motion must not count as target evidence.
- A target leaving/re-entering frame creates not-visible ranges.
- User movement selections may narrow candidates but cannot resolve person identity.
- Do not introduce face recognition or biometric identity tracking as part of this change.

Evaluate a future non-biometric reference-frame/person-tracking feature separately and obtain privacy/product approval before storing a target crop or embedding.

### Real-time context corrections

- Add `wodDescription?: string` to `ProcessWorkoutVideoOptions`, propagate it through `processWorkoutChunk` and `notifyChunkUploadComplete` in `features/wod/api.ts`, and pass it from `app/workout/visionTestPage.tsx`; the backend DTO already supports it.
- Change wording in `buildChunkAnalysisPrompt` and `buildWODContext` from “confirmed movements” to “user-provided candidate movements.”
- Do not claim fatigue is increasing unless the call receives earlier validated signals/elapsed context. Initially prohibit trend language when no prior evidence is present.
- If adding prior context, query only a compact summary of the last few validated chunks; do not resend raw prose or video.
- Make the synthetic split path propagate the same WOD/movement/injury/context fields and parse the same evidence contract.

### Evaluation gate

On the multi-person/occlusion set:

- target-attribution error must not exceed the predeclared baseline bound;
- ambiguity should increase when evidence is insufficient rather than forcing a guess;
- report accuracy-at-coverage so a trivial “always abstain” implementation cannot pass.

## PR 3C — Safety and injury contract

Work item: ACC-04

### Files to inspect/change

- `api/internal/worker/injury_analysis.go`
- `api/internal/worker/prompts.go` or current prompt constants
- `api/internal/worker/video_analysis.go`
- Injury integration tests

### Allowed output

- Visible body/equipment position and motion.
- A clearly time-bounded mechanical observation.
- Conservative coaching language such as reducing load/range and seeking a qualified professional when appropriate.
- `not_assessable` when camera angle, occlusion, clothing, frame rate, or target ambiguity prevents a conclusion.

### Prohibited inference from video alone

- pain level or “pain-free” range;
- diagnosis or tissue pathology;
- medical clearance, prognosis, or return-to-play readiness;
- internal joint load stated as fact;
- causation between a visible mechanic and a known injury;
- reassurance that an injury is absent.

Known injuries are caution context only. They must not cause the model to invent a visible fault.

Apply the same inferability boundary to the general `AnalysisPrompt`: remove requests for habitual posture, suspected conditions, or other longitudinal/medical conclusions that cannot be established from the supplied segment. General coaching may describe only time-bounded visible mechanics unless corroborating session evidence is present.

### Injury evidence contract

Each item includes:

- absolute media start/end;
- canonical movement;
- visible observation;
- `assessability`;
- confidence;
- conservative action/cue;
- explicit `medical_claim=false` or a schema that cannot express diagnoses.

Select evidence by risk and uncertainty, not simply the longest intervals. Preserve at least one negative/control fixture for every positive category in evaluation.

Any prohibited claim is a critical validation failure and blocks rollout, even if average movement metrics improve.

## PR 3D — Canonical movements and segment construction

Work items: ACC-05, ACC-06

### ACC-05 — Canonical movement registry

Create one narrow registry/helper for:

- stable canonical ID;
- display name/localization key;
- accepted aliases and abbreviations;
- `unknown` fallback.

Reuse existing WOD/image typo mappings where possible rather than creating competing tables. Persist canonical IDs in new structured evidence; keep current display text for compatibility.

Canonicalization must be deterministic and unit-tested. A model-provided name outside the registry maps to `unknown`; it must not create a new ID at runtime.

### Preserve rest boundaries

Change `buildSegmentsFromChunks` so it does not discard rest/unknown rows before adjacency decisions.

Merge two movement intervals only when all are true:

1. same canonical movement ID;
2. no intervening rest, unknown-target, or failed-evidence chunk;
3. media gap is within the encoder tolerance;
4. merged duration does not exceed a configurable deep-segment cap.

Add an evaluation-only candidate cap `VIDEO_MAX_DEEP_SEGMENT_SECONDS=60` and measure it against the uncapped control. Do not promote 60 seconds merely because it is a plausible default. If a longer run is split, use non-overlapping media windows by default; add overlap only if evaluation demonstrates boundary loss and then deduplicate evidence.

### ACC-06 — Stratified triage

Replace duration-only, first-N fallback with deterministic coverage reservations:

1. Group assessable segments by canonical movement.
2. Reserve at least one segment per movement.
3. When multiple intervals exist for a movement, reserve early, middle, and late representatives as budget permits.
4. Prioritize validated visible-risk candidates without removing the only representative of another movement.
5. Fill remaining budget using coaching-value features from structured chunk evidence.
6. If text reasoning is still needed, send evidence summaries only; do not resend the full video at high resolution.
7. On model/parse failure, use the same deterministic stratified selector, never the first N timeline items.

If distinct observed movements exceed the hard budget, increase the budget up to the existing hard maximum where safe; otherwise record uncovered movements and produce PARTIAL rather than pretending completion.

Tests must cover `Snatch -> rest -> Snatch`, aliases, unknown rows, early/middle/late selection, risk reservation, and more movements than budget.

## PR 3E — Session synthesis, scoring, and highlights

Work items: ACC-07, ACC-08

### ACC-07 — Evidence-based final synthesis

After all deep calls finish:

1. Validate each `SegmentEvidence`.
2. Build a compact text/JSON payload containing every successful segment, coverage summary, WOD context, and known profile fields.
3. Make one text-only synthesis call. Do not attach the video again.
4. Require a structured `SessionAnalysis` response containing strengths, corrections, session summary, score dimensions with assessability, and highlight candidates referencing evidence IDs.
5. Render the public Korean/Markdown output from this validated object.

No single segment call receives responsibility for whole-session scoring.

#### Score correctness

- Compute the current session's absolute score before adding prior-session history.
- Validate every component in `[0,100]` and require sufficient evidence for consistency/intensity; otherwise mark the dimension not assessable rather than returning a confident zero.
- Recompute `overall` on the server from validated components and reject/overwrite a mismatching model total.
- For the existing standard formula use `form*0.5 + intensity*0.3 + consistency*0.2` with documented rounding.
- The current skill-WOD prompt says `form*0.7 + remaining 0.3` but does not define the split. Before implementation, confirm the product rule; recommended default is `form*0.7 + intensity*0.15 + consistency*0.15`.
- Canonicalize movement keys and validate movement sub-scores.

Historical comparison should consume the already validated current score plus prior scores. Prefer deterministic deltas/table rendering first. If generated comparison prose is desired, use a separate text-only call so history cannot bias the current absolute score.

### ACC-08 — Highlight validation and selective verification

Change highlight output to numeric media seconds with:

- type enum;
- canonical movement ID;
- reason;
- referenced evidence ID;
- confidence;
- per-item verification state.

Before clipping:

- reject/clamp outside `[0, mediaDuration]` according to a documented rule;
- require `start < end` and duration limits;
- deduplicate overlaps/near-identical reasons;
- require referenced evidence to exist and overlap;
- never convert invalid time text to zero.

Verification must return a result per highlight. Remove or downgrade failed claims instead of setting only one all-or-none session boolean. High-risk/low-confidence candidates can be verified; do not automatically resend the full video for every obvious evidence-backed item.

Fix the fallback storage path in `verify_highlights.go` so it uses `videos/{profileId}/{sessionId}/...` and returns a valid `gs://` URI. Add a test using a nontrivial profile ID to prevent regression.

## Phase 3 release gate

- [ ] Every model extraction uses a typed JSON contract and semantic validator.
- [ ] Invalid timestamps are rejected, not coerced.
- [ ] Missing profile fields are omitted and tested.
- [ ] Target ambiguity suppresses person-specific coaching.
- [ ] Safety output cannot express diagnosis/pain/readiness claims.
- [ ] Real-time and synthetic chunk paths share context/evidence behavior.
- [ ] Canonical movements and rest boundaries drive segment construction.
- [ ] Triage covers every observed movement or reports PARTIAL.
- [ ] Final synthesis sees every successful evidence object.
- [ ] Current absolute score is computed without historical bias and overall is server-validated.
- [ ] Highlights are evidence-linked, range-validated, and individually verified where needed.
- [ ] Candidate passes all predeclared Phase 2 safety/accuracy gates.
