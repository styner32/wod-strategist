// Package recognitionreview compares immutable observations with manual confirmations.
// Diagnostic findings never become workout observations or benchmark facts.
package recognitionreview

import (
	"encoding/json"
	"time"

	"github.com/wod-strategist/api/internal/workoutprogress"
)

const (
	PromptVersion = "recognition-diagnosis-v1"
	SchemaVersion = "recognition-diagnosis-v1"
)

type MovementCount struct {
	Movement string `json:"movement"`
	Reps     int    `json:"reps"`
}

type RecordEvaluation struct {
	Record               workoutprogress.Record `json:"record"`
	PredictedMovements   []MovementCount        `json:"predicted_movements"`
	ObservedCompleteReps *int                   `json:"observed_complete_reps" extensions:"x-nullable"`
	MovementMatch        *bool                  `json:"movement_match" extensions:"x-nullable"`
	RepsDelta            *int                   `json:"reps_delta" extensions:"x-nullable"`
	EvidenceComplete     bool                   `json:"evidence_complete"`
	Status               string                 `json:"status"`
	Gaps                 []workoutprogress.Gap  `json:"gaps"`
	SourceRefs           []string               `json:"source_refs"`
}

type Summary struct {
	Total                int `json:"total"`
	Matched              int `json:"matched"`
	Mismatch             int `json:"mismatch"`
	InsufficientEvidence int `json:"insufficient_evidence"`
}

type Evaluation struct {
	SourceVersion string             `json:"source_version"`
	Fingerprint   string             `json:"fingerprint"`
	Records       []RecordEvaluation `json:"records"`
	Summary       Summary            `json:"summary"`
}

// ChunkEvidence preserves historical routing evidence rather than recomputing it
// using the current worker's classification rules.
type ChunkEvidence struct {
	ID                   uint            `json:"id"`
	MediaStartSecs       *float64        `json:"media_start_secs"`
	MediaEndSecs         *float64        `json:"media_end_secs"`
	ExerciseType         string          `json:"exercise_type"`
	Status               string          `json:"status"`
	Output               string          `json:"output"`
	MovementObservations json.RawMessage `json:"movement_observations"`
	TargetConfidence     float64         `json:"target_confidence"`
	TargetCues           string          `json:"target_cues"`
	SkipReason           string          `json:"skip_reason"`
	MotionScore          *float64        `json:"motion_score"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type Snapshot struct {
	Progress       workoutprogress.Progress `json:"progress"`
	AnalysisOutput string                   `json:"analysis_output"`
	Chunks         []ChunkEvidence          `json:"chunks"`
	Evaluation     Evaluation               `json:"evaluation"`
}

type Finding struct {
	StartSecs        float64 `json:"start_secs"`
	EndSecs          float64 `json:"end_secs"`
	Category         string  `json:"category"`
	Stage            string  `json:"stage"`
	Difference       string  `json:"difference"`
	OriginalEvidence string  `json:"original_evidence"`
	VisualEvidence   string  `json:"visual_evidence"`
	PossibleReason   string  `json:"possible_reason"`
	Limitations      string  `json:"limitations"`
	EvidenceLevel    string  `json:"evidence_level"`
}

type Batch struct {
	ID             int       `json:"id"`
	RecordID       uint      `json:"record_id"`
	StartSecs      float64   `json:"start_secs"`
	EndSecs        float64   `json:"end_secs"`
	CoreStartSecs  float64   `json:"core_start_secs"`
	CoreEndSecs    float64   `json:"core_end_secs"`
	Status         string    `json:"status"`
	Findings       []Finding `json:"findings"`
	Error          string    `json:"error,omitempty"`
	InputTokens    *int64    `json:"input_tokens,omitempty"`
	OutputTokens   *int64    `json:"output_tokens,omitempty"`
	TotalTokens    *int64    `json:"total_tokens,omitempty"`
	Attempts       int       `json:"attempts,omitempty"`
	GeminiFileName string    `json:"gemini_file_name,omitempty"`
}
