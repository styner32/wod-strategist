package controllers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
)

func (ctl *Controller) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
		"commit": ctl.gitCommit,
	})
}

func (ctl *Controller) CreateUploadURL(c *gin.Context) {
	if ctl.storageClient == nil {
		logger.Log.Error("storage client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Filename  string `json:"filename"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if sanitizeObjectPart(req.SessionID, "") == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}
	if sanitizeObjectPart(req.Filename, "") == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename is required"})
		return
	}

	objectName := buildVideoObjectName(req.SessionID, req.Filename)
	signedURL, err := ctl.storageClient.GenerateSignedURL(objectName, http.MethodPut, 15*time.Minute)
	if err != nil {
		logger.Log.Error("failed to generate signed URL", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_url": signedURL,
		"gcs_uri":    fmt.Sprintf("gs://%s/%s", ctl.bucketName, objectName),
	})
}

func (ctl *Controller) CompleteUpload(c *gin.Context) {
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	var req struct {
		SessionID   string   `json:"session_id"`
		GCSURI      string   `json:"gcs_uri"`
		Movements   []string `json:"movements"`
		Injuries    []string `json:"injuries"`
		WorkoutType string   `json:"workout_type"`
	}

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
	if req.GCSURI == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gcs_uri is required"})
		return
	}
	if req.Movements == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "movements is required"})
		return
	}

	if !isValidGCSURI(req.GCSURI) {
		logger.Log.Error("invalid GCS URI: must be a valid gs:// URI with a bucket", zap.String("uri", req.GCSURI))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid GCS URI"})
		return
	}

	if len(req.Movements) >= 100 {
		logger.Log.Error("too many movements", zap.Int("count", len(req.Movements)))
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many movements"})
		return
	}
	if !allowedMovements.containsAll(req.Movements) {
		logger.Log.Error("invalid movements", zap.Strings("movements", req.Movements))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid movements"})
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

	if !worker.IsValidWorkoutType(req.WorkoutType) {
		logger.Log.Error("invalid workout type", zap.String("workout_type", req.WorkoutType))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workout type"})
		return
	}

	workoutType := worker.NormalizeWorkoutType(req.WorkoutType)
	logger.Log.Info("Submit a video analysis request",
		zap.String("session_id", req.SessionID),
		zap.String("gcs_uri", req.GCSURI),
		zap.Strings("movements", req.Movements),
		zap.Strings("injuries", req.Injuries),
		zap.String("workout_type", workoutType),
	)

	task, err := ctl.newVideoAnalysisTask(req.SessionID, req.GCSURI, workoutType, req.Movements, req.Injuries)
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

func (ctl *Controller) Upload(c *gin.Context) {
	if ctl.storageClient == nil {
		logger.Log.Error("storage client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
		return
	}
	if ctl.queueClient == nil {
		logger.Log.Error("queue client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	sessionID := sanitizeIdentifier(c.PostForm("session_id"))
	if sessionID == "" {
		logger.Log.Error("session_id is required")
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
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

	objectName := buildVideoObjectName(sessionID, fileHeader.Filename)
	gcsURI, err := ctl.storageClient.UploadFile(c.Request.Context(), file, objectName)
	if err != nil {
		logger.Log.Error("failed to upload file to GCS", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
		return
	}

	task, err := ctl.newVideoAnalysisTask(sessionID, gcsURI, worker.WorkoutTypeWOD, nil, nil)
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
		"message":    "File uploaded and analysis started",
		"task_id":    info.ID,
		"session_id": sessionID,
		"file_url":   gcsURI,
	})
}

func (ctl *Controller) GetAnalysis(c *gin.Context) {
	if ctl.analysisResults == nil {
		logger.Log.Error("analysis result repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch results"})
		return
	}

	results, err := ctl.analysisResults.FindBySessionID(c.Request.Context(), c.Param("session_id"))
	if err != nil {
		logger.Log.Error("failed to fetch results", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch results"})
		return
	}

	c.JSON(http.StatusOK, results)
}

func (ctl *Controller) GetHistory(c *gin.Context) {
	if ctl.analysisResults == nil {
		logger.Log.Error("analysis result repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}

	results, err := ctl.analysisResults.ListRecent(c.Request.Context(), 20)
	if err != nil {
		logger.Log.Error("failed to fetch history", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}

	c.JSON(http.StatusOK, results)
}

func (ctl *Controller) ListMovements(c *gin.Context) {
	c.JSON(http.StatusOK, append([]string(nil), movements...))
}

func (ctl *Controller) ListInjuries(c *gin.Context) {
	c.JSON(http.StatusOK, append([]string(nil), injuries...))
}

func buildVideoObjectName(sessionID string, filename string) string {
	return fmt.Sprintf(
		"videos/%s_%s",
		sanitizeObjectPart(sessionID, "session"),
		sanitizeObjectPart(filename, "upload.bin"),
	)
}

func sanitizeIdentifier(value string) string {
	return sanitizeObjectPart(value, "")
}

func trimRequiredString(value string) string {
	return strings.TrimSpace(value)
}

func isValidGCSURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Host != "" && u.Scheme == "gs"
}
