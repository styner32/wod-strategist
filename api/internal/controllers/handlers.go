package controllers

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/subtitle"
	"go.uber.org/zap"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	var req CreateUploadURLRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.SessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}
	if !isValidSessionID(req.SessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}
	cleanFilename := sanitizeObjectPart(req.Filename, "")
	if cleanFilename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename is required"})
		return
	}
	if strings.HasPrefix(cleanFilename, "sensor_telemetry_v") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reserved filename prefix: use sensor upload endpoint"})
		return
	}

	if req.ProfileID > 0 && !ctl.assertOwnsProfile(c, req.ProfileID) {
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

// GET /analysis/:session_id
func (ctl *Controller) GetAnalysis(c *gin.Context) {
	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" || !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}
	if !ctl.assertOwnsSession(c, sessionID) {
		return
	}

	results, err := ctl.analysisResults.FindBySessionID(c.Request.Context(), sessionID)
	if err != nil {
		logger.Log.Error("failed to fetch results", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch results"})
		return
	}

	c.JSON(http.StatusOK, normalizeHighlightResultsForResponseWithSchema(results, workoutLoadSchemaVersion(c)))
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

	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" || !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}
	if !ctl.assertOwnsSession(c, sessionID) {
		return
	}

	results, err := ctl.analysisResults.FindChunksBySessionID(c.Request.Context(), sessionID)
	if err != nil {
		logger.Log.Error("failed to fetch chunk results", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chunk results"})
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

func isValidGCSURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Host != "" && u.Scheme == "gs"
}

func isValidSessionGCSURI(gcsURI string, profileID uint, sessionID string) bool {
	if !strings.HasPrefix(gcsURI, "gs://") {
		return false
	}
	u, err := url.Parse(gcsURI)
	if err != nil || u.Host == "" {
		return false
	}
	objectName := strings.TrimPrefix(u.Path, "/")
	allowedPrefixes := []string{
		fmt.Sprintf("videos/%d/%s/", profileID, sessionID),
		fmt.Sprintf("videos/0/%s/", sessionID),
		fmt.Sprintf("videos/%s_", sessionID),
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(objectName, prefix) {
			return true
		}
	}
	return false
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

	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" || !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}

	if !ctl.assertOwnsSession(c, sessionID) {
		return
	}

	chunks, err := ctl.analysisResults.FindChunksBySessionID(c.Request.Context(), sessionID)
	if err != nil {
		logger.Log.Error("failed to fetch chunk results for subtitles", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chunk results"})
		return
	}

	srt := subtitle.FormatSRT(chunks)

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.srt"`, sanitizeFilename(sessionID)))
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

	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" || !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id format"})
		return
	}
	if !ctl.assertOwnsSession(c, sessionID) {
		return
	}

	results, err := ctl.highlightResults.FindBySessionID(c.Request.Context(), sessionID)
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
	if ctl.highlightResults == nil {
		logger.Log.Error("highlight results repository is not configured")
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

	if !ctl.assertOwnsProfile(c, result.ProfileID) {
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

// workoutBlockRegex extracts the JSON content from a ```workout or ```json fenced block.
