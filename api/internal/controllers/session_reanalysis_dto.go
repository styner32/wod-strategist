package controllers

import "time"

type CreateSessionReanalysisRequest struct {
	ClientRequestID string `json:"client_request_id"`
	AppearanceHints string `json:"appearance_hints,omitempty"`
	Model           string `json:"model,omitempty"`
}

type CreateSessionReanalysisResponse struct {
	RunID  uint   `json:"run_id"`
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

type SessionReanalysisCandidateResponse struct {
	Output            string `json:"output"`
	HighlightSegments any    `json:"highlight_segments"`
	SessionScore      string `json:"session_score"`
	WorkoutType       string `json:"workout_type"`
}

type SessionReanalysisRunResponse struct {
	ID            uint                                `json:"id"`
	SessionID     string                              `json:"session_id"`
	TaskID        string                              `json:"task_id,omitempty"`
	Status        string                              `json:"status"`
	Candidate     *SessionReanalysisCandidateResponse `json:"candidate,omitempty"`
	Model         string                              `json:"model,omitempty"`
	PromptVersion string                              `json:"prompt_version,omitempty"`
	PromptHash    string                              `json:"prompt_hash,omitempty"`
	SchemaVersion string                              `json:"schema_version,omitempty"`
	InputTokens   int32                               `json:"input_tokens,omitempty"`
	OutputTokens  int32                               `json:"output_tokens,omitempty"`
	TokenUsage    *ChunkReanalysisTokenUsageResponse  `json:"token_usage,omitempty"`
	DurationMs    int64                               `json:"duration_ms,omitempty"`
	Error         string                              `json:"error,omitempty"`
	CreatedAt     time.Time                           `json:"created_at"`
	StartedAt     *time.Time                          `json:"started_at,omitempty"`
	CompletedAt   *time.Time                          `json:"completed_at,omitempty"`
	UpdatedAt     time.Time                           `json:"updated_at"`
}

type SessionReanalysisReadinessResponse struct {
	CanCreate          bool   `json:"can_create"`
	ActiveChunkRuns    int64  `json:"active_chunk_runs"`
	VideoAvailable     bool   `json:"video_available"`
	ActiveSessionRunID *uint  `json:"active_session_run_id,omitempty"`
	BlockedReason      string `json:"blocked_reason,omitempty"`
}

type ListSessionReanalysesResponse struct {
	Runs      []SessionReanalysisRunResponse     `json:"runs"`
	Readiness SessionReanalysisReadinessResponse `json:"readiness"`
}
