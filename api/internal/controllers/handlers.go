package controllers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
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

// @Summary      Create Upload URL
// @Description  Generates a signed GCS upload URL for a specific video file
// @Tags         upload
// @Accept       json
// @Produce      json
// @Param        request body CreateUploadURLRequest true "Session and filename"
// @Success      200 {object} CreateUploadURLResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /upload-url [post]
func (ctl *Controller) CreateUploadURL(c *gin.Context) {
	if ctl.storageClient == nil {
		logger.Log.Error("storage client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
		return
	}

	var req CreateUploadURLRequest

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

	task, err := ctl.newVideoAnalysisTask(req.SessionID, req.GCSURI, workoutType, req.Movements, req.Injuries, req.ProfileID)
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

	task, err := ctl.newVideoAnalysisTask(sessionID, gcsURI, worker.WorkoutTypeWOD, nil, nil, 0)
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

// @Summary      Get Chunk Analysis
// @Description  Fetches the partial analysis chunks for a given session
// @Tags         analysis
// @Produce      json
// @Success      200 {array} db.ChunkAnalysisResult
// @Router       /chunk-analysis/:session_id [get]
func (ctl *Controller) GetChunkAnalysis(c *gin.Context) {
	if ctl.analysisResults == nil {
		logger.Log.Error("analysis result repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch results"})
		return
	}

	results, err := ctl.analysisResults.FindChunksBySessionID(c.Request.Context(), c.Param("session_id"))
	if err != nil {
		logger.Log.Error("failed to fetch chunk results", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chunk results"})
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

	var profileID uint
	if pidStr := c.Query("profile_id"); pidStr != "" {
		pid, err := strconv.ParseUint(pidStr, 10, 32)
		if err == nil {
			profileID = uint(pid)
		}
	}

	results, err := ctl.analysisResults.ListRecent(c.Request.Context(), 20, profileID)
	if err != nil {
		logger.Log.Error("failed to fetch history", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}

	c.JSON(http.StatusOK, results)
}

// @Summary      List supported movements
// @Description  Returns a list of all supported workout movements
// @Tags         metadata
// @Produce      json
// @Success      200 {array} string
// @Router       /movements [get]
func (ctl *Controller) ListMovements(c *gin.Context) {
	c.JSON(http.StatusOK, append([]string(nil), movements...))
}

// @Summary      List supported injuries
// @Description  Returns a list of all supported injuries that can be analyzed
// @Tags         metadata
// @Produce      json
// @Success      200 {array} string
// @Router       /injuries [get]
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

// @Summary      Chunk Complete
// @Description  Notifies the backend that a chunk upload is complete and triggers chunk analysis
// @Tags         upload
// @Accept       json
// @Produce      json
// @Param        request body CompleteUploadRequest true "Upload metadata"
// @Success      202 {object} CompleteUploadResponse
// @Router       /chunk-complete [post]
func (ctl *Controller) ChunkComplete(c *gin.Context) {
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
	if req.GCSURI == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gcs_uri is required"})
		return
	}

	if !isValidGCSURI(req.GCSURI) {
		logger.Log.Error("invalid GCS URI: must be a valid gs:// URI with a bucket", zap.String("uri", req.GCSURI))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid GCS URI"})
		return
	}

	workoutType := worker.NormalizeWorkoutType(req.WorkoutType)

	task, err := ctl.newChunkAnalysisTask(req.SessionID, req.GCSURI, workoutType, req.Movements, req.Injuries, req.ProfileID)
	if err != nil {
		logger.Log.Error("failed to create chunk task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
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

// ==========================================
// Profile Handlers
// ==========================================

func (ctl *Controller) CreateProfile(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	var req CreateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	profile := &db.Profile{
		BirthYear:  req.BirthYear,
		BirthMonth: req.BirthMonth,
		BirthDay:   req.BirthDay,
		Gender:     req.Gender,
		HeightCm:   req.HeightCm,
		WeightKg:   req.WeightKg,
	}

	if err := ctl.profiles.Create(c.Request.Context(), profile); err != nil {
		logger.Log.Error("failed to create profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create profile"})
		return
	}

	c.JSON(http.StatusCreated, ProfileResponse{
		ID:         profile.ID,
		BirthYear:  profile.BirthYear,
		BirthMonth: profile.BirthMonth,
		BirthDay:   profile.BirthDay,
		Gender:     profile.Gender,
		HeightCm:   profile.HeightCm,
		WeightKg:   profile.WeightKg,
	})
}

func (ctl *Controller) GetProfile(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile id"})
		return
	}

	profile, err := ctl.profiles.FindByID(c.Request.Context(), uint(id))
	if err != nil {
		logger.Log.Error("failed to find profile", zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}

	c.JSON(http.StatusOK, ProfileResponse{
		ID:         profile.ID,
		BirthYear:  profile.BirthYear,
		BirthMonth: profile.BirthMonth,
		BirthDay:   profile.BirthDay,
		Gender:     profile.Gender,
		HeightCm:   profile.HeightCm,
		WeightKg:   profile.WeightKg,
	})
}
