package controllers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

// Maximum number of telemetry samples per session (2 hours @ 1Hz).
const maxTelemetrySamples = 7200

// ---------------------------------------------------------------------------
// DTO
// ---------------------------------------------------------------------------

// DebugTelemetrySample mirrors the frontend TelemetrySample type.
type DebugTelemetrySample struct {
	Ts       float64                `json:"ts"`
	HR       *int                   `json:"hr,omitempty"`
	Batt     *float64               `json:"batt,omitempty"`
	ChunkIdx *int                   `json:"chunkIdx,omitempty"`
	Motion   map[string]interface{} `json:"motion,omitempty"`
}

// DebugTelemetryRequest is the JSON body for POST /debug/telemetry.
type DebugTelemetryRequest struct {
	SessionID   string                 `json:"sessionId"`
	ProfileID   int                    `json:"profileId"`
	StartedAt   int64                  `json:"startedAt"`
	EndedAt     int64                  `json:"endedAt"`
	Samples     []DebugTelemetrySample `json:"samples"`
	AppVersion  string                 `json:"appVersion"`
	Platform    string                 `json:"platform"`
	DeviceModel string                 `json:"deviceModel"`
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

// UploadDebugTelemetry stores a telemetry session JSON in GCS.
//
// @Summary      Upload Debug Telemetry
// @Description  Receives a telemetry session from the app and stores it in GCS for debugging
// @Tags         debug
// @Accept       json
// @Produce      json
// @Param        body body DebugTelemetryRequest true "Telemetry session"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /debug/telemetry [post]
func (ctl *Controller) UploadDebugTelemetry(c *gin.Context) {
	var req DebugTelemetryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Validation
	if req.SessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sessionId is required"})
		return
	}
	if req.ProfileID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profileId is required"})
		return
	}
	if len(req.Samples) > maxTelemetrySamples {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("too many samples (max %d)", maxTelemetrySamples),
		})
		return
	}

	// Security: Verify user owns the profile they are uploading telemetry for
	if !ctl.assertOwnsProfile(c, uint(req.ProfileID)) {
		return
	}

	if ctl.storageClient == nil {
		logger.Log.Error("storage client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not configured"})
		return
	}

	// Marshal back to JSON for GCS upload
	data, err := json.Marshal(req)
	if err != nil {
		logger.Log.Error("failed to marshal telemetry", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to process telemetry"})
		return
	}

	// Upload to GCS: debug/telemetry/{profileId}/{sessionId}.json
	objectName := fmt.Sprintf("debug/telemetry/%d/%s.json", req.ProfileID, sanitizeObjectPart(req.SessionID, "unknown"))

	_, err = ctl.storageClient.UploadFile(
		c.Request.Context(),
		newBytesFile(data),
		objectName,
	)
	if err != nil {
		logger.Log.Error("failed to upload telemetry to GCS",
			zap.String("session_id", req.SessionID),
			zap.Int("profile_id", req.ProfileID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload telemetry"})
		return
	}

	gcsURI := fmt.Sprintf("gs://%s/%s", ctl.bucketName, objectName)
	logger.Log.Info("telemetry uploaded",
		zap.String("session_id", req.SessionID),
		zap.Int("profile_id", req.ProfileID),
		zap.Int("samples", len(req.Samples)),
		zap.String("gcs_uri", gcsURI))

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"gcs_uri": gcsURI,
	})
}

// bytesFile wraps a byte slice to implement multipart.File for UploadFile.
type bytesFile struct {
	*bytes.Reader
}

func newBytesFile(data []byte) *bytesFile {
	return &bytesFile{Reader: bytes.NewReader(data)}
}

func (b *bytesFile) Close() error { return nil }
