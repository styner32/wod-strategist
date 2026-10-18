DROP TABLE IF EXISTS performance_records;
ALTER TABLE session_reanalysis_runs DROP COLUMN IF EXISTS workout_progress;
ALTER TABLE analysis_results DROP COLUMN IF EXISTS workout_progress;
