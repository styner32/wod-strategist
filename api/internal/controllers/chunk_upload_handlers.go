package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

// @Summary      Chunk Complete
// @Description  Notifies the backend that a chunk upload is complete and triggers chunk analysis
// @Tags         upload
// @Accept       json
// @Produce      json
// @Param        request body ChunkCompleteRequest true "Upload metadata"
// @Success      202 {object} RecordingMediaChunkReceipt
// @Router       /chunk-complete [post]
func (ctl *Controller) ChunkComplete(c *gin.Context) {
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

	if !ctl.requireSessionProcessing(c, req.SessionID, req.ProfileID, true) {
		return
	}

	ctl.handleRecordingMediaChunk(c, req)
}
