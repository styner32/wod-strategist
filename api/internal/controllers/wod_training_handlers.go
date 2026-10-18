package controllers

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/wodeval"
)

type wodTrainingResponse struct {
	Eligible    bool           `json:"eligible"`
	Effort      *int           `json:"effort"`
	Start       *wodeval.Start `json:"wod_start"`
	SessionLoad *float64       `json:"session_load"`
	Score       *wodeval.Score `json:"score"`
}

func (ctl *Controller) GetWODTraining(c *gin.Context) {
	sid, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	pid, ok := ctl.resolveFeedbackSession(c, sid)
	if !ok {
		return
	}
	var s db.Session
	if err := ctl.db.WithContext(c.Request.Context()).Where("session_id = ? AND profile_id = ?", sid, pid).First(&s).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load workout"})
			return
		}
		c.JSON(http.StatusOK, wodTrainingResponse{})
		return
	}
	out := wodTrainingResponse{Eligible: s.WorkoutType == "wod" && s.WODEvaluationVersion == wodeval.Version, Effort: s.Effort}
	if out.Eligible {
		var a db.AnalysisResult
		if ctl.db.WithContext(c.Request.Context()).Where("session_id = ? AND profile_id = ? AND status = ? AND archived_at IS NULL", sid, pid, "COMPLETED").First(&a).Error == nil {
			if score, ok := wodeval.Decode(a.SessionScore); ok {
				out.Score = &score
				out.Start = &score.WODStart
				out.SessionLoad = wodeval.SessionLoad(wodeval.Workout{Effort: s.Effort, Start: score.WODStart})
			}
		}
	}
	c.JSON(http.StatusOK, out)
}

func (ctl *Controller) UpdateWODEffort(c *gin.Context) {
	sid, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	pid, ok := ctl.resolveFeedbackSession(c, sid)
	if !ok {
		return
	}
	var req struct {
		Effort *int `json:"effort"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Effort == nil || *req.Effort < 1 || *req.Effort > 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "effort must be an integer from 1 to 10"})
		return
	}
	result := ctl.db.WithContext(c.Request.Context()).Model(&db.Session{}).Where("session_id = ? AND profile_id = ? AND workout_type = ? AND wod_evaluation_version = ?", sid, pid, "wod", wodeval.Version).Update("effort", *req.Effort)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save effort"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "effort is available only for new WOD sessions"})
		return
	}
	ctl.GetWODTraining(c)
}

func (ctl *Controller) GetWODTrainingLoad(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Query("profile_id"), 10, 64)
	if err != nil || pid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}
	if UserIDFromContext(c) == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var profile db.Profile
	if ctl.db.WithContext(c.Request.Context()).Where("id = ? AND user_id = ?", pid, UserIDFromContext(c)).First(&profile).Error != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized for this profile"})
		return
	}
	location, err := time.LoadLocation(c.DefaultQuery("timezone", "Asia/Seoul"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid timezone"})
		return
	}
	now := time.Now()
	d := now.In(location)
	from := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -27)
	type row struct {
		WorkoutAt    *time.Time
		SessionScore string
		Status       string
		Effort       *int
	}
	rows := []row{}
	if err := ctl.db.WithContext(c.Request.Context()).Table("analysis_results AS a").Select("a.workout_at, a.session_score, a.status, s.effort").Joins("JOIN sessions s ON s.session_id = a.session_id AND s.profile_id = a.profile_id").Where("a.profile_id = ? AND a.archived_at IS NULL AND s.workout_type = ? AND s.wod_evaluation_version = ? AND (a.workout_at >= ? OR a.workout_at IS NULL)", pid, "wod", wodeval.Version, from).Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load training history"})
		return
	}
	entries := []wodeval.Workout{}
	missingTime := 0
	for _, r := range rows {
		if r.WorkoutAt == nil {
			missingTime++
			continue
		}
		var score wodeval.Score
		_ = json.Unmarshal([]byte(r.SessionScore), &score)
		if r.Status != "COMPLETED" || score.EvaluationVersion != 1 {
			score.WODStart = wodeval.Start{Status: "unresolved"}
		}
		entries = append(entries, wodeval.Workout{At: *r.WorkoutAt, Effort: r.Effort, Start: score.WODStart})
	}
	comparison := wodeval.Compare(entries, now, profile.WODTrackingStartedAt, location)
	if missingTime > 0 {
		comparison.Status = "incomplete_records"
		comparison.ChangePercent = nil
	}
	c.JSON(http.StatusOK, gin.H{"comparison": comparison, "missing_workout_time": missingTime, "timezone": location.String()})
}
