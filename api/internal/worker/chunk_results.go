package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

// Serialize writers for an original chunk, including when no row exists yet.
// Existing duplicate rows are retained because feedback may reference their IDs.
func (w *Worker) persistChunkAnalysisResult(ctx context.Context, result *db.ChunkAnalysisResult) error {
	if w.DB == nil {
		return errors.New("database is not configured")
	}
	key, err := json.Marshal([]any{result.ProfileID, result.SessionID, result.FilePath})
	if err != nil {
		return err
	}
	return w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", string(key)).Error; err != nil {
			return err
		}
		var existing db.ChunkAnalysisResult
		err := tx.Where("session_id = ? AND profile_id = ? AND file_path = ?", result.SessionID, result.ProfileID, result.FilePath).
			Order("CASE WHEN status = 'COMPLETED' THEN 0 ELSE 1 END, id ASC").First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(result).Error
		}
		if err != nil {
			return err
		}
		if existing.Status == "COMPLETED" {
			*result = existing
			return nil
		}
		// Keep verified merged-media offsets and references when a late success
		// replaces a failure. Include zero values from the new analysis.
		if result.StartSecs == nil {
			result.StartSecs = existing.StartSecs
			result.EndSecs = existing.EndSecs
		}
		if err := tx.Model(&existing).Select("*").Omit("id", "created_at", "media_start_secs", "media_end_secs").Updates(result).Error; err != nil {
			return fmt.Errorf("update chunk result: %w", err)
		}
		result.ID = existing.ID
		return nil
	})
}
