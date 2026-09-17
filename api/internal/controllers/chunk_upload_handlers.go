package controllers

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

// @Summary      Chunk Complete
// @Description  Notifies the backend that a chunk upload is complete and triggers chunk analysis
// @Tags         upload
// @Accept       json
// @Produce      json
// @Param        request body ChunkCompleteRequest true "Upload metadata"
// @Success      202 {object} CompleteUploadResponse
// @Router       /chunk-complete [post]
func (ctl *Controller) ChunkComplete(c *gin.Context) {
	var req ChunkCompleteRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("failed to bind JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.LiveAnalysisVersion != 0 && req.LiveAnalysisVersion != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported live_analysis_version"})
		return
	}
	if req.LiveAnalysisVersion == 1 && !activity.ValidInterval(req.StartSecs, req.EndSecs) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "live recording requires a valid capture interval"})
		return
	}
	req.SessionID = sanitizeIdentifier(req.SessionID)
	req.GCSURI = trimRequiredString(req.GCSURI)
	req.WorkoutType = trimRequiredString(req.WorkoutType)

	if req.SessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}
	if !isValidSessionID(req.SessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}
	if req.GCSURI == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gcs_uri is required"})
		return
	}

	if !isValidGCSURI(req.GCSURI) {
		logger.Log.Error("invalid GCS URI: must be a valid gs:// URI with a bucket", zap.String("uri", req.GCSURI))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid GCS URI"})
		return
	}

	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}

	if !isValidSessionGCSURI(req.GCSURI, req.ProfileID, req.SessionID) {
		logger.Log.Error("GCS URI does not match session path", zap.String("uri", req.GCSURI), zap.Uint("profile_id", req.ProfileID), zap.String("session_id", req.SessionID))
		c.JSON(http.StatusBadRequest, gin.H{"error": "GCS URI does not match session path"})
		return
	}

	if !ctl.assertOwnsProfile(c, req.ProfileID) {
		return
	}
	if err := ctl.persistSessionMovementHints(c.Request.Context(), req.SessionID, req.ProfileID, req.Movements); err != nil {
		logger.Log.Error("failed to persist movement hints", zap.String("session_id", req.SessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist movement hints"})
		return
	}
	if strings.TrimSpace(req.AppearanceHints) != "" {
		if err := persistSessionAppearanceHints(c.Request.Context(), ctl.db, req.SessionID, req.ProfileID, &AppearanceInput{Appearance: req.AppearanceHints}); err != nil {
			logger.Log.Error("failed to persist appearance hints", zap.String("session_id", req.SessionID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist appearance hints"})
			return
		}
	}

	workoutType := worker.NormalizeWorkoutType(req.WorkoutType)

	logger.Log.Info("chunk-complete received",
		zap.String("session_id", req.SessionID),
		zap.Float64("workout_confidence", req.WorkoutConfidence),
		zap.Int("heart_rate_bpm", req.HeartRateBPM),
		zap.Float64("start_secs", req.StartSecs),
		zap.Float64("end_secs", req.EndSecs),
	)

	var task *asynq.Task
	_, err := gorm.G[db.Session](ctl.db).Where("session_id = ? AND profile_id = ?", req.SessionID, req.ProfileID).First(c)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			task, err = worker.NewChunkAnalysisTask(req.SessionID, req.GCSURI, workoutType, req.Movements, req.Injuries, req.ProfileID, req.StartSecs, req.EndSecs, req.HeartRateBPM, req.WODDescription, req.WorkoutConfidence)
			if err != nil {
				logger.Log.Error("failed to create chunk task", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
				return
			}
		} else {
			logger.Log.Error("failed to get session", zap.Error(err), zap.String("session_id", req.SessionID), zap.Uint("profile_id", req.ProfileID))
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to get session"})
			return
		}
	}

	if task == nil {
		task, err = worker.NewChunkAnalysisWithSessionTask(req.SessionID, req.GCSURI, req.ProfileID, req.StartSecs, req.EndSecs, req.HeartRateBPM, req.WorkoutConfidence)
		if err != nil {
			logger.Log.Error("failed to create chunk task", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
			return
		}
	}

	if req.LiveAnalysisVersion == 1 {
		var payload map[string]any
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid chunk task"})
			return
		}
		payload["live_analysis_version"] = req.LiveAnalysisVersion
		encoded, err := json.Marshal(payload)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid chunk task"})
			return
		}
		task = asynq.NewTask(task.Type(), encoded)
	}
	info, err := ctl.queueClient.Enqueue(task)
	if err != nil {
		logger.Log.Error("failed to enqueue chunk task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "Chunk analysis started",
		"task_id":    info.ID,
		"session_id": req.SessionID,
	})
}
