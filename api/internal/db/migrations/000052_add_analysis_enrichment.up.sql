ALTER TABLE analysis_results ADD COLUMN analysis_summary JSONB NOT NULL DEFAULT '{}', ADD COLUMN agentic_highlight_analysis JSONB NOT NULL DEFAULT '{}';
