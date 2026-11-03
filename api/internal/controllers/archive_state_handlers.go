package controllers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

type archiveStateRequest struct {
	ProfileID  uint     `json:"profile_id" binding:"required"`
	SessionIDs []string `json:"session_ids" binding:"required,min=1,max=100"`
}
type sessionArchiveState struct {
	SessionID  string     `json:"session_id"`
	State      string     `json:"state"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

func (ctl *Controller) GetSessionArchiveStates(c *gin.Context) {
	var req archiveStateRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id and 1 to 100 session_ids are required"})
		return
	}
	if !ctl.assertOwnsProfile(c, req.ProfileID) {
		return
	}
	ids := make([]string, 0, len(req.SessionIDs))
	seen := map[string]bool{}
	for _, id := range req.SessionIDs {
		if !isValidSessionID(id) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id"})
			return
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	var states []sessionArchiveState
	var revision int64
	err := ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY").Error; err != nil {
			return err
		}
		var profile db.Profile
		if err := tx.Select("workout_history_revision").First(&profile, req.ProfileID).Error; err != nil {
			return err
		}
		revision = profile.WorkoutHistoryRevision
		type ownership struct {
			SessionID  string
			ProfileID  uint
			ArchivedAt *time.Time
		}
		var rows []ownership
		if err := tx.Raw(`SELECT session_id, profile_id, archived_at FROM analysis_results WHERE session_id IN ? UNION ALL SELECT session_id, profile_id, NULL::timestamptz AS archived_at FROM sessions WHERE session_id IN ? UNION ALL SELECT session_id, profile_id, NULL::timestamptz AS archived_at FROM chunk_analysis_results WHERE session_id IN ?`, ids, ids, ids).Scan(&rows).Error; err != nil {
			return err
		}
		byID := map[string][]ownership{}
		for _, row := range rows {
			byID[row.SessionID] = append(byID[row.SessionID], row)
		}
		states = make([]sessionArchiveState, 0, len(ids))
		for _, id := range ids {
			state := sessionArchiveState{SessionID: id, State: "not_registered"}
			for _, row := range byID[id] {
				if row.ProfileID != req.ProfileID {
					state.State = "unavailable"
					state.ArchivedAt = nil
					break
				}
				state.State = "active"
				if row.ArchivedAt != nil {
					state.ArchivedAt = row.ArchivedAt
				}
			}
			if state.State != "unavailable" && state.ArchivedAt != nil {
				state.State = "archived"
			}
			states = append(states, state)
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to check archive states"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"checked_at": time.Now().UTC(), "history_revision": strconv.FormatInt(revision, 10), "states": states})
}
