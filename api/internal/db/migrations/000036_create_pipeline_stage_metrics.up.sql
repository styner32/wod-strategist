CREATE TABLE pipeline_stage_metrics (
  id BIGSERIAL PRIMARY KEY,
  session_id TEXT NOT NULL,
  profile_id BIGINT NOT NULL DEFAULT 0,
  stage TEXT NOT NULL,        -- chunk_analysis | injury_analysis | verify_highlights
  variant TEXT NOT NULL,      -- legacy | optimized
  api_calls INTEGER NOT NULL DEFAULT 0,
  skipped_calls INTEGER NOT NULL DEFAULT 0,
  upload_bytes BIGINT NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_pipeline_stage_metrics_session ON pipeline_stage_metrics(session_id);
