ALTER TABLE chunk_analysis_results
    ADD COLUMN target_confidence DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    ADD COLUMN target_cues TEXT NOT NULL DEFAULT '{}';
