package db

import (
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/wodeval"
)

const (
	SessionStatusStarted   = "started"
	SessionStatusCompleted = "completed"
	SessionStatusFailed    = "failed"
)

type SessionStatus string

func (s SessionStatus) String() string {
	return string(s)
}

type Session struct {
	WODEvaluationVersion        int                  `json:"wod_evaluation_version"`
	Effort                      *int                 `json:"effort"`
	ActivitySummary             NullableJSONDocument `json:"activity_summary,omitempty" swaggertype:"object"`
	ActivityReviewAttempts      NullableJSONDocument `gorm:"column:activity_review_attempts;type:jsonb" json:"-"`
	ActivitySummaryCacheVersion int                  `gorm:"column:activity_summary_cache_version" json:"-"`

	ID             uint          `gorm:"primaryKey" json:"id"`
	SessionID      string        `gorm:"uniqueIndex;not null" json:"session_id"`
	Status         SessionStatus `json:"status"` // started, completed, failed
	ProfileID      uint          `gorm:"index;not null" json:"profile_id"`
	IdempotencyKey string        `gorm:"index;not null" json:"idempotency_key"`
	WODDescription string        `gorm:"not null" json:"wod_description"`
	MovementHints  JSONDocument  `json:"movement_hints"`
	WorkoutType    string        `json:"workout_type"`
	UpdatedAt      time.Time     `json:"updated_at"`
	CreatedAt      time.Time     `json:"created_at"`
}

func (s *Session) IsEffortEligible() bool {
	return s != nil && s.WorkoutType == "wod" && s.WODEvaluationVersion == wodeval.Version
}

func (s *Session) IsWODDescriptionEmpty() bool {
	return s == nil || strings.TrimSpace(s.WODDescription) == ""
}
