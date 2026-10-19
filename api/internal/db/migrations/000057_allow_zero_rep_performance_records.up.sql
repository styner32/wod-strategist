ALTER TABLE performance_records
    DROP CONSTRAINT IF EXISTS performance_records_reps_check,
    DROP CONSTRAINT IF EXISTS performance_records_reps_per_minute_check,
    ADD CONSTRAINT performance_records_reps_check CHECK (reps >= 0),
    ADD CONSTRAINT performance_records_reps_per_minute_check CHECK (reps_per_minute >= 0 AND reps_per_minute < 'Infinity'::float8);
