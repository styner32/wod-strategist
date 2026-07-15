package controllers

import "time"

type CreateChunkReanalysisRequest struct {
	ClientRequestID string `json:"client_request_id" binding:"required"`
}

type CreateChunkReanalysisResponse struct {
	RunID  uint   `json:"run_id"`
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

type ChunkReanalysisCandidateResponse struct {
	ExerciseType    string         `json:"exercise_type"`
	Output          string         `json:"output"`
	ObservedSignals map[string]any `json:"observed_signals"`
}

type ChunkReanalysisTokenUsageResponse struct {
	PromptTokens    int32 `json:"prompt_tokens"`
	CandidateTokens int32 `json:"candidate_tokens"`
	TotalTokens     int32 `json:"total_tokens"`
}

type ChunkReanalysisRunResponse struct {
	ID             uint                               `json:"id"`
	SessionID      string                             `json:"session_id"`
	ChunkID        uint                               `json:"chunk_id"`
	TaskID         string                             `json:"task_id,omitempty"`
	Status         string                             `json:"status"`
	SourceKind     string                             `json:"source_kind,omitempty"`
	MediaStartSecs *float64                           `json:"media_start_secs,omitempty"`
	MediaEndSecs   *float64                           `json:"media_end_secs,omitempty"`
	Model          string                             `json:"model,omitempty"`
	PromptVersion  string                             `json:"prompt_version,omitempty"`
	PromptHash     string                             `json:"prompt_hash,omitempty"`
	SchemaVersion  string                             `json:"schema_version,omitempty"`
	RawOutput      string                             `json:"raw_output,omitempty"`
	Candidate      *ChunkReanalysisCandidateResponse  `json:"candidate,omitempty"`
	TokenUsage     *ChunkReanalysisTokenUsageResponse `json:"token_usage,omitempty"`
	DurationMs     int64                              `json:"duration_ms,omitempty"`
	Error          string                             `json:"error,omitempty"`
	CreatedAt      time.Time                          `json:"created_at"`
	StartedAt      *time.Time                         `json:"started_at,omitempty"`
	CompletedAt    *time.Time                         `json:"completed_at,omitempty"`
}

type ListChunkReanalysesResponse struct {
	Runs []ChunkReanalysisRunResponse `json:"runs"`
}

type ChunkPlayURLResponse struct {
	SessionID      string   `json:"session_id"`
	ChunkID        uint     `json:"chunk_id"`
	PlayURL        string   `json:"play_url"`
	SourceKind     string   `json:"source_kind"`
	MediaStartSecs *float64 `json:"media_start_secs,omitempty"`
	MediaEndSecs   *float64 `json:"media_end_secs,omitempty"`
	ExpiresAt      string   `json:"expires_at"`
}
