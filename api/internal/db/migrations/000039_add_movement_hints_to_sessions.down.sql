ALTER TABLE sessions
    DROP CONSTRAINT IF EXISTS sessions_movement_hints_array_check,
    DROP COLUMN IF EXISTS movement_hints;
