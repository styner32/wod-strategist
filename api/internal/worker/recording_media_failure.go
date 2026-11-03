package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The durable failure checkpoint is also an outbox for the existing history
// result. A lost lease cannot publish directly; this path rechecks the sealed
// run under the parent fence and never replaces a completed analysis.
func (w *Worker) persistRecordingMediaFailure(ctx context.Context, runID uint) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var run db.RecordingMediaRun
	if err := w.DB.WithContext(cleanup).Where("id = ? AND version = ?", runID, db.RecordingMediaVersion).First(&run).Error; err != nil {
		return err
	}
	if run.State != "FAILED" {
		return nil
	}
	manifest, err := recordingManifest(run)
	if err != nil || len(manifest) == 0 {
		return nil
	}
	publication, done, err := w.withSessionWorkContext(cleanup, run.SessionID, run.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(publication, err)
	}
	defer done()
	return w.withSessionPublication(publication, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		var current db.RecordingMediaRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND version = ?", run.ID, db.RecordingMediaVersion).First(&current).Error; err != nil {
			return err
		}
		if current.State != "FAILED" || current.ManifestHash != run.ManifestHash {
			return nil
		}
		var existing db.AnalysisResult
		err := tx.Where("profile_id = ? AND session_id = ?", run.ProfileID, run.SessionID).First(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && existing.Status == "COMPLETED" {
			return nil
		}
		message := current.SafeError
		if message == "" {
			message = "영상 처리 중 오류가 발생했습니다. 다시 시도해주세요."
		}
		if current.FailedStage == "duration" {
			message = videoTooLongUserMessage
		}
		mergedAvailable := current.MergedGeneration > 0 && current.MergedObject != ""
		if err == nil && existing.Status == "FAILED" && existing.Output == message && (!mergedAvailable || containsRecordingMerged(existing.AvailableVideos)) {
			return nil
		}
		var payload VideoAnalysisPayload
		if err := json.Unmarshal(current.Payload, &payload); err != nil {
			return err
		}
		payload.SessionID, payload.ProfileID, payload.MediaRunID = current.SessionID, current.ProfileID, current.ID
		failed := &db.AnalysisResult{SessionID: current.SessionID, ProfileID: current.ProfileID, Status: "FAILED", AnalysisType: db.AnalysisTypeWOD, Output: message}
		if mergedAvailable {
			failed.AvailableVideos = db.CommaStringArray{"merged"}
		}
		transactionWorker := *w
		transactionWorker.DB = tx
		return transactionWorker.persistVideoAnalysisFailed(publication, payload, failed)
	})
}

func containsRecordingMerged(videos db.CommaStringArray) bool {
	for _, video := range videos {
		if video == "merged" {
			return true
		}
	}
	return false
}
