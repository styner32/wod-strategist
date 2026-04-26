# Fitness Level System

## Database
- Column: `profiles.fitness_level` — `TEXT NOT NULL DEFAULT 'intermediate'`
- Valid values: `beginner`, `intermediate`, `advanced`
- Migration: `000016_add_fitness_level_to_profiles`

## Observed Signals (Chunk Benchmarking)
- Column: `chunk_analysis_results.observed_signals` — `TEXT NOT NULL DEFAULT '{}'`
- Contains JSON with estimated per-chunk workout metrics from Gemini
- Migration: `000017_add_observed_signals_to_chunks`

### Schema
```json
{
  "movement": "Deadlift",
  "set_duration_s": 10,
  "rep_count": 5,
  "avg_cadence_s": 2.0,
  "exertion_estimate": "high",
  "form_issues_seen": ["forward_torso_late_set"]
}
```
- Values are rough AI estimates, not accurate sensor data
- Only emitted when exercise is detected (NO_EXERCISE chunks have `"{}"`)

## Prompt Injection
- `buildChunkAnalysisPrompt()` in `chunk_analysis.go`:
  1. Base prompt (`ChunkAnalysisPrompt`) — general rules
  2. Level policy (`levelPolicyForFitnessLevel()`) — beginner/intermediate/advanced-specific coaching instructions
  3. Movements, injuries, profile context
  4. Observed signals prompt (`ObservedSignalsPrompt`) — structured JSON output requirement
- `lookupFitnessLevel(profileID)` — fetches from DB, defaults to `"intermediate"`

## Level Policy Summary
| Level        | Focus                | Pace cues? | Tone                  |
|-------------|---------------------|-----------|----------------------|
| beginner     | Form + symmetry only | No        | Warm, encouraging     |
| intermediate | Form + pace/intensity| Yes       | Direct, coaching      |
| advanced     | Performance-primary  | Yes       | Demanding, firm       |

## Frontend
- Profile store: `fitnessLevel: FitnessLevel` (`"beginner" | "intermediate" | "advanced"`)
- Profile editor: 3-button selector (same UI pattern as gender)
- Setup page banner: shows fitness level label
- i18n keys: `profileEdit.fitnessLevel`, `profileEdit.beginner`, `profileEdit.intermediate`, `profileEdit.advanced`

## API
- `fitness_level` field on `CreateProfileRequest`, `UpdateProfileRequest`, `ProfileResponse`
- Validation: `oneof=beginner intermediate advanced` (omitempty on create → defaults to `"intermediate"`)
