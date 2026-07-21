ALTER TABLE sessions
    ADD COLUMN movement_hints JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT sessions_movement_hints_array_check
        CHECK (jsonb_typeof(movement_hints) = 'array');
