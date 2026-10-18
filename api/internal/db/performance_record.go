package db

import (
	"time"

	"gorm.io/gorm"
)

// PerformanceRecord stores a manual confirmation separately from raw progress.
// Schema constraints and indexes are defined by migration 000056.
type PerformanceRecord struct {
	ID                  uint         `gorm:"primaryKey" json:"id"`
	ProfileID           uint         `json:"-"`
	AnalysisID          uint         `json:"-"`
	SessionID           string       `json:"session_id"`
	IntervalID          string       `json:"interval_id"`
	SourceVersion       string       `json:"source_version"`
	Status              string       `json:"status"`
	Movement            string       `json:"movement"`
	StartSecs           float64      `json:"start_secs"`
	EndSecs             float64      `json:"end_secs"`
	ElapsedSeconds      float64      `json:"elapsed_seconds"`
	Reps                int          `json:"reps"`
	LoadValue           *float64     `json:"-"`
	LoadUnit            *string      `json:"-"`
	LoadBasis           *string      `json:"-"`
	LoadKG              *float64     `json:"load_kg"`
	Measurement         string       `json:"measurement"`
	Technique           string       `json:"technique"`
	WorkoutPrescription string       `json:"workout_prescription"`
	RepsPerMinute       float64      `json:"reps_per_minute"`
	RateBasis           string       `json:"rate_basis"`
	MedianCycleSeconds  *float64     `json:"median_cycle_seconds"`
	OriginalCandidate   JSONDocument `json:"-"`
	PerformedAt         *time.Time   `json:"performed_at"`
	PerformedAtSource   *string      `json:"performed_at_source"`
	ConfirmedAt         time.Time    `json:"confirmed_at"`
	CreatedAt           time.Time    `json:"-"`
	UpdatedAt           time.Time    `json:"-"`
}

// InvalidatePerformanceRecords must run in the transaction replacing analysis
// evidence. An unavailable source also invalidates every old confirmation.
func InvalidatePerformanceRecords(tx *gorm.DB, profileID uint, sessionID, sourceVersion string) error {
	query := tx.Model(&PerformanceRecord{}).Where("profile_id = ? AND session_id = ? AND status = ?", profileID, sessionID, "confirmed")
	if sourceVersion != "" {
		query = query.Where("source_version <> ?", sourceVersion)
	}
	return query.Updates(map[string]any{"status": "needs_confirmation", "updated_at": time.Now().UTC()}).Error
}
