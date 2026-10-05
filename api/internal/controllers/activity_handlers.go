package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

// GetActivitySummary returns deterministic counts, never legacy rep estimates.
// @Summary Get observed workout activity
// @Tags analysis
// @Produce json
// @Param session_id path string true "Session ID"
// @Param profile_id query int true "Owned profile ID"
// @Success 200 {object} activity.Summary
// @Router /sessions/{session_id}/activity-summary [get]
func (ctl *Controller) GetActivitySummary(c *gin.Context) {
	sid := c.Param("session_id")
	pid, err := strconv.ParseUint(c.Query("profile_id"), 10, 32)
	if !isValidSessionID(sid) || err != nil || pid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid session_id and profile_id are required"})
		return
	}
	if !ctl.assertOwnsProfile(c, uint(pid)) || !ctl.assertOwnsSession(c, sid) {
		return
	}
	var session db.Session
	err = ctl.db.WithContext(c.Request.Context()).Where("session_id = ?", sid).First(&session).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "activity summary unavailable"})
		return
	}
	if err == nil && session.ProfileID != uint(pid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	var rows []db.ChunkAnalysisResult
	if err = ctl.db.WithContext(c.Request.Context()).Where("session_id = ? AND profile_id = ?", sid, pid).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "activity summary unavailable"})
		return
	}
	var stored activity.Summary
	_ = json.Unmarshal(session.ActivitySummary, &stored)
	c.JSON(http.StatusOK, activity.Build(rows, &stored))
}
