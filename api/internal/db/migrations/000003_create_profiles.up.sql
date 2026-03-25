CREATE TABLE IF NOT EXISTS profiles (
    id SERIAL PRIMARY KEY,
    birth_year INTEGER NOT NULL,
    birth_month INTEGER NOT NULL,
    birth_day INTEGER NOT NULL,
    gender TEXT NOT NULL,
    height_cm INTEGER NOT NULL,
    weight_kg DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

ALTER TABLE analysis_results ADD COLUMN IF NOT EXISTS profile_id INTEGER REFERENCES profiles(id);
CREATE INDEX IF NOT EXISTS idx_analysis_results_profile_id ON analysis_results(profile_id);

ALTER TABLE chunk_analysis_results ADD COLUMN IF NOT EXISTS profile_id INTEGER REFERENCES profiles(id);
CREATE INDEX IF NOT EXISTS idx_chunk_analysis_results_profile_id ON chunk_analysis_results(profile_id);
