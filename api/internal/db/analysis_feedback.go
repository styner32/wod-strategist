package db

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

const (
	FeedbackTargetSession = "session"
	FeedbackTargetChunk   = "chunk"

	FeedbackCategorySessionAccuracy = "session_accuracy"
	FeedbackCategoryMovement        = "movement"
	FeedbackCategoryActivity        = "activity"
	FeedbackCategoryFatigue         = "fatigue"
	FeedbackCategoryOther           = "other"
)

// JSONDocument stores an arbitrary JSON value without converting it to a
// string in API responses. Database constraints and defaults remain defined by
// migrations; this type only supplies the database/sql conversion boundary.
type JSONDocument json.RawMessage

func (j JSONDocument) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "{}", nil
	}
	if !json.Valid(j) {
		return nil, fmt.Errorf("invalid JSON document")
	}
	return string(j), nil
}

func (j *JSONDocument) Scan(value any) error {
	if value == nil {
		*j = JSONDocument("null")
		return nil
	}

	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		return fmt.Errorf("scan JSON document from %T", value)
	}
	if !json.Valid(raw) {
		return fmt.Errorf("scan invalid JSON document")
	}
	*j = append((*j)[:0], raw...)
	return nil
}

func (j JSONDocument) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("{}"), nil
	}
	if !json.Valid(j) {
		return nil, fmt.Errorf("marshal invalid JSON document")
	}
	return j, nil
}

func (j *JSONDocument) UnmarshalJSON(data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("unmarshal invalid JSON document")
	}
	*j = append((*j)[:0], data...)
	return nil
}

// AnalysisFeedback is one immutable event in a correction revision chain.
// Later edits and retractions insert a new row with the same FeedbackKey.
type AnalysisFeedback struct {
	ID                    uint         `gorm:"primaryKey" json:"id"`
	FeedbackKey           string       `json:"feedback_key"`
	ProfileID             uint         `json:"-"`
	SessionID             string       `json:"session_id"`
	TargetType            string       `json:"target_type"`
	ChunkAnalysisResultID *uint        `json:"chunk_id,omitempty"`
	Category              string       `json:"category"`
	OriginalPrediction    JSONDocument `json:"original_prediction"`
	Correction            JSONDocument `json:"correction"`
	Note                  string       `json:"note"`
	ConsentToImprove      bool         `json:"consent_to_improve"`
	ClientRequestID       string       `json:"-"`
	Revision              int          `json:"revision"`
	SupersedesFeedbackID  *uint        `json:"supersedes_feedback_id,omitempty"`
	Retracted             bool         `json:"retracted"`
	ReanalysisRunID       *uint        `json:"reanalysis_run_id,omitempty"`
	CreatedAt             time.Time    `json:"created_at"`
}

func (AnalysisFeedback) TableName() string {
	return "analysis_feedback"
}
