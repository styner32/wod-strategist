package db

import "time"

const (
	SessionReanalysisStatusQueued             = "QUEUED"
	SessionReanalysisStatusRunning            = "RUNNING"
	SessionReanalysisStatusCompleted          = "COMPLETED"
	SessionReanalysisStatusFailed             = "FAILED"
	SessionReanalysisStatusVideoUnavailable   = "VIDEO_UNAVAILABLE"
	SessionReanalysisStatusContextUnavailable = "CONTEXT_UNAVAILABLE"
)

// SessionReanalysisRun is an append-only whole-workout debug candidate. It is
// deliberately separate from analysis_results so a rerun cannot replace the
// immutable production analysis or trigger production-derived output.
type SessionReanalysisRun struct {
	ID                       uint         `gorm:"primaryKey" json:"id"`
	SessionID                string       `json:"session_id"`
	ProfileID                uint         `json:"-"`
	ClientRequestID          string       `json:"-"`
	TaskID                   string       `json:"task_id,omitempty"`
	Status                   string       `json:"status"`
	SourceGCSURI             string       `json:"-"`
	SourceContextSnapshot    JSONDocument `gorm:"type:jsonb" json:"-"`
	OriginalAnalysisSnapshot JSONDocument `gorm:"type:jsonb" json:"-"`
	Output                   string       `json:"-"`
	HighlightSegments        string       `json:"-"`
	SessionScore             string       `json:"-"`
	WorkoutType              string       `json:"-"`
	Model                    string       `json:"model,omitempty"`
	PromptVersion            string       `json:"prompt_version,omitempty"`
	PromptHash               string       `json:"prompt_hash,omitempty"`
	SchemaVersion            string       `json:"schema_version,omitempty"`
	PromptTokens             int32        `json:"-"`
	CandidateTokens          int32        `json:"-"`
	TotalTokens              int32        `json:"-"`
	DurationMs               int64        `json:"duration_ms,omitempty"`
	SafeError                string       `json:"error,omitempty"`
	GeminiFileURI            string       `json:"-"`
	GeminiFileName           string       `json:"-"`
	GeminiMIMEType           string       `json:"-"`
	GeminiFileExpiresAt      *time.Time   `json:"-"`
	StartedAt                *time.Time   `json:"started_at,omitempty"`
	CompletedAt              *time.Time   `json:"completed_at,omitempty"`
	CreatedAt                time.Time    `json:"created_at"`
	UpdatedAt                time.Time    `json:"updated_at"`
}

func (SessionReanalysisRun) TableName() string {
	return "session_reanalysis_runs"
}
