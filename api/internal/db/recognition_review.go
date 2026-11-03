package db

import "time"

const (
	RecognitionReviewStatusQueued    = "QUEUED"
	RecognitionReviewStatusRunning   = "RUNNING"
	RecognitionReviewStatusCompleted = "COMPLETED"
	RecognitionReviewStatusPartial   = "PARTIAL"
	RecognitionReviewStatusFailed    = "FAILED"
)

// RecognitionReviewRun stores an immutable evidence snapshot and append-only
// diagnosis result. Migration 000060 owns its constraints and indexes.
type RecognitionReviewRun struct {
	ID                    uint         `gorm:"primaryKey" json:"id"`
	SessionID             string       `json:"session_id"`
	ProfileID             uint         `json:"-"`
	ClientRequestID       string       `json:"-"`
	RequestHash           string       `json:"-"`
	TaskID                string       `json:"task_id,omitempty"`
	Status                string       `json:"status"`
	SourceVersion         string       `json:"source_version"`
	EvaluationFingerprint string       `json:"evaluation_fingerprint"`
	SourceGCSURI          string       `json:"-"`
	SourceObject          string       `json:"-"`
	SourceGeneration      int64        `json:"-"`
	RecordIDs             JSONDocument `json:"-"`
	StartSecs             float64      `json:"start_secs"`
	EndSecs               float64      `json:"end_secs"`
	Snapshot              JSONDocument `json:"-"`
	Batches               JSONDocument `json:"-"`
	Model                 string       `json:"model"`
	PromptVersion         string       `json:"prompt_version"`
	PromptHash            string       `json:"prompt_hash"`
	SchemaVersion         string       `json:"schema_version"`
	InputTokens           *int64       `json:"input_tokens,omitempty"`
	OutputTokens          *int64       `json:"output_tokens,omitempty"`
	TotalTokens           *int64       `json:"total_tokens,omitempty"`
	DurationMs            int64        `json:"duration_ms"`
	SafeError             string       `json:"error,omitempty"`
	StartedAt             *time.Time   `json:"started_at,omitempty"`
	CompletedAt           *time.Time   `json:"completed_at,omitempty"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
}

func (RecognitionReviewRun) TableName() string { return "recognition_review_runs" }
