ALTER TABLE analysis_results ADD COLUMN workout_progress JSONB DEFAULT NULL;
ALTER TABLE session_reanalysis_runs ADD COLUMN workout_progress JSONB DEFAULT NULL;

CREATE TABLE performance_records (
    id BIGSERIAL PRIMARY KEY,
    profile_id BIGINT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    analysis_id BIGINT NOT NULL REFERENCES analysis_results(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    interval_id TEXT NOT NULL,
    source_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('confirmed', 'needs_confirmation')),
    movement TEXT NOT NULL,
    start_secs DOUBLE PRECISION NOT NULL CHECK (start_secs >= 0 AND start_secs < 'Infinity'::float8),
    end_secs DOUBLE PRECISION NOT NULL CHECK (end_secs > start_secs AND end_secs < 'Infinity'::float8),
    elapsed_seconds DOUBLE PRECISION NOT NULL CHECK (elapsed_seconds > 0 AND elapsed_seconds < 'Infinity'::float8),
    reps INTEGER NOT NULL CHECK (reps > 0),
    load_value DOUBLE PRECISION,
    load_unit TEXT,
    load_basis TEXT,
    load_kg DOUBLE PRECISION,
    measurement TEXT NOT NULL CHECK (measurement IN ('set', 'window')),
    technique TEXT NOT NULL CHECK (technique IN ('unspecified', 'singles', 'touch_and_go')),
    workout_prescription TEXT NOT NULL DEFAULT '',
    reps_per_minute DOUBLE PRECISION NOT NULL CHECK (reps_per_minute > 0 AND reps_per_minute < 'Infinity'::float8),
    rate_basis TEXT NOT NULL CHECK (rate_basis IN ('actual_60_second_window', 'normalized_per_minute')),
    median_cycle_seconds DOUBLE PRECISION CHECK (median_cycle_seconds IS NULL OR (median_cycle_seconds > 0 AND median_cycle_seconds < 'Infinity'::float8)),
    original_candidate JSONB NOT NULL,
    performed_at TIMESTAMPTZ,
    performed_at_source TEXT,
    confirmed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT performance_records_load CHECK (
        (load_value IS NULL AND load_unit IS NULL AND load_basis IS NULL AND load_kg IS NULL)
        OR (load_value IS NOT NULL AND load_value > 0 AND load_value < 'Infinity'::float8 AND load_unit IS NOT NULL AND load_unit IN ('lb','kg') AND load_basis IS NOT NULL AND load_basis IN ('total','per_implement') AND load_kg IS NOT NULL AND load_kg >= 0 AND load_kg < 'Infinity'::float8)
    ),
    UNIQUE (profile_id, session_id, interval_id)
);
CREATE INDEX performance_records_profile_movement_time ON performance_records (profile_id, movement, performed_at DESC, id DESC) WHERE status = 'confirmed';
