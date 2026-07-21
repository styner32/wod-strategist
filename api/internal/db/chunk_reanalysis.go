package db

import "time"

const (
	ChunkReanalysisStatusQueued              = "QUEUED"
	ChunkReanalysisStatusRunning             = "RUNNING"
	ChunkReanalysisStatusCompleted           = "COMPLETED"
	ChunkReanalysisStatusFailed              = "FAILED"
	ChunkReanalysisStatusVideoUnavailable    = "VIDEO_UNAVAILABLE"
	ChunkReanalysisStatusIntervalUnavailable = "INTERVAL_UNAVAILABLE"

	ChunkReanalysisSourceSessionVideo = "session_video"
	ChunkReanalysisSourceChunk        = "chunk"
)

// ChunkReanalysisRun is an immutable debug candidate for one production chunk.
// It deliberately lives outside chunk_analysis_results so legacy mobile polling
// can never mistake a debugging run for a new production analysis.
type ChunkReanalysisRun struct {
	ID                         uint         `gorm:"primaryKey" json:"id"`
	SessionID                  string       `json:"session_id"`
	ProfileID                  uint         `json:"profile_id"`
	ChunkAnalysisResultID      uint         `json:"chunk_id"`
	ClientRequestID            string       `json:"-"`
	TaskID                     string       `json:"task_id,omitempty"`
	Status                     string       `json:"status"`
	SourceKind                 string       `json:"source_kind,omitempty"`
	SourceGCSURI               string       `json:"-"`
	SourceContextSnapshot      JSONDocument `gorm:"type:jsonb" json:"-"`
	OriginalPredictionSnapshot JSONDocument `gorm:"type:jsonb" json:"-"`
	MediaStartSecs             *float64     `json:"media_start_secs,omitempty"`
	MediaEndSecs               *float64     `json:"media_end_secs,omitempty"`
	Model                      string       `json:"model,omitempty"`
	PromptVersion              string       `json:"prompt_version,omitempty"`
	PromptHash                 string       `json:"prompt_hash,omitempty"`
	SchemaVersion              string       `json:"schema_version,omitempty"`
	RawOutput                  string       `json:"raw_output,omitempty"`
	StructuredCandidate        JSONDocument `gorm:"type:jsonb" json:"-"`
	PromptTokens               int32        `json:"-"`
	CandidateTokens            int32        `json:"-"`
	TotalTokens                int32        `json:"-"`
	DurationMs                 int64        `json:"duration_ms,omitempty"`
	SafeError                  string       `json:"error,omitempty"`
	GeminiFileURI              string       `json:"-"`
	GeminiFileName             string       `json:"-"`
	GeminiMIMEType             string       `json:"-"`
	GeminiFileExpiresAt        *time.Time   `json:"-"`
	StartedAt                  *time.Time   `json:"started_at,omitempty"`
	CompletedAt                *time.Time   `json:"completed_at,omitempty"`
	CreatedAt                  time.Time    `json:"created_at"`
	UpdatedAt                  time.Time    `json:"updated_at"`
}

func (ChunkReanalysisRun) TableName() string {
	return "chunk_reanalysis_runs"
}
