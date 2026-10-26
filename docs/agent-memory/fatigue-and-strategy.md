# Fatigue Tracking & Pre-WOD Strategy System

This document outlines the architecture, mathematical model, database schema, and API contracts for the multi-muscle group fatigue tracking and Pre-WOD Strategy coaching system.

> Planned change, not implemented (2026-09-07): [Single-table sensor analysis and workout load design](../polar-sensor-analysis-design.md) specifies optional sensor fields in `analysis_results`, frozen calculation inputs, canonical workout timestamps, and evidence-aware API responses. The descriptions below remain the current implementation baseline; the linked design must not be reported as deployed behavior.

## 1. 6 Functional Muscle Groups & Movement Patterns

The system divides CrossFit and functional fitness movements into 6 core anatomical & kinematic categories:

| Group Key | Korean Name | Key Movements | Configured decay half-life ($T_{1/2}$) |
|---|---|---|---|
| `shoulders_push` | 어깨 / 상체 밀기 | Overhead Press, Push Jerk, HSPU, Thruster (Upper), Bench, Wall Walk | 30 hours |
| `upper_pull_grip` | 등·광배 / 상체 당기기·악력 | Pull-up, Muscle-up, Rope Climb, Row, Heavy Barbell Hold | 28 hours |
| `posterior_chain` | 허리 / 후면사슬 | Deadlift, Clean/Snatch Pull, KB Swing, Good Morning | 42 hours (slowest configured decay) |
| `quads_squat` | 하체 / 스쿼트 | Back/Front Squat, Wall-ball, Box Jump, Lunge | 36 hours |
| `core_midline` | 코어 / 미드라인 | Toes-to-Bar, Sit-up, GHD Sit-up, L-sit | 24 hours |
| `cardio_metabolic` | 심폐 / 전신 유산소 | Burpee, Double Under, Running, Echo Bike | 18 hours (fastest configured decay) |

## 2. Load Calculation & Decay Model

These are heuristic software scores and configured decay constants, not measured biological recovery or medical readiness. Source: `api/internal/fatigue/fatigue.go`. The visual-evidence contract remains separate: [movement-fatigue-evidence.md](movement-fatigue-evidence.md).

### (1) Session Load Formulation
$$\text{Load}_{\text{muscle}} = \sum_{\text{movements}} \left( \min\left(50, \frac{\text{Volume}}{20} \times 30\right) \times \text{Weight}_{\text{muscle}} \times \text{IntensityFactor} \times \text{FatigueMultiplier} \right)$$
- `IntensityFactor`: `max(0.5, min(1.5, SessionScore.Intensity / 70.0))` for positive intensity; otherwise `1.0`.
- `Volume`: sum of chunk `rep_count`, with `3` substituted for nonpositive/missing reps. If no chunk volume remains, use `20` for each scored movement; if no movements remain, return `30 × IntensityFactor` for every group. These are implementation fallbacks, not measured volume.
- `FatigueMultiplier`: `1.0 + min(0.5, VisualFatigueCount * 0.15)` when `fatigue_visually_established` is observed.
- Heart rate bonus: only when `HeartRateBPM > 140`, add `min(20.0, (HeartRateBPM - 140) * 0.5)` to `cardio_metabolic` (except the no-movement early return). This score adjustment does not establish a visual fatigue event.
- Capped at 100.0 per group per session.

### (2) Exponential Decay
$$\text{ResidualFatigue}(t) = \sum_{s \in \text{Sessions}} \text{Load}_s \times 2^{-\frac{\Delta t}{T_{1/2}}}$$
- Accumulated group scores are capped at 100 and rounded to integers before applying these states:
  - $0 \sim 25\%$: `fresh` (신선 / 최적 수행 가능)
  - $26 \sim 50\%$: `moderate` (보통)
  - $51 \sim 75\%$: `fatigued` (피로 주의 / 중량 감량 및 템포 조절 권장)
  - $76 \sim 100\%$: `exhausted` (극심한 피로 / 대체 동작 또는 적극적 회복 권장)

## 3. Storage & Calculation Strategy (Zero Schema Changes)

- **No DB Schema Changes**: `AnalysisResult` requires no new columns.
- **On-the-Fly Derivation**: When `/strategies/pre-wod-advice` is called, past completed, non-archived session records (last 7 days, at most 20) are retrieved and their 6-muscle loads are derived on-the-fly from `session_score.movements`, `session_score.intensity`, and movement weight catalog.
- **Current callers**: both `GetPreWODAdvice` and `populateSessionFatigue` pass `ComputeSessionMuscleLoads(sessionScore, nil, 0)`. They currently use the score/movement fallbacks, not chunk reps, visual-fatigue counts, or BPM bonuses. The complete formula above describes helper capability, not inputs already connected to those endpoints.
- **Real-Time Exponential Decay**: The configured half-life decay is applied at query time relative to `time.Now()`.

## 4. API Endpoint

- **Route**: `POST /api/v1/strategies/pre-wod-advice` (JWT Protected)
- **Request Body**:
  ```json
  {
    "profile_id": 1,
    "wod_description": "21-15-9 Thrusters (95lb), Pull-ups",
    "movements": ["Thruster", "Pull-up"]
  }
  ```
- **Response**:
  - `muscle_readiness`: 6 muscle group items with scores and coaching notes
  - `target_rpe`: Recommended RPE and pacing strategy (withheld as `null` unless `evidence_status == "complete"`)
  - `effort_reflection`: Optional recent-WOD feedback summary (`applied`, `rated_count`, `sensor_count`, `total_recent`, `reason`, `recent_workouts`) — strictly `null`/omitted unless `evidence_status == "complete"` and at least 1 recent main WOD exists in `[asOf-28d, asOf]`
  - `scaling_advice`: List of specific movement scaling/substitutions
  - `mobility_warmup`: 1-2 targeted pre-WOD mobility movements
  - `overall_summary`: Head coach executive briefing

### Recent 3 WOD Feedback & Hybrid Sensor Rules (`GetPreWODAdvice` / `FinalizePreWODAdvice`)
- **Query scope**: Inside the same `REPEATABLE READ READ ONLY` transaction, queries up to 3 completed non-archived main WODs (`analysis_type = 'wod'`, `COALESCE(s.workout_type, 'wod') = 'wod'`, `workout_at_source IN ('session_ulid', 'legacy_local_time')`) in `[asOf-28d, asOf]`.
- **No guessing or scale conflation**:
  - `recent_workouts[i].effort` comes strictly from manual `sessions.effort` (`1..10`) when `workout_type == "wod" && wod_evaluation_version == 1`. Unrated workouts stay `nil` (never inferred from HR/sensor or mapped 1:1 to RIR).
  - `recent_workouts[i].duration_seconds` comes strictly from `wodeval.DurationSeconds(score.WODStart)` (`evaluation_version == 1` and `ValidWindow`); unresolved windows stay `nil`.
  - `recent_workouts[i].hybrid_sensor` comes only from verified fresh `CalculationVersion >= 3` `sensor_summary` rows with `hybrid_intensity != nil`, `data_coverage >= 0.50`, and `valid_hybrid_seconds >= 60`.
- **Safety cap (`OverallFatigueScore >= 60`)**: `FinalizePreWODAdvice` forces `TargetRPE.Score <= 6` whenever `OverallFatigueScore >= 60` so low manual `Effort` or low `hybrid_intensity` (e.g., slow heavy barbell work where external weight is unmeasured) can never assert biological recovery or override high 6-muscle fatigue. RIR-mapping and recovery-guarantee claims in `effort_reflection_reason` fall back to `BuildDeterministicEffortReflectionReason`.
- **One classification for adjustment and explanation**: `fatigue.ClassifyRecentEffort(recent)` picks the kind in precedence order `high_effort` (any Effort >= 8) > `low_effort` (any <= 5) > `rated_effort` (only 6–7) > `high_sensor` (no Effort; rounded hybrid >= 80) > `sensor`, and anchors on the most recent workout of that kind. `buildDeterministicFallbackAdvice` lowers RPE (8/9 → 7/8) for `high_effort` and `high_sensor`, and both its pacing text and `BuildDeterministicEffortReflectionReason` cite that anchor's values only (a sensor note is the anchor's own score). `FormatRecentWorkoutAnchor(w, index)` names it (`직전 WOD` for index 0, `N회 전 WOD` otherwise, or `최근 <movements> WOD`); text built on it attaches only `에서`/`의`, never final-syllable particles like `을/를`.
- **Grounded model reasons**: a model-written `effort_reflection_reason` is kept only if every `Effort N`, `N/100` score and duration (`N분 N초`, `N분`, `N초`) it cites matches the recent workouts (Effort, rounded hybrid/heart/motion ×100, `formatDurationKO`) or readiness (overall/muscle fatigue). Otherwise the deterministic reason replaces it.
- **RPE label**: after clamping to 1..10 and the fatigue cap, `syncRPELabel` rewrites an `RPE N` (or `N~M` range) in `target_rpe.label` that no longer contains the final score.

## 5. UI Integration

- Component: `features/wod/ui/PreWodStrategyCard.tsx`
- Rendered in Step 2 (Confirm) of `app/workout/setup.tsx`.
- Automatically fetches advice upon entering confirm step when profile and WOD description / movements are present.

## 6. Per-Workout Fatigue in Workout History

- **Endpoint**: `GET /api/v1/history?profile_id={id}`
- **Field**: `AnalysisResult.SessionFatigue` (`gorm:"-" json:"session_fatigue,omitempty"`)
- **Computation**: In `api/internal/controllers/highlight_response.go`, `populateSessionFatigue` derives `session_fatigue` for completed sessions from `SessionScore` using `fatigue.ComputeSessionMuscleLoads`.
- **UI Components**:
  - `features/wod/ui/WorkoutFatigueCard.tsx`: Renders overall workout load badge (color-coded) and 6 muscle group progress bars with top loaded muscle summary.
  - `features/wod/ui/HistoryList.tsx`: Displays compact fatigue pill next to date in collapsed card view, and expands to full `WorkoutFatigueCard` in detailed report view.
