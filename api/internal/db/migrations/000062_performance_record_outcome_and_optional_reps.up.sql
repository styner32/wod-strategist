ALTER TABLE performance_records
    ADD COLUMN outcome TEXT NOT NULL DEFAULT 'completed',
    ALTER COLUMN reps DROP NOT NULL,
    ALTER COLUMN reps_per_minute DROP NOT NULL,
    ALTER COLUMN rate_basis DROP NOT NULL,
    DROP CONSTRAINT IF EXISTS performance_records_reps_check,
    DROP CONSTRAINT IF EXISTS performance_records_reps_per_minute_check,
    DROP CONSTRAINT IF EXISTS performance_records_rate_basis_check;

-- The down migration preserves rest as its distinct empty-movement/null-count
-- shape. Restore it before validating completed records during a reapply.
UPDATE performance_records
SET outcome = 'rest'
WHERE movement = ''
    AND reps IS NULL
    AND reps_per_minute IS NULL
    AND rate_basis IS NULL
    AND load_value IS NULL
    AND load_unit IS NULL
    AND load_basis IS NULL
    AND load_kg IS NULL;

UPDATE performance_records
SET outcome = 'incomplete',
    reps = NULL,
    reps_per_minute = NULL,
    rate_basis = NULL,
    median_cycle_seconds = NULL
WHERE reps = 0;

ALTER TABLE performance_records
    ADD CONSTRAINT performance_records_outcome_check CHECK (
        outcome IN ('completed', 'incomplete', 'rest')
    ),
    ADD CONSTRAINT performance_records_outcome_fields_check CHECK (
        (
            outcome = 'completed'
            AND movement <> ''
            AND (
                (reps IS NULL AND reps_per_minute IS NULL AND rate_basis IS NULL)
                OR (
                    reps IS NOT NULL
                    AND reps >= 1
                    AND reps_per_minute IS NOT NULL
                    AND reps_per_minute > 0
                    AND reps_per_minute < 'Infinity'::float8
                    AND rate_basis IS NOT NULL
                    AND rate_basis IN ('actual_60_second_window', 'normalized_per_minute')
                )
            )
        )
        OR (
            outcome = 'incomplete'
            AND movement <> ''
            AND reps IS NULL
            AND reps_per_minute IS NULL
            AND rate_basis IS NULL
        )
        OR (
            outcome = 'rest'
            AND movement = ''
            AND reps IS NULL
            AND reps_per_minute IS NULL
            AND rate_basis IS NULL
            AND load_value IS NULL
            AND load_unit IS NULL
            AND load_basis IS NULL
            AND load_kg IS NULL
        )
    );
