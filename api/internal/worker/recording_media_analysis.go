package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (w *Worker) handleRecordingVideoAnalysisTask(ctx context.Context, task *asynq.Task, p VideoAnalysisPayload) error {
	if p.AnalysisPreparationID == 0 || p.SourceGeneration <= 0 {
		return fmt.Errorf("recording analysis input is not prepared: %w", asynq.SkipRetry)
	}
	var run db.RecordingMediaRun
	var prep db.AnalysisVideoPreparation
	token := uuid.NewString()
	alreadyCompleted := false
	err := db.WithSessionAutomaticWork(ctx, w.DB, p.ProfileID, p.SessionID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND profile_id = ? AND session_id = ? AND version = ?", p.MediaRunID, p.ProfileID, p.SessionID, db.RecordingMediaVersion).First(&run).Error; err != nil {
			return err
		}
		if run.State != "READY_FOR_ANALYSIS" && run.State != "ANALYZING" {
			return errRecordingMediaStale
		}
		if p.MediaDispatchVersion != run.DispatchVersion {
			return errRecordingMediaStale
		}
		var completed int64
		if err := tx.Model(&db.AnalysisResult{}).Where("profile_id = ? AND session_id = ? AND status = ? AND updated_at >= ?", run.ProfileID, run.SessionID, "COMPLETED", run.CreatedAt).Count(&completed).Error; err != nil {
			return err
		}
		if completed > 0 {
			alreadyCompleted = true
			return tx.Model(&run).Updates(map[string]any{"state": "COMPLETED", "lease_token": "", "lease_expires_at": nil}).Error
		}
		if run.LeaseToken != "" && run.LeaseExpiresAt != nil && run.LeaseExpiresAt.After(time.Now()) {
			return errRecordingMediaStale
		}
		if err := tx.Where("id = ? AND run_id = ? AND source_generation = ? AND state = ?", p.AnalysisPreparationID, run.ID, run.MergedGeneration, "READY").First(&prep).Error; err != nil {
			return err
		}
		if prep.OutputGeneration != p.SourceGeneration || p.FilePath != "gs://"+w.BucketName+"/"+prep.OutputObject {
			return fmt.Errorf("recording analysis source does not match prepared generation: %w", asynq.SkipRetry)
		}
		if run.AnalysisAttempts >= recordingMediaMaxAttempts {
			ctx = WithRetryCount(ctx, 3)
		} else {
			run.AnalysisAttempts++
		}
		return tx.Model(&run).Updates(map[string]any{"state": "ANALYZING", "lease_token": token, "lease_expires_at": time.Now().UTC().Add(recordingMediaLeaseDuration), "analysis_attempts": run.AnalysisAttempts, "analysis_dispatched": true}).Error
	})
	if err != nil {
		if recordingMediaIgnored(err) {
			return nil
		}
		return err
	}
	if alreadyCompleted {
		return nil
	}
	workCtx, done, err := w.recordingMediaContext(ctx, run, "recording_media_runs", run.ID, token)
	if err != nil {
		return err
	}
	defer done()
	err = w.scopedSessionWorker(workCtx).handleVideoAnalysisTask(workCtx, task)
	state := "COMPLETED"
	failedStage := ""
	if err != nil {
		state = "ANALYZING" // Asynq owns ordinary AI retries; media artifacts stay READY.
		if errors.Is(err, asynq.SkipRetry) || getRetryCount(ctx) >= 3 {
			state, failedStage = "FAILED", "analysis"
		}
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if errors.Is(db.SessionAutomaticWorkAllowed(persistCtx, w.DB, run.ProfileID, run.SessionID), db.ErrSessionWorkCancelled) {
		_ = w.DB.WithContext(persistCtx).Model(&db.RecordingMediaRun{}).Where("id = ? AND lease_token = ?", run.ID, token).Updates(map[string]any{"state": "CANCELLED", "lease_token": "", "lease_expires_at": nil}).Error
		return nil
	}
	persistErr := db.WithSessionAutomaticWork(persistCtx, w.DB, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		return tx.Model(&db.RecordingMediaRun{}).Where("id = ? AND lease_token = ?", run.ID, token).Updates(map[string]any{"state": state, "failed_stage": failedStage, "lease_token": "", "lease_expires_at": nil}).Error
	})
	if persistErr != nil {
		return persistErr
	}
	return sessionWorkResult(workCtx, err)
}

// Only missing or terminal queue deliveries are recovered. A scheduled retry
// remains owned by Asynq, so a recovery tick cannot create another paid call.
func (w *Worker) recoverRecordingVideoAnalysis(ctx context.Context, run db.RecordingMediaRun) error {
	if run.LeaseToken != "" && run.LeaseExpiresAt != nil && run.LeaseExpiresAt.After(time.Now()) {
		return nil
	}
	if w.QueueInspector == nil {
		return errors.New("analysis recovery requires a queue inspector")
	}
	var prep db.AnalysisVideoPreparation
	if err := w.DB.WithContext(ctx).Where("run_id = ? AND source_generation = ? AND state = ?", run.ID, run.MergedGeneration, "READY").First(&prep).Error; err != nil {
		return err
	}
	info, err := w.QueueInspector.GetTaskInfo("default", recordingAnalysisTaskID(run.ID, prep.ID, run.DispatchVersion))
	if err != nil && !errors.Is(err, asynq.ErrTaskNotFound) && !errors.Is(err, asynq.ErrQueueNotFound) {
		return err
	}
	if info != nil {
		switch info.State {
		case asynq.TaskStatePending, asynq.TaskStateActive, asynq.TaskStateScheduled, asynq.TaskStateRetry, asynq.TaskStateAggregating:
			return nil
		}
	}
	return db.WithSessionAutomaticWork(ctx, w.DB, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		var current db.RecordingMediaRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, run.ID).Error; err != nil {
			return err
		}
		if (current.State != "ANALYZING" && current.State != "READY_FOR_ANALYSIS") || current.DispatchVersion != run.DispatchVersion || (current.LeaseExpiresAt != nil && current.LeaseExpiresAt.After(time.Now())) {
			return nil
		}
		var completed int64
		if err := tx.Model(&db.AnalysisResult{}).Where("profile_id = ? AND session_id = ? AND status = ? AND updated_at >= ?", current.ProfileID, current.SessionID, "COMPLETED", current.CreatedAt).Count(&completed).Error; err != nil {
			return err
		}
		if completed > 0 {
			return tx.Model(&current).Updates(map[string]any{"state": "COMPLETED", "lease_token": "", "lease_expires_at": nil}).Error
		}
		if current.AnalysisAttempts >= recordingMediaMaxAttempts {
			// Keep the existing history status terminal too when a worker dies
			// before its ordinary final retry can publish the failure.
			scoped := *w
			scoped.DB = tx
			payload := VideoAnalysisPayload{SessionID: current.SessionID, ProfileID: current.ProfileID}
			failed := &db.AnalysisResult{SessionID: current.SessionID, ProfileID: current.ProfileID, Status: "FAILED", AnalysisType: db.AnalysisTypeWOD, Output: "동영상 분석 중 오류가 발생했습니다. 다시 시도해주세요.", AvailableVideos: db.CommaStringArray{"merged"}}
			if err := scoped.persistVideoAnalysisFailed(ctx, payload, failed); err != nil {
				return err
			}
			return tx.Model(&current).Updates(map[string]any{"state": "FAILED", "failed_stage": "analysis", "safe_error": "동영상 분석 중 오류가 발생했습니다. 다시 시도해주세요.", "lease_token": "", "lease_expires_at": nil}).Error
		}
		return tx.Model(&current).Updates(map[string]any{"state": "READY_FOR_ANALYSIS", "analysis_dispatched": false, "dispatch_version": gorm.Expr("dispatch_version + 1"), "lease_token": "", "lease_expires_at": nil}).Error
	})
}
