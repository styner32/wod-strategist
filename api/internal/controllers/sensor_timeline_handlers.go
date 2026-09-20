package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
	"gorm.io/gorm"
)

type VideoMappingSegmentDTO struct {
	CaptureStartMs int64 `json:"capture_start_ms"`
	CaptureEndMs   int64 `json:"capture_end_ms"`
	MediaStartMs   int64 `json:"media_start_ms"`
	MediaEndMs     int64 `json:"media_end_ms"`
}

type VideoMappingDTO struct {
	Kind     string                   `json:"kind"`   // "merged"
	Method   string                   `json:"method"` // "chunk_linear"
	Segments []VideoMappingSegmentDTO `json:"segments"`
}

type SensorTimelineResponse struct {
	Status       string                     `json:"status"` // "none" | "pending" | "completed" | "limited" | "failed" | "unavailable"
	Reason       string                     `json:"reason,omitempty"`
	Timeline     *sensor.SensorTimelineData `json:"timeline"`
	VideoMapping VideoMappingDTO            `json:"video_mapping"`
}

// GetSensorTimeline handles GET /api/v1/sessions/:session_id/sensor-timeline?profile_id=...
func (ctl *Controller) GetSensorTimeline(c *gin.Context) {
	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" || !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required or invalid"})
		return
	}

	profileIDStr := c.Query("profile_id")
	profileID, _ := strconv.ParseUint(profileIDStr, 10, 32)
	if profileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id query parameter is required"})
		return
	}

	userID := UserIDFromContext(c)
	ctx := c.Request.Context()

	status, authErr := ctl.verifySensorSessionOwnership(ctx, ctl.db, sessionID, uint(profileID), userID)
	if authErr != nil {
		c.JSON(status, gin.H{"error": authErr.Error()})
		return
	}

	// 1. Build video mapping dynamically from verified chunk_analysis_results
	var chunks []db.ChunkAnalysisResult
	_ = ctl.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("start_secs ASC, id ASC").
		Find(&chunks).Error

	segments := make([]VideoMappingSegmentDTO, 0, len(chunks))
	for _, ch := range chunks {
		if ch.StartSecs == nil || ch.EndSecs == nil || ch.MediaStartSecs == nil || ch.MediaEndSecs == nil {
			continue
		}
		if math.IsNaN(*ch.StartSecs) || math.IsNaN(*ch.EndSecs) || math.IsNaN(*ch.MediaStartSecs) || math.IsNaN(*ch.MediaEndSecs) ||
			math.IsInf(*ch.StartSecs, 0) || math.IsInf(*ch.EndSecs, 0) || math.IsInf(*ch.MediaStartSecs, 0) || math.IsInf(*ch.MediaEndSecs, 0) {
			continue
		}
		if *ch.EndSecs <= *ch.StartSecs || *ch.MediaEndSecs <= *ch.MediaStartSecs {
			continue
		}

		cStart := int64(math.Round(*ch.StartSecs * 1000))
		cEnd := int64(math.Round(*ch.EndSecs * 1000))
		mStart := int64(math.Round(*ch.MediaStartSecs * 1000))
		mEnd := int64(math.Round(*ch.MediaEndSecs * 1000))

		// Monotonicity and overlap check
		if len(segments) > 0 {
			prev := segments[len(segments)-1]
			if cStart < prev.CaptureEndMs || mStart < prev.MediaEndMs {
				// Ambiguous / overlapping mapping; exclude to prevent erroneous seeks
				continue
			}
		}

		segments = append(segments, VideoMappingSegmentDTO{
			CaptureStartMs: cStart,
			CaptureEndMs:   cEnd,
			MediaStartMs:   mStart,
			MediaEndMs:     mEnd,
		})
	}

	videoMapping := VideoMappingDTO{
		Kind:     "merged",
		Method:   "chunk_linear",
		Segments: segments,
	}

	// 2. Query analysis_results for session sensor state and timeline
	var row db.AnalysisResult
	err := ctl.db.WithContext(ctx).
		Select("id, session_id, profile_id, sensor_state, sensor_version, sensor_processing, sensor_summary, sensor_timeline").
		Where("session_id = ? AND archived_at IS NULL", sessionID).
		First(&row).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusOK, SensorTimelineResponse{
				Status:       "none",
				Timeline:     nil,
				VideoMapping: videoMapping,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	if row.SensorState == db.SensorStateNone || row.SensorVersion == 0 {
		c.JSON(http.StatusOK, SensorTimelineResponse{
			Status:       "none",
			Timeline:     nil,
			VideoMapping: videoMapping,
		})
		return
	}

	switch row.SensorState {
	case db.SensorStateUploading, db.SensorStatePending, db.SensorStateRunning:
		c.JSON(http.StatusOK, SensorTimelineResponse{
			Status:       "pending",
			Timeline:     nil,
			VideoMapping: videoMapping,
		})
		return

	case db.SensorStateFailed, db.SensorStateExpired:
		reason := "processing_failed"
		var proc struct {
			LastErrorCode *string `json:"last_error_code"`
		}
		if len(row.SensorProcessing) > 0 && string(row.SensorProcessing) != "{}" {
			if json.Unmarshal(row.SensorProcessing, &proc) == nil && proc.LastErrorCode != nil {
				reason = *proc.LastErrorCode
			}
		}
		c.JSON(http.StatusOK, SensorTimelineResponse{
			Status:       "failed",
			Reason:       reason,
			Timeline:     nil,
			VideoMapping: videoMapping,
		})
		return

	case db.SensorStateCompleted:
		// Check if sensor_timeline exists
		if len(row.SensorTimeline) == 0 || string(row.SensorTimeline) == "{}" || string(row.SensorTimeline) == "null" {
			c.JSON(http.StatusOK, SensorTimelineResponse{
				Status:       "unavailable",
				Reason:       "timeline_not_generated",
				Timeline:     nil,
				VideoMapping: videoMapping,
			})
			return
		}

		// Check if timeline records a processing failure
		var failureCheck struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if json.Unmarshal(row.SensorTimeline, &failureCheck) == nil && failureCheck.Status == "failed" {
			c.JSON(http.StatusOK, SensorTimelineResponse{
				Status:       "failed",
				Reason:       failureCheck.Error,
				Timeline:     nil,
				VideoMapping: videoMapping,
			})
			return
		}

		var timeline sensor.SensorTimelineData
		if err := json.Unmarshal(row.SensorTimeline, &timeline); err != nil {
			c.JSON(http.StatusOK, SensorTimelineResponse{
				Status:       "unavailable",
				Reason:       "corrupt_timeline",
				Timeline:     nil,
				VideoMapping: videoMapping,
			})
			return
		}

		// Verify timeline is not stale (matches current completed version)
		expectedVer := fmt.Sprintf("%d", row.SensorVersion)
		if timeline.Source.SensorVersion != "" && timeline.Source.SensorVersion != expectedVer {
			c.JSON(http.StatusOK, SensorTimelineResponse{
				Status:       "unavailable",
				Reason:       "stale_timeline",
				Timeline:     nil,
				VideoMapping: videoMapping,
			})
			return
		}

		// Evaluate quality status (limited if HR coverage/quality is limited)
		responseStatus := "completed"
		var summary struct {
			Quality struct {
				ValidHR bool `json:"valid_hr"`
			} `json:"quality"`
		}
		if len(row.SensorSummary) > 0 && json.Unmarshal(row.SensorSummary, &summary) == nil {
			if !summary.Quality.ValidHR {
				responseStatus = "limited"
			}
		}

		c.JSON(http.StatusOK, SensorTimelineResponse{
			Status:       responseStatus,
			Timeline:     &timeline,
			VideoMapping: videoMapping,
		})
		return

	default:
		c.JSON(http.StatusOK, SensorTimelineResponse{
			Status:       "unavailable",
			Reason:       "unknown_sensor_state",
			Timeline:     nil,
			VideoMapping: videoMapping,
		})
		return
	}
}
