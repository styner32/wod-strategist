package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (ctl *Controller) GetHistory(c *gin.Context) {
	if c.Query("profile_id") == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}
	pid, err := strconv.ParseUint(c.Query("profile_id"), 10, 32)
	if err != nil || pid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile_id"})
		return
	}
	if !ctl.assertOwnsProfile(c, uint(pid)) {
		return
	}
	archived := c.Query("archived")
	if archived != "" && archived != "true" && archived != "false" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "archived must be true or false"})
		return
	}
	query := ctl.db.WithContext(c.Request.Context()).Where("profile_id = ?", pid)
	if archived == "true" {
		query = query.Where("archived_at IS NOT NULL")
	} else {
		query = query.Where("archived_at IS NULL")
	}
	from, to := c.Query("from"), c.Query("to")
	if from != "" && to != "" {
		start, e1 := time.Parse("2006-01-02", from)
		end, e2 := time.Parse("2006-01-02", to)
		if e1 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from date, expected YYYY-MM-DD"})
			return
		}
		if e2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to date, expected YYYY-MM-DD"})
			return
		}
		query = query.Where("created_at >= ? AND created_at < ?", start, end.AddDate(0, 0, 1)).Order("created_at desc")
	} else {
		limit, e := strconv.Atoi(c.Query("limit"))
		if e != nil || limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		if before, e := strconv.ParseUint(c.Query("before_id"), 10, 32); e == nil && before > 0 {
			query = query.Where("id < ?", before)
		}
		query = query.Order("id desc").Limit(limit)
	}
	results := []db.AnalysisResult{}
	if err := query.Find(&results).Error; err != nil {
		logger.Log.Error("failed to fetch history", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}
	populateMissingWODDescriptions(c.Request.Context(), ctl.db, results)
	c.JSON(http.StatusOK, compactAnalysisSummaries(normalizeHighlightResultsForResponseWithSchema(results, workoutLoadSchemaVersion(c))))
}

func (ctl *Controller) ArchiveHistory(c *gin.Context)   { ctl.setHistoryArchived(c, true) }
func (ctl *Controller) UnarchiveHistory(c *gin.Context) { ctl.setHistoryArchived(c, false) }

func (ctl *Controller) setHistoryArchived(c *gin.Context, archive bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if !ctl.assertOwnsAnalysis(c, uint(id)) {
		return
	}
	ctx := c.Request.Context()
	var row db.AnalysisResult
	if err := ctl.db.WithContext(ctx).First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "analysis not found"})
		return
	}
	var revision int64
	err = ctl.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Always lock profile before analysis, including sensor calibration writes.
		var profile db.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&profile, row.ProfileID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; err != nil {
			return err
		}
		revision = profile.WorkoutHistoryRevision
		if (row.ArchivedAt != nil) == archive {
			return nil
		}
		now := time.Now().UTC()
		if archive {
			updates := map[string]any{"archived_at": now, "automatic_processing_cancelled_at": gorm.Expr("COALESCE(automatic_processing_cancelled_at, ?)", now), "updated_at": now}
			if row.Status != "COMPLETED" && row.Status != "FAILED" {
				updates["status"] = "CANCELLED"
			}
			if row.SensorState == db.SensorStateUploading || row.SensorState == db.SensorStatePending || row.SensorState == db.SensorStateRunning {
				updates["sensor_state"], updates["sensor_next_attempt_at"] = db.SensorStateCancelled, nil
				updates["sensor_version"] = gorm.Expr("sensor_version + 1")
				var processing map[string]any
				_ = json.Unmarshal(row.SensorProcessing, &processing)
				if processing == nil {
					processing = map[string]any{}
				}
				processing["last_error_code"] = "SESSION_ARCHIVED"
				delete(processing, "lease_token")
				delete(processing, "lease_started_at")
				delete(processing, "retry_not_before")
				raw, _ := json.Marshal(processing)
				updates["sensor_processing"] = db.JSONDocument(raw)
			}
			updates["analysis_summary"] = interruptArchiveEnrichment(row.AnalysisSummary)
			updates["agentic_highlight_analysis"] = interruptArchiveEnrichment(row.AgenticHighlightAnalysis)
			if err := tx.Model(&db.AnalysisResult{}).Where("id = ?", id).Updates(updates).Error; err != nil {
				return err
			}
			for _, table := range []string{"chunk_analysis_results", "highlight_results", "chunk_reanalysis_runs", "session_reanalysis_runs", "recognition_review_runs"} {
				values := map[string]any{"status": "CANCELLED", "updated_at": now}
				if table == "chunk_reanalysis_runs" || table == "session_reanalysis_runs" || table == "recognition_review_runs" {
					values["safe_error"] = "SESSION_ARCHIVED"
					values["completed_at"] = now
				}
				if err := tx.Table(table).Where("session_id = ? AND profile_id = ? AND UPPER(status) IN ('QUEUED','PENDING','UPLOADED','PROCESSING','MERGING','RUNNING')", row.SessionID, row.ProfileID).Updates(values).Error; err != nil {
					return err
				}
			}
			if err := tx.Exec(`UPDATE sessions SET activity_summary = jsonb_set(activity_summary, '{review_state}', '"unavailable"'::jsonb), updated_at = ? WHERE session_id = ? AND profile_id = ? AND activity_summary->>'review_state' IN ('provisional','queued','running')`, now, row.SessionID, row.ProfileID).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&db.AnalysisResult{}).Where("id = ?", id).Updates(map[string]any{"archived_at": nil, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := db.RecomputeProfileENMOCalibrationTx(ctx, tx, row.ProfileID, false); err != nil {
			return err
		}
		revision++
		return tx.Table("profiles").Where("id = ?", row.ProfileID).Update("workout_history_revision", revision).Error
	})
	if err != nil {
		logger.Log.Error("failed to change history archive state", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to change archive state"})
		return
	}
	message := "history unarchived"
	if archive {
		message = "history archived"
	}
	c.JSON(http.StatusOK, gin.H{"message": message, "history_revision": strconv.FormatInt(revision, 10)})
}

func interruptArchiveEnrichment(raw db.JSONDocument) db.JSONDocument {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return db.JSONDocument(`{}`)
	}
	active := func(status any) bool {
		s, _ := status.(string)
		return s == "pending" || s == "preparing" || s == "running" || s == "stale"
	}
	if active(value["status"]) {
		value["status"], value["error"] = "interrupted", "SESSION_ARCHIVED"
		if owned, _ := value["owned_upload"].(bool); owned {
			value["cleanup_pending"] = true
		}
	}
	if items, ok := value["items"].([]any); ok {
		for _, item := range items {
			if m, ok := item.(map[string]any); ok && active(m["status"]) {
				m["status"], m["error"] = "interrupted", "SESSION_ARCHIVED"
			}
		}
	}
	result, _ := json.Marshal(value)
	return db.JSONDocument(result)
}

// requireSessionProcessing checks entry points; worker publication transactions
// repeat the check so archive racing an enqueue cannot resurrect work.
func (ctl *Controller) requireSessionProcessing(c *gin.Context, sessionID string, profileID uint, automatic bool) bool {
	var row db.AnalysisResult
	err := ctl.db.WithContext(c.Request.Context()).Select("id,profile_id,archived_at,automatic_processing_cancelled_at").Where("session_id = ?", sessionID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to check session state"})
		return false
	}
	if profileID != 0 && row.ProfileID != profileID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized for this session"})
		return false
	}
	if row.ArchivedAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "session is archived", "error_code": "SESSION_ARCHIVED"})
		return false
	}
	if automatic && row.AutomaticProcessingCancelledAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "session processing was cancelled", "error_code": "SESSION_PROCESSING_CANCELLED"})
		return false
	}
	return true
}

func (ctl *Controller) enqueueSessionAutomaticTask(c *gin.Context, sessionID string, profileID uint, task *asynq.Task) (*asynq.TaskInfo, error) {
	return ctl.enqueueSessionAutomaticTaskWithMetadata(c, sessionID, profileID, task, nil)
}

type sessionUploadMetadata struct {
	Movements       []string
	WODDescription  string
	AppearanceHints string
}

const sessionUploadAcceptanceTimeout = 15 * time.Second

func (ctl *Controller) enqueueSessionAutomaticTaskWithMetadata(c *gin.Context, sessionID string, profileID uint, task *asynq.Task, metadata *sessionUploadMetadata) (*asynq.TaskInfo, error) {
	ctx := c.Request.Context()
	if metadata != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Once acceptance starts, a caller disconnect must not roll back source
		// metadata after Redis has accepted the task. Bound the detached work.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), sessionUploadAcceptanceTimeout)
		defer cancel()
	}
	var info *asynq.TaskInfo
	err := db.WithSessionAutomaticWork(ctx, ctl.db, profileID, sessionID, func(tx *gorm.DB) error {
		// Metadata is source evidence too: serialize it with archive and roll it
		// back when this request cannot enqueue its processing task.
		if metadata != nil {
			if err := persistSessionMovementHints(ctx, tx, sessionID, profileID, metadata.Movements); err != nil {
				return err
			}
			if err := persistSessionWODDescription(ctx, tx, sessionID, profileID, metadata.WODDescription); err != nil {
				return err
			}
			if err := persistSessionAppearanceHints(ctx, tx, sessionID, profileID, &AppearanceInput{Appearance: metadata.AppearanceHints}); err != nil {
				return err
			}
		}
		var err error
		info, err = ctl.queueClient.Enqueue(task)
		return err
	})
	return info, err
}

func writeSessionProcessingConflict(c *gin.Context, err error) bool {
	if !errors.Is(err, db.ErrSessionWorkCancelled) {
		return false
	}
	c.JSON(http.StatusConflict, gin.H{"error": "session is archived or processing was cancelled", "error_code": "SESSION_PROCESSING_CANCELLED"})
	return true
}

func lockActiveSession(tx *gorm.DB, sessionID string, profileID uint) error {
	var row db.AnalysisResult
	err := tx.Select("id,profile_id,archived_at").Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id = ?", sessionID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.ProfileID != profileID || row.ArchivedAt != nil {
		return db.ErrSessionWorkCancelled
	}
	return nil
}
