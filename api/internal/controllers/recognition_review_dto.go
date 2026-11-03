package controllers

import (
	"time"

	"github.com/wod-strategist/api/internal/recognitionreview"
)

type CreateRecognitionReviewRequest struct {
	RecordIDs             []uint  `json:"record_ids"`
	StartSecs             float64 `json:"start_secs"`
	EndSecs               float64 `json:"end_secs"`
	SourceVersion         string  `json:"source_version"`
	EvaluationFingerprint string  `json:"evaluation_fingerprint"`
	ClientRequestID       string  `json:"client_request_id"`
}

type CreateRecognitionReviewResponse struct {
	RunID  uint   `json:"run_id"`
	Status string `json:"status"`
}

// RecognitionReviewBatchResponse deliberately has no provider upload identifiers.
// The persisted worker batch additionally journals its own temporary upload.
type RecognitionReviewBatchResponse struct {
	ID            int                         `json:"id"`
	RecordID      uint                        `json:"record_id"`
	StartSecs     float64                     `json:"start_secs"`
	EndSecs       float64                     `json:"end_secs"`
	CoreStartSecs float64                     `json:"core_start_secs"`
	CoreEndSecs   float64                     `json:"core_end_secs"`
	Status        string                      `json:"status"`
	Findings      []recognitionreview.Finding `json:"findings"`
	Error         string                      `json:"error,omitempty"`
	InputTokens   *int64                      `json:"input_tokens,omitempty"`
	OutputTokens  *int64                      `json:"output_tokens,omitempty"`
	TotalTokens   *int64                      `json:"total_tokens,omitempty"`
}

type RecognitionReviewRunResponse struct {
	ID                    uint                             `json:"id"`
	Status                string                           `json:"status"`
	SourceVersion         string                           `json:"source_version"`
	EvaluationFingerprint string                           `json:"evaluation_fingerprint"`
	RecordIDs             []uint                           `json:"record_ids"`
	StartSecs             float64                          `json:"start_secs"`
	EndSecs               float64                          `json:"end_secs"`
	Model                 string                           `json:"model"`
	PromptVersion         string                           `json:"prompt_version"`
	SchemaVersion         string                           `json:"schema_version"`
	Batches               []RecognitionReviewBatchResponse `json:"batches"`
	Evaluation            *recognitionreview.Evaluation    `json:"evaluation,omitempty"`
	Outdated              bool                             `json:"outdated"`
	InputTokens           *int64                           `json:"input_tokens,omitempty"`
	OutputTokens          *int64                           `json:"output_tokens,omitempty"`
	TotalTokens           *int64                           `json:"total_tokens,omitempty"`
	DurationMs            int64                            `json:"duration_ms"`
	Error                 string                           `json:"error,omitempty"`
	CreatedAt             time.Time                        `json:"created_at"`
	UpdatedAt             time.Time                        `json:"updated_at"`
}

type ListRecognitionReviewsResponse struct {
	Runs        []RecognitionReviewRunResponse `json:"runs"`
	ActiveRunID *uint                          `json:"active_run_id,omitempty"`
}
