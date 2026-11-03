package controllers

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"net/http"
	"path/filepath"
	"strings"
)

func (ctl *Controller) Upload(c *gin.Context) {
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	sessionID := trimRequiredString(c.PostForm("session_id"))
	if sessionID == "" {
		logger.Log.Error("session_id is required")
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}
	if !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}

	profileID, err := ctl.resolveLegacyUploadProfile(c.Request.Context(), sessionID, c.PostForm("profile_id"))
	if err != nil {
		switch {
		case errors.Is(err, errLegacyUploadProfileRequired):
			c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		case errors.Is(err, errLegacyUploadProfileInvalid):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile_id"})
		case errors.Is(err, errLegacyUploadProfileMissing):
			c.JSON(http.StatusBadRequest, gin.H{"error": "profile not found"})
		case errors.Is(err, errLegacyUploadProfileMismatch):
			c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id does not match session"})
		default:
			logger.Log.Error("failed to resolve legacy upload profile", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve profile"})
		}
		return
	}
	if !ctl.assertOwnsProfile(c, profileID) {
		return
	}
	if !ctl.requireSessionProcessing(c, sessionID, profileID, true) {
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		logger.Log.Error("file is required", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		logger.Log.Error("failed to open file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open file"})
		return
	}
	defer file.Close()

	// Preserve the legacy dev-tool storage path. The queued analysis task uses
	// the resolved real profile so analysis_results never receives profile_id=0.
	objectName := buildVideoObjectName(0, sessionID, fileHeader.Filename)
	gcsURI, err := ctl.storageClient.UploadFile(c.Request.Context(), file, objectName)
	if err != nil {
		logger.Log.Error("failed to upload file to GCS", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
		return
	}

	task, err := ctl.newVideoAnalysisTask(sessionID, gcsURI, worker.WorkoutTypeWOD, nil, nil, profileID, false, "")
	if err != nil {
		logger.Log.Error("failed to create task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.enqueueSessionAutomaticTask(c, sessionID, profileID, task)
	if err != nil {
		if writeSessionProcessingConflict(c, err) {
			return
		}
		logger.Log.Error("failed to enqueue task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "File uploaded and analysis started",
		"task_id":    info.ID,
		"session_id": sessionID,
		"file_url":   gcsURI,
	})
}

func (ctl *Controller) GenerateHighlight(c *gin.Context) {
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	var req GenerateHighlightRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("failed to bind JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.SessionID = sanitizeIdentifier(req.SessionID)

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
	if !ctl.requireSessionProcessing(c, req.SessionID, req.ProfileID, true) {
		return
	}

	if req.MaxDuration <= 0 {
		req.MaxDuration = 60
	}
	if req.MaxDuration > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_duration cannot exceed 120 seconds"})
		return
	}

	logger.Log.Info("Submit a highlight generation request",
		zap.String("session_id", req.SessionID),
		zap.Int("max_duration", req.MaxDuration),
		zap.Uint("profile_id", req.ProfileID))

	task, err := ctl.newGenerateHighlight(req.SessionID, req.ProfileID, req.MaxDuration)
	if err != nil {
		logger.Log.Error("failed to create highlight task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.enqueueSessionAutomaticTask(c, req.SessionID, req.ProfileID, task)
	if err != nil {
		if writeSessionProcessingConflict(c, err) {
			return
		}
		logger.Log.Error("failed to enqueue highlight task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "Highlight generation started",
		"task_id":    info.ID,
		"session_id": req.SessionID,
	})
}

func (ctl *Controller) VerifyHighlights(c *gin.Context) {
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	var req VerifyHighlightsRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("failed to bind JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.SessionID = sanitizeIdentifier(req.SessionID)

	if req.SessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}
	if !isValidSessionID(req.SessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}

	if !ctl.assertOwnsSession(c, req.SessionID) {
		return
	}
	if !ctl.requireSessionProcessing(c, req.SessionID, 0, true) {
		return
	}

	logger.Log.Info("Submit a highlight verification request",
		zap.String("session_id", req.SessionID))

	task, err := ctl.newVerifyHighlightsTask(req.SessionID)
	if err != nil {
		logger.Log.Error("failed to create verify highlights task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.enqueueSessionAutomaticTask(c, req.SessionID, 0, task)
	if err != nil {
		if writeSessionProcessingConflict(c, err) {
			return
		}
		logger.Log.Error("failed to enqueue verify highlights task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "Highlight verification started",
		"task_id":    info.ID,
		"session_id": req.SessionID,
	})
}

func (ctl *Controller) RetryAnalysis(c *gin.Context) {
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not configured"})
		return
	}

	var req RetryAnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.SessionID = sanitizeIdentifier(req.SessionID)
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
	if !ctl.requireSessionProcessing(c, req.SessionID, req.ProfileID, true) {
		return
	}
	if ctl.handleRecordingMediaRetry(c, req.SessionID, req.ProfileID) {
		return
	}

	// Find a video file in GCS for this session. Prefer merged.mp4.
	prefix := fmt.Sprintf("videos/%d/%s/", req.ProfileID, req.SessionID)
	objects, err := ctl.storageClient.ListObjects(c.Request.Context(), prefix)
	if err != nil {
		logger.Log.Error("failed to list GCS objects for retry",
			zap.String("session_id", req.SessionID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to find video files"})
		return
	}

	if len(objects) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no video files found for this session"})
		return
	}

	// Pick the best candidate: merged.mp4 > any .mp4 file
	var gcsURI string
	for _, obj := range objects {
		base := filepath.Base(obj)
		if base == "merged.mp4" {
			gcsURI = fmt.Sprintf("gs://%s/%s", ctl.bucketName, obj)
			break
		}
	}
	if gcsURI == "" {
		// Fall back to first .mp4 file
		for _, obj := range objects {
			if strings.HasSuffix(obj, ".mp4") {
				gcsURI = fmt.Sprintf("gs://%s/%s", ctl.bucketName, obj)
				break
			}
		}
	}
	if gcsURI == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no playable video found for this session"})
		return
	}

	logger.Log.Info("Retrying video analysis",
		zap.String("session_id", req.SessionID),
		zap.Uint("profile_id", req.ProfileID),
		zap.String("gcs_uri", gcsURI))

	task, err := ctl.newVideoAnalysisTask(req.SessionID, gcsURI, worker.WorkoutTypeWOD, nil, nil, req.ProfileID, false, "")
	if err != nil {
		logger.Log.Error("failed to create retry task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.enqueueSessionAutomaticTask(c, req.SessionID, req.ProfileID, task)
	if err != nil {
		if writeSessionProcessingConflict(c, err) {
			return
		}
		logger.Log.Error("failed to enqueue retry task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	c.JSON(http.StatusAccepted, RetryAnalysisResponse{
		Message:   "Analysis retry started",
		TaskID:    info.ID,
		SessionID: req.SessionID,
	})
}

func (ctl *Controller) GenerateHardSub(c *gin.Context) {
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not configured"})
		return
	}

	var req GenerateHardSubRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.SessionID = sanitizeIdentifier(req.SessionID)
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
	if !ctl.requireSessionProcessing(c, req.SessionID, req.ProfileID, true) {
		return
	}

	logger.Log.Info("Enqueuing hardsub generation",
		zap.String("session_id", req.SessionID),
		zap.Uint("profile_id", req.ProfileID))

	task, err := ctl.newGenerateHardSub(req.SessionID, req.ProfileID, req.EnableTTS)
	if err != nil {
		logger.Log.Error("failed to create hardsub task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.enqueueSessionAutomaticTask(c, req.SessionID, req.ProfileID, task)
	if err != nil {
		if writeSessionProcessingConflict(c, err) {
			return
		}
		logger.Log.Error("failed to enqueue hardsub task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	c.JSON(http.StatusAccepted, GenerateHardSubResponse{
		Message:   "Hardsub generation started",
		TaskID:    info.ID,
		SessionID: req.SessionID,
	})
}
