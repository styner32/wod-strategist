package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/subtitle"
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

	objectName := buildVideoObjectName(req.ProfileID, req.SessionID, req.Filename)
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

	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}

	if !isValidGCSURI(req.GCSURI) {
		logger.Log.Error("invalid GCS URI: must be a valid gs:// URI with a bucket", zap.String("uri", req.GCSURI))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid GCS URI"})
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

	task, err := ctl.newVideoAnalysisTask(req.SessionID, req.GCSURI, workoutType, req.Movements, req.Injuries, req.ProfileID, req.EnableTTS)
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

	// Legacy multipart upload path — no profile_id available, use 0
	objectName := buildVideoObjectName(0, sessionID, fileHeader.Filename)
	gcsURI, err := ctl.storageClient.UploadFile(c.Request.Context(), file, objectName)
	if err != nil {
		logger.Log.Error("failed to upload file to GCS", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
		return
	}

	task, err := ctl.newVideoAnalysisTask(sessionID, gcsURI, worker.WorkoutTypeWOD, nil, nil, 0, false)
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

	pidStr := c.Query("profile_id")
	if pidStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}
	pid, err := strconv.ParseUint(pidStr, 10, 32)
	if err != nil || pid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile_id"})
		return
	}
	profileID := uint(pid)

	results, err := ctl.analysisResults.ListRecent(c.Request.Context(), 20, profileID)
	if err != nil {
		logger.Log.Error("failed to fetch history", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}

	// Enrich with available video kinds per session (best-effort)
	// TODO(perf): This does one GCS ListObjects call per unique session — O(N) RPCs.
	// Replace with a DB column (e.g. analysis_results.available_videos TEXT[] or a
	// session_videos table) that workers update when they produce merged/hardsubbed/encoded
	// files. Then this becomes a single SQL query instead of N GCS list calls.
	videoKinds := map[string][]string{} // sessionID → ["merged", "encoded", ...]
	if ctl.storageClient != nil {
		// Collect unique session IDs
		seen := map[string]bool{}
		for _, r := range results {
			if r.SessionID != "" && !seen[r.SessionID] {
				seen[r.SessionID] = true
			}
		}
		for sessionID := range seen {
			prefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
			objectInfos, listErr := ctl.storageClient.ListObjectInfos(c.Request.Context(), prefix)
			if listErr != nil {
				continue
			}
			var kinds []string
			for _, info := range objectInfos {
				base := filepath.Base(info.Name)
				switch {
				case base == "merged.mp4" || strings.Contains(base, "_merged_"):
					if !sliceContains(kinds, "merged") {
						kinds = append(kinds, "merged")
					}
				case base == "hardsubbed.mp4" || strings.Contains(base, "_hardsubbed_"):
					if !sliceContains(kinds, "hardsubbed") {
						kinds = append(kinds, "hardsubbed")
					}
				case strings.Contains(base, "_encoded"):
					if !sliceContains(kinds, "encoded") {
						kinds = append(kinds, "encoded")
					}
				}
			}
			if len(kinds) > 0 {
				videoKinds[sessionID] = kinds
			}
		}
	}

	// Build enriched response
	type historyItem struct {
		db.AnalysisResult
		AvailableVideos []string `json:"available_videos,omitempty"`
	}
	enriched := make([]historyItem, len(results))
	for i, r := range results {
		enriched[i] = historyItem{
			AnalysisResult:  r,
			AvailableVideos: videoKinds[r.SessionID],
		}
	}

	c.JSON(http.StatusOK, enriched)
}

func (ctl *Controller) ArchiveHistory(c *gin.Context) {
	if ctl.analysisResults == nil {
		logger.Log.Error("analysis result repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to archive history"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := ctl.analysisResults.Archive(c.Request.Context(), uint(id)); err != nil {
		logger.Log.Error("failed to archive history", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to archive history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "history archived"})
}

func (ctl *Controller) UnarchiveHistory(c *gin.Context) {
	if ctl.analysisResults == nil {
		logger.Log.Error("analysis result repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unarchive history"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := ctl.analysisResults.Unarchive(c.Request.Context(), uint(id)); err != nil {
		logger.Log.Error("failed to unarchive history", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unarchive history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "history unarchived"})
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

// @Summary      List movement groups
// @Description  Returns all supported workout movements organized by category
// @Tags         metadata
// @Produce      json
// @Success      200 {array} MovementGroup
// @Router       /movement-groups [get]
func (ctl *Controller) ListMovementGroups(c *gin.Context) {
	c.JSON(http.StatusOK, movementGroups)
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

func buildVideoObjectName(profileID uint, sessionID string, filename string) string {
	return fmt.Sprintf(
		"videos/%d/%s/%s",
		profileID,
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

func sliceContains(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
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

	var req ChunkCompleteRequest

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

	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}

	workoutType := worker.NormalizeWorkoutType(req.WorkoutType)

	task, err := ctl.newChunkAnalysisTask(req.SessionID, req.GCSURI, workoutType, req.Movements, req.Injuries, req.ProfileID, req.StartSecs, req.EndSecs, req.HeartRateBPM)
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
		logger.Log.Error("failed to bind JSON for profile creation", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Validate injuries if provided
	if req.Injuries == nil {
		req.Injuries = []string{}
	}
	if !allowedInjuries.containsAll(req.Injuries) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid injuries"})
		return
	}

	injuriesJSON, err := json.Marshal(req.Injuries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode injuries"})
		return
	}

	fitnessLevel := req.FitnessLevel
	if fitnessLevel == "" {
		fitnessLevel = "intermediate"
	}

	profile := &db.Profile{
		Name:         req.Name,
		BirthYear:    req.BirthYear,
		BirthMonth:   req.BirthMonth,
		BirthDay:     req.BirthDay,
		Gender:       req.Gender,
		HeightCm:     req.HeightCm,
		WeightKg:     req.WeightKg,
		FitnessLevel: fitnessLevel,
		Injuries:     string(injuriesJSON),
	}

	if err := ctl.profiles.Create(c.Request.Context(), profile); err != nil {
		logger.Log.Error("failed to create profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create profile"})
		return
	}

	c.JSON(http.StatusCreated, toProfileResponse(profile))
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

	c.JSON(http.StatusOK, toProfileResponse(profile))
}

func (ctl *Controller) ListProfiles(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	includeArchived := c.Query("include_archived") == "true"

	profiles, err := ctl.profiles.ListAll(c.Request.Context(), includeArchived)
	if err != nil {
		logger.Log.Error("failed to list profiles", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list profiles"})
		return
	}

	resp := make([]ProfileResponse, len(profiles))
	for i := range profiles {
		resp[i] = toProfileResponse(&profiles[i])
	}

	c.JSON(http.StatusOK, resp)
}

func (ctl *Controller) UpdateProfile(c *gin.Context) {
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

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("failed to parse request body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Name != nil {
		profile.Name = *req.Name
	}
	if req.BirthYear != nil {
		profile.BirthYear = *req.BirthYear
	}
	if req.BirthMonth != nil {
		profile.BirthMonth = *req.BirthMonth
	}
	if req.BirthDay != nil {
		profile.BirthDay = *req.BirthDay
	}
	if req.Gender != nil {
		profile.Gender = *req.Gender
	}
	if req.HeightCm != nil {
		profile.HeightCm = *req.HeightCm
	}
	if req.WeightKg != nil {
		profile.WeightKg = *req.WeightKg
	}
	if req.FitnessLevel != nil {
		profile.FitnessLevel = *req.FitnessLevel
	}
	if req.Injuries != nil {
		if !allowedInjuries.containsAll(req.Injuries) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid injuries"})
			return
		}
		injuriesJSON, err := json.Marshal(req.Injuries)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode injuries"})
			return
		}
		profile.Injuries = string(injuriesJSON)
	}

	if err := ctl.profiles.Update(c.Request.Context(), profile); err != nil {
		logger.Log.Error("failed to update profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}

	c.JSON(http.StatusOK, toProfileResponse(profile))
}

func (ctl *Controller) ArchiveProfile(c *gin.Context) {
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

	if err := ctl.profiles.Archive(c.Request.Context(), uint(id)); err != nil {
		logger.Log.Error("failed to archive profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to archive profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "profile archived"})
}

func (ctl *Controller) UnarchiveProfile(c *gin.Context) {
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

	if err := ctl.profiles.Unarchive(c.Request.Context(), uint(id)); err != nil {
		logger.Log.Error("failed to unarchive profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unarchive profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "profile unarchived"})
}

func toProfileResponse(p *db.Profile) ProfileResponse {
	var injuryList []string
	if p.Injuries != "" {
		_ = json.Unmarshal([]byte(p.Injuries), &injuryList)
	}
	if injuryList == nil {
		injuryList = []string{}
	}

	resp := ProfileResponse{
		ID:           p.ID,
		Name:         p.Name,
		BirthYear:    p.BirthYear,
		BirthMonth:   p.BirthMonth,
		BirthDay:     p.BirthDay,
		Gender:       p.Gender,
		HeightCm:     p.HeightCm,
		WeightKg:     p.WeightKg,
		FitnessLevel: p.FitnessLevel,
		Injuries:     injuryList,
	}
	if p.ArchivedAt != nil {
		ts := p.ArchivedAt.Format(time.RFC3339)
		resp.ArchivedAt = &ts
	}
	return resp
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

	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}

	workoutType := worker.NormalizeWorkoutType(req.WorkoutType)

	// The merge task uses profileID + sessionID to discover chunks in GCS.
	// prefix = "videos/{profileId}/{sessionId}/"
	placeholderGCSURI := fmt.Sprintf("gs://%s/videos/%d/%s/", ctl.bucketName, req.ProfileID, req.SessionID)

	task, err := ctl.newMergeChunksTask(req.SessionID, placeholderGCSURI, workoutType, req.Movements, req.Injuries, req.ProfileID, req.EnableTTS)
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

// @Summary      Get Subtitles
// @Description  Returns chunk analysis feedback as an SRT subtitle file for a given session
// @Tags         analysis
// @Produce      text/plain
// @Param        session_id path string true "Session ID"
// @Success      200 {string} string "SRT subtitle content"
// @Failure      500 {object} ErrorResponse
// @Router       /subtitles/:session_id [get]
func (ctl *Controller) GetSubtitles(c *gin.Context) {
	if ctl.analysisResults == nil {
		logger.Log.Error("analysis result repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch results"})
		return
	}

	sessionID := c.Param("session_id")

	chunks, err := ctl.analysisResults.FindChunksBySessionID(c.Request.Context(), sessionID)
	if err != nil {
		logger.Log.Error("failed to fetch chunk results for subtitles", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chunk results"})
		return
	}

	srt := subtitle.FormatSRT(chunks)

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.srt"`, sessionID))
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(srt))
}

// @Summary      Generate Highlight
// @Description  Triggers short-form highlight video generation from WOD analysis for a session
// @Tags         highlight
// @Accept       json
// @Produce      json
// @Param        request body GenerateHighlightRequest true "Session ID and options"
// @Success      202 {object} map[string]string
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /generate-highlight [post]
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

	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
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

	info, err := ctl.queueClient.Enqueue(task)
	if err != nil {
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

// @Summary      Get Highlight
// @Description  Returns highlight generation results for a given session
// @Tags         highlight
// @Produce      json
// @Param        session_id path string true "Session ID"
// @Success      200 {array} db.HighlightResult
// @Failure      500 {object} ErrorResponse
// @Router       /highlight/:session_id [get]
func (ctl *Controller) GetHighlight(c *gin.Context) {
	if ctl.highlightResults == nil {
		logger.Log.Error("highlight result repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch highlights"})
		return
	}

	results, err := ctl.highlightResults.FindBySessionID(c.Request.Context(), c.Param("session_id"))
	if err != nil {
		logger.Log.Error("failed to fetch highlight results", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch highlights"})
		return
	}

	c.JSON(http.StatusOK, results)
}

// @Summary      Get Highlight Download URL
// @Description  Returns a time-limited signed URL for downloading a specific highlight video
// @Tags         highlight
// @Produce      json
// @Param        id path string true "Highlight result ID"
// @Success      200 {object} VideoDownloadURLResponse
// @Failure      400 {object} ErrorResponse
// @Failure      404 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /highlight-download/:id [get]
func (ctl *Controller) GetHighlightDownloadURL(c *gin.Context) {
	if ctl.highlightResults == nil || ctl.storageClient == nil {
		logger.Log.Error("highlight results or storage client not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not configured"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid highlight id"})
		return
	}

	result, err := ctl.highlightResults.FindByID(c.Request.Context(), uint(id))
	if err != nil {
		logger.Log.Error("highlight result not found", zap.Uint64("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "highlight not found"})
		return
	}

	if result.Status != "COMPLETED" || result.GCSURI == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "highlight video not available"})
		return
	}

	// Extract object name from gs://bucket/object format
	objectName := extractGCSObjectName(result.GCSURI)
	if objectName == "" {
		logger.Log.Error("invalid GCS URI in highlight result", zap.String("gcs_uri", result.GCSURI))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid highlight storage path"})
		return
	}

	signedURL, err := ctl.storageClient.GenerateSignedURL(objectName, http.MethodGet, 15*time.Minute)
	if err != nil {
		logger.Log.Error("failed to generate highlight download URL",
			zap.Uint("id", result.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate download URL"})
		return
	}

	filename := fmt.Sprintf("%s_highlight_%d.mp4", result.SessionID, result.ID)
	c.JSON(http.StatusOK, VideoDownloadURLResponse{
		SessionID:   result.SessionID,
		Kind:        "highlight",
		DownloadURL: signedURL,
		Filename:    filename,
		ExpiresAt:   time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339),
	})
}

// extractGCSObjectName extracts the object path from a gs://bucket/object URI.
func extractGCSObjectName(gcsURI string) string {
	u, err := url.Parse(gcsURI)
	if err != nil || u.Scheme != "gs" || u.Host == "" {
		return ""
	}
	// u.Path starts with "/" so trim it
	return strings.TrimPrefix(u.Path, "/")
}

// @Summary      Verify Highlights
// @Description  Triggers verification of highlight segments to detect hallucinated movements
// @Tags         highlight
// @Accept       json
// @Produce      json
// @Param        request body VerifyHighlightsRequest true "Session ID"
// @Success      202 {object} map[string]string
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /verify-highlights [post]
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

	logger.Log.Info("Submit a highlight verification request",
		zap.String("session_id", req.SessionID))

	task, err := ctl.newVerifyHighlightsTask(req.SessionID)
	if err != nil {
		logger.Log.Error("failed to create verify highlights task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.queueClient.Enqueue(task)
	if err != nil {
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

// @Summary      Get Video Download URL
// @Description  Returns a time-limited signed URL for downloading the merged or hardsubbed video
// @Tags         video
// @Produce      json
// @Param        session_id path string true "Session ID"
// @Param        kind query string false "Video kind: merged (default) or hardsubbed"
// @Success      200 {object} VideoDownloadURLResponse
// @Failure      400 {object} ErrorResponse
// @Failure      404 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /video-download/:session_id [get]
func (ctl *Controller) GetVideoDownloadURL(c *gin.Context) {
	if ctl.storageClient == nil {
		logger.Log.Error("storage client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not configured"})
		return
	}

	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}

	profileIDStr := c.DefaultQuery("profile_id", "0")
	var profileID uint
	if _, err := fmt.Sscanf(profileIDStr, "%d", &profileID); err != nil || profileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id query param is required"})
		return
	}

	kind := strings.ToLower(strings.TrimSpace(c.DefaultQuery("kind", "merged")))
	if kind != "merged" && kind != "hardsubbed" && kind != "encoded" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be 'merged', 'hardsubbed', or 'encoded'"})
		return
	}

	// List all objects under the session directory: videos/{profileId}/{sessionId}/
	prefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
	objectInfos, err := ctl.storageClient.ListObjectInfos(c.Request.Context(), prefix)
	if err != nil {
		logger.Log.Error("failed to list session objects", zap.String("session_id", sessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up video"})
		return
	}

	// In the new layout, files are named simply: merged.mp4, hardsubbed.mp4
	// For "encoded", match any file containing "_encoded" (e.g. vid_xxx_encoded.mp4)
	var bestObject string
	var bestCreated time.Time
	for _, info := range objectInfos {
		base := filepath.Base(info.Name)
		var match bool
		switch kind {
		case "encoded":
			match = strings.Contains(base, "_encoded")
		default:
			newTarget := kind + ".mp4"
			oldMarker := fmt.Sprintf("_%s_", kind) // backward compat: *_merged_*.mp4
			match = base == newTarget || strings.Contains(base, oldMarker)
		}
		if match && (bestObject == "" || info.Created.After(bestCreated)) {
			bestObject = info.Name
			bestCreated = info.Created
		}
	}

	if bestObject == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("no %s video found for session", kind)})
		return
	}

	signedURL, err := ctl.storageClient.GenerateSignedURL(bestObject, http.MethodGet, 15*time.Minute)
	if err != nil {
		logger.Log.Error("failed to generate download signed URL",
			zap.String("session_id", sessionID),
			zap.String("kind", kind),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate download URL"})
		return
	}

	c.JSON(http.StatusOK, VideoDownloadURLResponse{
		SessionID:   sessionID,
		Kind:        kind,
		DownloadURL: signedURL,
		Filename:    sessionID + "_" + kind + ".mp4",
		ExpiresAt:   time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339),
	})
}

// @Summary      Retry Analysis
// @Description  Re-enqueues a video analysis task for a failed session using existing GCS files
// @Tags         analysis
// @Accept       json
// @Produce      json
// @Param        request body RetryAnalysisRequest true "Session and profile"
// @Success      202 {object} RetryAnalysisResponse
// @Failure      400 {object} ErrorResponse
// @Failure      404 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /retry-analysis [post]
func (ctl *Controller) RetryAnalysis(c *gin.Context) {
	if ctl.queueClient == nil || ctl.storageClient == nil {
		logger.Log.Error("queue or storage client is not configured")
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
	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
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

	task, err := ctl.newVideoAnalysisTask(req.SessionID, gcsURI, worker.WorkoutTypeWOD, nil, nil, req.ProfileID, false)
	if err != nil {
		logger.Log.Error("failed to create retry task", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	info, err := ctl.queueClient.Enqueue(task)
	if err != nil {
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

// @Summary      Generate Hardsubbed Video
// @Description  Creates a hardsubbed version of the video with burned-in subtitles
// @Tags         video
// @Accept       json
// @Produce      json
// @Param        request body GenerateHardSubRequest true "Session and profile"
// @Success      202 {object} GenerateHardSubResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /generate-hardsub [post]
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
	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
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

	info, err := ctl.queueClient.Enqueue(task)
	if err != nil {
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
