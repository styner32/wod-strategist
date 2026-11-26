package db

import (
	"strings"
	"time"
)

type AnalysisResult struct {
	AutomaticProcessingCancelledAt *time.Time           `json:"automatic_processing_cancelled_at,omitempty"`
	WorkoutProgress                NullableJSONDocument `json:"workout_progress" swaggertype:"object"`

	AnalysisSummary          JSONDocument `json:"analysis_summary"`
	AgenticHighlightAnalysis JSONDocument `json:"-"`
	ID                       uint         `gorm:"primaryKey" json:"id"`
	SessionID                string       `gorm:"uniqueIndex;not null" json:"session_id"`
	ProfileID                uint         `gorm:"index;not null" json:"profile_id"`
	AnalysisType             string       `gorm:"default:wod" json:"analysis_type"` // wod, injury_supplement
	Status                   string       `json:"status"`                           // PENDING, COMPLETED, FAILED
	Output                   string       `json:"output"`
	InjuryOutput             string       `json:"injury_output,omitempty"` // Injury supplement analysis (appended, not overwritten)
	InjuryOutputAlt          string       `json:"-"`
	GeminiFileURI            string       `json:"-"`
	GeminiFileName           string       `json:"-"`
	GeminiMIMEType           string       `json:"-"`
	GeminiFileExpiresAt      *time.Time   `json:"-"`
	HighlightSegments        string       `json:"highlight_segments"` // JSON array of highlight segments
	Verified                 *bool        `json:"verified,omitempty"` // nil=unchecked, true=confirmed, false=hallucination detected
	// WODDescription is the user-supplied workout descriptor (e.g. "Fran", "For Time: 5 rounds of...").
	// Injected into analysis prompts to enable benchmark comparison and WOD-type-aware scoring.
	WODDescription string `gorm:"type:text;not null;default:''" json:"wod_description,omitempty"`
	// SessionScore is a compact JSON blob of per-dimension scores (0–100) produced at analysis time.
	// Schema: {"overall":74,"form":68,"intensity":82,"consistency":72,"movements":{},"summary":"..."}
	// Used to inject historical performance context into future analysis prompts.
	SessionScore           string           `gorm:"type:text;not null;default:'{}'" json:"session_score,omitempty"`
	SessionFatigue         *SessionFatigue  `gorm:"-" json:"session_fatigue,omitempty"`
	NormalizedWorkout      string           `json:"normalized_workout,omitempty"`
	MobilityObservations   string           `gorm:"type:text;not null;default:'[]'" json:"mobility_observations,omitempty"`
	StretchRecommendations string           `gorm:"type:text;not null;default:'[]'" json:"stretch_recommendations,omitempty"`
	AvailableVideos        CommaStringArray `gorm:"column:available_videos;type:text;not null;default:'merged'" json:"available_videos"`
	SensorVersion          int64            `gorm:"column:sensor_version;not null;default:0" json:"sensor_version,string"`
	SensorState            string           `gorm:"column:sensor_state;not null;default:NONE" json:"sensor_state"`
	SensorProcessing       JSONDocument     `gorm:"column:sensor_processing;type:jsonb;not null;default:'{}'" json:"-"`
	SensorSummary          JSONDocument     `gorm:"column:sensor_summary;type:jsonb;not null;default:'{}'" json:"sensor_summary,omitempty"`
	SensorTimeline         JSONDocument     `gorm:"column:sensor_timeline;type:jsonb" json:"-"`
	SensorNextAttemptAt    *time.Time       `gorm:"column:sensor_next_attempt_at" json:"-"`
	WorkoutAt              *time.Time       `gorm:"column:workout_at" json:"workout_at,omitempty"`
	WorkoutAtSource        *string          `gorm:"column:workout_at_source" json:"workout_at_source,omitempty"`
	ArchivedAt             *time.Time       `json:"archived_at,omitempty"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`

	HeartRate       *HeartRateSummaryDTO `gorm:"-" json:"heart_rate,omitempty"`
	InputStatus     *HistoryInputStatus  `gorm:"-" json:"input_status,omitempty"`
	Highlights      *[]HighlightResult   `gorm:"-" json:"highlights,omitempty"`
	ActivitySummary any                  `gorm:"-" json:"activity_summary,omitempty" swaggertype:"object"`

	session *Session `gorm:"-" json:"-"`
}

func (a *AnalysisResult) Session() *Session {
	if a == nil {
		return nil
	}

	return a.session
}

func (a *AnalysisResult) SetSession(s *Session) {
	if a == nil {
		return
	}

	a.session = s
}

func (a *AnalysisResult) TrimmedSessionID() string {
	if a == nil {
		return ""
	}

	return strings.TrimSpace(a.SessionID)
}

func (a *AnalysisResult) IsWODDescriptionEmpty() bool {
	if a == nil {
		return false
	}

	return strings.TrimSpace(a.WODDescription) == ""
}

func (a *AnalysisResult) IsCompleted() bool {
	if a == nil {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(a.Status), "COMPLETED")
}
