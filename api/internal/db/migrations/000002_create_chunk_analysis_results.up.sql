CREATE TABLE IF NOT EXISTS chunk_analysis_results (
    id SERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    status TEXT,
    output TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_chunk_analysis_results_session_id ON chunk_analysis_results(session_id);
