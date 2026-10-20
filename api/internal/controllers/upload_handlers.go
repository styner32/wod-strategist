package controllers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
)

// @Summary      Complete Upload
// @Description  Notifies the backend that a video upload is complete and triggers analysis
// @Tags         upload
// @Accept       json
// @Produce      json
// @Param        request body CompleteUploadRequest true "Upload metadata"
// @Success      202 {object} CompleteUploadResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /upload-complete [post]
func (ctl *Controller) CompleteUpload(c *gin.Context) {
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	var req CompleteUploadRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("failed to bind JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
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
	if req.Movements == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "movements is required"})
		return
	}

	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}

	if !ctl.assertOwnsProfile(c, req.ProfileID) {
		return
	}

	if !isValidGCSURI(req.GCSURI) {
		logger.Log.Error("invalid GCS URI: must be a valid gs:// URI with a bucket", zap.String("uri", req.GCSURI))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid GCS URI"})
		return
	}

	if !isValidSessionGCSURI(req.GCSURI, req.ProfileID, req.SessionID) {
		logger.Log.Error("GCS URI does not match session path", zap.String("uri", req.GCSURI), zap.Uint("profile_id", req.ProfileID), zap.String("session_id", req.SessionID))
		c.JSON(http.StatusBadRequest, gin.H{"error": "GCS URI does not match session path"})
		return
	}

	if ok, reason := validateMovements(req.Movements); !ok {
		logger.Log.Error("invalid movements", zap.String("reason", reason), zap.Strings("movements", req.Movements))
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}

	if len(req.Injuries) >= 100 {
		logger.Log.Error("too many injuries", zap.Int("count", len(req.Injuries)))
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many injuries"})
		return
	}
	if !allowedInjuries.containsAll(req.Injuries) {
		logger.Log.Error("invalid injuries", zap.Strings("injuries", req.Injuries))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid injuries"})
		return
	}

	workoutType := worker.NormalizeWorkoutType(req.WorkoutType)
	if !worker.IsValidWorkoutType(workoutType) {
		logger.Log.Error("invalid workout type", zap.String("workout_type", workoutType))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workout type"})
		return
	}
	if err := ctl.persistSessionMovementHints(c.Request.Context(), req.SessionID, req.ProfileID, req.Movements); err != nil {
		logger.Log.Error("failed to persist movement hints", zap.String("session_id", req.SessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist movement hints"})
		return
	}
	if err := ctl.persistSessionWODDescription(c.Request.Context(), req.SessionID, req.ProfileID, req.WODDescription); err != nil {
		logger.Log.Error("failed to persist wod description", zap.String("session_id", req.SessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist wod description"})
		return
	}
	if strings.TrimSpace(req.AppearanceHints) != "" {
		if err := persistSessionAppearanceHints(c.Request.Context(), ctl.db, req.SessionID, req.ProfileID, &AppearanceInput{Appearance: req.AppearanceHints}); err != nil {
			logger.Log.Error("failed to persist appearance hints", zap.String("session_id", req.SessionID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist appearance hints"})
			return
		}
	}

	logger.Log.Info("Submit a video analysis request",
		zap.String("session_id", req.SessionID),
		zap.String("gcs_uri", req.GCSURI),
		zap.Strings("movements", req.Movements),
		zap.Strings("injuries", req.Injuries),
		zap.String("workout_type", workoutType),
	)

	task, err := ctl.newVideoAnalysisTask(req.SessionID, req.GCSURI, workoutType, req.Movements, req.Injuries, req.ProfileID, req.EnableTTS, req.WODDescription)
	if err != nil {
		logger.Log.Error("failed to create task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.queueClient.Enqueue(task)
	if err != nil {
		logger.Log.Error("failed to enqueue task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "Analysis started",
		"task_id":    info.ID,
		"session_id": req.SessionID,
	})
}

// @Summary      Merge Chunks
// @Description  Triggers server-side merging of all uploaded chunks for a session, then runs full video analysis
// @Tags         upload
// @Accept       json
// @Produce      json
// @Param        request body MergeChunksRequest true "Session and workout metadata"
// @Success      202 {object} MergeChunksResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /merge-chunks [post]
func (ctl *Controller) MergeChunks(c *gin.Context) {
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	var req MergeChunksRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("failed to bind JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.SessionID = sanitizeIdentifier(req.SessionID)
	req.WorkoutType = trimRequiredString(req.WorkoutType)

	if req.SessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}
	if !isValidSessionID(req.SessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}

	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
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
	if err := ctl.persistSessionWODDescription(c.Request.Context(), req.SessionID, req.ProfileID, req.WODDescription); err != nil {
		logger.Log.Error("failed to persist wod description", zap.String("session_id", req.SessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist wod description"})
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

	// The merge task uses profileID + sessionID to discover chunks in GCS.
	// prefix = "videos/{profileId}/{sessionId}/"
	placeholderGCSURI := fmt.Sprintf("gs://%s/videos/%d/%s/", ctl.bucketName, req.ProfileID, req.SessionID)

	task, err := ctl.newMergeChunksTask(req.SessionID, placeholderGCSURI, workoutType, req.Movements, req.Injuries, req.ProfileID, req.EnableTTS, req.WODDescription)
	if err != nil {
		logger.Log.Error("failed to create merge task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.queueClient.Enqueue(task)
	if err != nil {
		logger.Log.Error("failed to enqueue merge task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "Merge started",
		"task_id":    info.ID,
		"session_id": req.SessionID,
	})
}
