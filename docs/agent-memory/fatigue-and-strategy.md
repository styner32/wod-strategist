# Fatigue Tracking & Pre-WOD Strategy System

This document outlines the architecture, mathematical model, database schema, and API contracts for the multi-muscle group fatigue tracking and Pre-WOD Strategy coaching system.

## 1. 6 Functional Muscle Groups & Movement Patterns

The system divides CrossFit and functional fitness movements into 6 core anatomical & kinematic categories:

| Group Key | Korean Name | Key Movements | Biological Half-Life ($T_{1/2}$) |
|---|---|---|---|
| `shoulders_push` | 어깨 / 상체 밀기 | Overhead Press, Push Jerk, HSPU, Thruster (Upper), Bench, Wall Walk | 30 hours |
| `upper_pull_grip` | 등·광배 / 상체 당기기·악력 | Pull-up, Muscle-up, Rope Climb, Row, Heavy Barbell Hold | 28 hours |
| `posterior_chain` | 허리 / 후면사슬 | Deadlift, Clean/Snatch Pull, KB Swing, Good Morning | 42 hours (Slowest / CNS load) |
| `quads_squat` | 하체 / 스쿼트 | Back/Front Squat, Wall-ball, Box Jump, Lunge | 36 hours |
| `core_midline` | 코어 / 미드라인 | Toes-to-Bar, Sit-up, GHD Sit-up, L-sit | 24 hours |
| `cardio_metabolic` | 심폐 / 전신 유산소 | Burpee, Double Under, Running, Echo Bike | 18 hours (Fastest recovery) |

## 2. Load Calculation & Decay Model

### (1) Session Load Formulation
$$\text{Load}_{\text{muscle}} = \sum_{\text{movements}} \left( \min\left(50, \frac{\text{Volume}}{20} \times 30\right) \times \text{Weight}_{\text{muscle}} \times \text{IntensityFactor} \times \text{FatigueMultiplier} \right)$$
- `IntensityFactor`: `max(0.5, min(1.5, SessionScore.Intensity / 70.0))`
- `FatigueMultiplier`: `1.0 + min(0.5, VisualFatigueCount * 0.15)` when `fatigue_visually_established` is observed.
- Heart rate bonus: `min(20.0, (HeartRateBPM - 140) * 0.5)` added to `cardio_metabolic`.
- Capped at 100.0 per group per session.

### (2) Exponential Decay
$$\text{ResidualFatigue}(t) = \sum_{s \in \text{Sessions}} \text{Load}_s \times 2^{-\frac{\Delta t}{T_{1/2}}}$$
- Readiness States:
  - $0 \sim 25\%$: `fresh` (신선 / 최적 수행 가능)
  - $26 \sim 50\%$: `moderate` (보통)
  - $51 \sim 75\%$: `fatigued` (피로 주의 / 중량 감량 및 템포 조절 권장)
  - $76 \sim 100\%$: `exhausted` (극심한 피로 / 대체 동작 또는 적극적 회복 권장)

## 3. Storage & Calculation Strategy (Zero Schema Changes)

- **No DB Schema Changes**: `AnalysisResult` requires no new columns.
- **On-the-Fly Derivation**: When `/strategies/pre-wod-advice` is called, past completed session records (last 7 days) are retrieved and their 6-muscle loads are derived on-the-fly from `session_score.movements`, `session_score.intensity`, and movement weight catalog.
- **Real-Time Exponential Decay**: The biological half-life decay is applied at query time relative to `time.Now()`.

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
  - `target_rpe`: Recommended RPE and pacing strategy
  - `scaling_advice`: List of specific movement scaling/substitutions
  - `mobility_warmup`: 1-2 targeted pre-WOD mobility movements
  - `overall_summary`: Head coach executive briefing

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


