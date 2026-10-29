package db

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrSessionWorkCancelled is a terminal user cancellation, not a retryable failure.
var ErrSessionWorkCancelled = errors.New("session processing cancelled")

func sessionAutomaticWorkAllowed(conn *gorm.DB, profileID uint, sessionID string, lock bool) error {
	if conn == nil {
		return nil
	}
	var row AnalysisResult
	query := conn.Select("id, profile_id, archived_at, automatic_processing_cancelled_at").Where("session_id = ?", sessionID)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if (profileID != 0 && row.ProfileID != profileID) || row.ArchivedAt != nil || row.AutomaticProcessingCancelledAt != nil {
		return ErrSessionWorkCancelled
	}
	return nil
}

func SessionAutomaticWorkAllowed(ctx context.Context, database *gorm.DB, profileID uint, sessionID string) error {
	if database == nil {
		return nil
	}
	return sessionAutomaticWorkAllowed(database.WithContext(ctx), profileID, sessionID, false)
}

// WithSessionAutomaticWork serializes a publication with archive. A missing
// parent is allowed for initial chunks: such a session cannot be archived yet.
func WithSessionAutomaticWork(ctx context.Context, database *gorm.DB, profileID uint, sessionID string, fn func(*gorm.DB) error) error {
	if database == nil {
		return fn(database)
	}
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		pid := profileID
		if pid == 0 {
			var parent AnalysisResult
			if err := tx.Select("profile_id").Where("session_id = ?", sessionID).First(&parent).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			pid = parent.ProfileID
		}
		if pid != 0 {
			var profile Profile
			if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).First(&profile, pid).Error; err != nil {
				return err
			}
		}
		if err := sessionAutomaticWorkAllowed(tx, profileID, sessionID, true); err != nil {
			return err
		}
		return fn(tx)
	})
}
