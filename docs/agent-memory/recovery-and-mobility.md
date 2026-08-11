# Recovery Workouts, Mobility Observations, and Stretch Recommendations

This document outlines architectural patterns, scoring rubrics, prompt rules, and database schemas for Warm-up/Cool-down recovery sessions and mobility-based stretch recommendations.

## 1. Recovery Workout Scoring & Chunk Filtering

- **Workout Types:** `"warmup"`, `"cooldown"` (identified via `worker.IsRecoveryWorkoutType(wt)`).
- **Session Score Formula:**
  - Standard WOD: `overall = form×0.5 + intensity×0.3 + consistency×0.2`
  - Recovery Workout (warmup/cooldown): `overall = form×0.7 + consistency×0.3` (`intensity` is fixed to 0 and rendered as `"-"` in history context).
- **Chunk Deep Analysis Inclusion (`includeChunkInDeepAnalysis`):**
  - Standard WOD: drops low-motion chunks (`"walking"`, `"rest"`, `"rest_setup"`).
  - Recovery Workout: keeps `"rest_setup"` / low-motion activity chunks (essential for stretching/mobility poses), while still dropping purely ambient `"walking"`.

## 2. Mobility Observations (`mobility_observations`)

- **Schema Column:** `analysis_results.mobility_observations` (TEXT NOT NULL DEFAULT `'[]'`)
- **Prompt:** `worker.MobilityOutputPrompt` appended to all workout analysis prompts. Emits a ```mobility JSON block.
- **Closed Vocabulary:**
  - `joint`: `Neck`, `Shoulder`, `Elbow`, `Wrist`, `Upper Back`, `Lower Back`, `Hip`, `Hamstring`, `Knee`, `Ankle`
  - `observation`: `limited_hip_flexion`, `limited_hip_internal_rotation`, `limited_ankle_dorsiflexion`, `limited_overhead_flexion`, `limited_shoulder_external_rotation`, `limited_thoracic_extension`, `tight_hamstrings`, `tight_hip_flexors`, `limited_wrist_extension`, `general_stiffness`
  - `side`: `left`, `right`, `both`
- **Sanitization & Quality Gate (`sanitizeMobilityObservations`):**
  - Drops items with unknown joint or observation.
  - Drops items with `confidence < 0.4` or `assessable == false`.
  - Clamps `confidence` to `[0.0, 1.0]`.
  - Caps per-session observations to max 10.
- **Cross-Session Aggregation (`db.BuildMobilityHistory`):**
  - Queries last 20 completed non-archived sessions with mobility data.
  - Groups by `(joint, observation)`.
  - Sorts by `SessionCount` descending, then `LatestAt` recency.
  - Caps history output at 5 items.
  - Injected as Korean markdown table on the final segment of recovery workouts.

## 3. Stretch Recommendations (`stretch_recommendations`)

- **Schema Column:** `analysis_results.stretch_recommendations` (TEXT NOT NULL DEFAULT `'[]'`)
- **Closed Catalog (`stretchCatalog`):**
  `Pigeon Pose`, `Couch Stretch`, `Samson (Hip Flexor Lunge) Stretch`, `Cossack Squat Hold`, `Ankle Dorsiflexion Rock`, `Calf Stretch`, `Standing Forward Fold`, `Hamstring Floss`, `Child's Pose`, `Downward Dog`, `Thread the Needle`, `Cat-Cow`, `Thoracic Extension over Foam Roller`, `Doorway Pec Stretch`, `Wall Slide`, `Wrist Flexor/Extensor Stretch`, `Neck Lateral Stretch`.
- **Fail-Open Gate:**
  - If `len(history) == 0 && len(current) == 0`, `recommendStretches` immediately returns `"[]"` without calling Gemini (0 token cost).
- **Provisional Marking:**
  - `SessionCount >= 2`: `provisional: false`
  - Single session / first-time observation: `provisional: true` ("임시 (추이 관찰)")
- **Sanitization:**
  - Drops any recommendation whose `target_area` has no matching joint in current session observations or cross-session history.
  - Drops any stretch not in `stretchCatalog`.
  - Caps at max 3 recommendations per session.

## 4. UI Components

- **Web:** `web/src/history/SessionDetailPage.tsx` renders a "Stretch Recommendations" card displaying stretch name, target area badge, reason, duration hint, and caution warning.
- **Mobile:** `features/wod/ui/StretchRecommendationsCard.tsx` rendered inside `HistoryList.tsx` expanded session view.
