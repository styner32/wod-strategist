-- Drop new outcome constraints before restoring legacy zero-rep rows on rollback.
ALTER TABLE performance_records
    DROP CONSTRAINT IF EXISTS performance_records_outcome_fields_check,
    DROP CONSTRAINT IF EXISTS performance_records_outcome_check;

UPDATE performance_records
SET reps = 0,
    reps_per_minute = 0,
    rate_basis = 'normalized_per_minute'
WHERE outcome = 'incomplete' AND reps IS NULL;

ALTER TABLE performance_records
    DROP COLUMN IF EXISTS outcome,
    ADD CONSTRAINT performance_records_reps_check CHECK (reps >= 0) NOT VALID,
    ADD CONSTRAINT performance_records_reps_per_minute_check CHECK (reps_per_minute >= 0 AND reps_per_minute < 'Infinity'::float8) NOT VALID,
    ADD CONSTRAINT performance_records_rate_basis_check CHECK (rate_basis IN ('actual_60_second_window', 'normalized_per_minute')) NOT VALID;
