package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/timeline"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WorkoutProgressResponse struct {
	Progress *workoutprogress.Progress `json:"progress"`
	Records  []workoutprogress.Record  `json:"records"`
}

type PerformanceBenchmarksResponse struct {
	Records     []workoutprogress.Record     `json:"records"`
	Comparisons []workoutprogress.Comparison `json:"comparisons"`
	Best        []workoutprogress.Record     `json:"best"`
}

func performanceRecordResponse(row db.PerformanceRecord) workoutprogress.Record {
	record := workoutprogress.Record{ID: row.ID, SessionID: row.SessionID, IntervalID: row.IntervalID, SourceVersion: row.SourceVersion, Status: row.Status, Movement: row.Movement, Start: row.StartSecs, End: row.EndSecs, Reps: row.Reps, LoadKG: row.LoadKG, Measurement: row.Measurement, Technique: row.Technique, WorkoutPrescription: row.WorkoutPrescription, RepsPerMinute: row.RepsPerMinute, RateBasis: row.RateBasis, MedianCycleSeconds: row.MedianCycleSeconds, PerformedAt: row.PerformedAt, ConfirmedAt: row.ConfirmedAt}
	record.ElapsedSeconds = row.ElapsedSeconds
	record.PerformedAtSource = row.PerformedAtSource
	record.RestComparisons = workoutprogress.RestComparisonsFromCandidate(row.OriginalCandidate, record)
	if row.LoadValue != nil && row.LoadUnit != nil && row.LoadBasis != nil {
		record.Load = &workoutprogress.Load{Value: *row.LoadValue, Unit: *row.LoadUnit, Basis: *row.LoadBasis, Status: "confirmed"}
	}
	return record
}

func (ctl *Controller) progressSession(c *gin.Context) (string, uint, bool) {
	if UserIDFromContext(c) == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return "", 0, false
	}
	sid, ok := feedbackSessionID(c)
	if !ok {
		return "", 0, false
	}
	pid, ok := ctl.resolveFeedbackSession(c, sid)
	return sid, pid, ok
}

// GetWorkoutProgress returns independent observations and separate confirmations.
// @Summary Get chronological workout observations
// @Tags analysis
// @Produce json
// @Param session_id path string true "Owned session ID"
// @Success 200 {object} WorkoutProgressResponse
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /sessions/{session_id}/workout-progress [get]
func (ctl *Controller) GetWorkoutProgress(c *gin.Context) {
	sid, pid, ok := ctl.progressSession(c)
	if !ok {
		return
	}
	out := WorkoutProgressResponse{Records: []workoutprogress.Record{}}
	var analysis db.AnalysisResult
	err := ctl.db.WithContext(c.Request.Context()).Where("session_id = ? AND profile_id = ? AND archived_at IS NULL", sid, pid).First(&analysis).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load workout progress"})
		return
	}
	if err == nil && analysis.Status == "COMPLETED" {
		if progress, e := workoutprogress.Decode(analysis.WorkoutProgress); e == nil {
			out.Progress = &progress
		}
	}
	rows := []db.PerformanceRecord{}
	if err := ctl.db.WithContext(c.Request.Context()).Where("session_id = ? AND profile_id = ?", sid, pid).Order("start_secs ASC, id ASC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load performance records"})
		return
	}
	for _, row := range rows {
		record := performanceRecordResponse(row)
		if out.Progress == nil || row.SourceVersion != out.Progress.SourceVersion {
			record.Status = "needs_confirmation"
		}
		out.Records = append(out.Records, record)
	}
	c.JSON(http.StatusOK, out)
}

type PerformanceConfirmationInput struct {
	IntervalID  string          `json:"interval_id"`
	Start       float64         `json:"start_secs"`
	End         float64         `json:"end_secs"`
	Reps        int             `json:"reps"`
	Load        json.RawMessage `json:"load" swaggertype:"object"`
	Measurement string          `json:"measurement"`
	Technique   string          `json:"technique"`
}

type PerformanceRecordsRequest struct {
	SourceVersion string                         `json:"source_version"`
	Records       []PerformanceConfirmationInput `json:"records"`
}

var errProgressSourceChanged = errors.New("workout observation source changed")

// UpdatePerformanceRecords confirms observed windows without modifying progress.
// @Summary Confirm workout performance records
// @Tags analysis
// @Accept json
// @Produce json
// @Param session_id path string true "Owned session ID"
// @Param request body PerformanceRecordsRequest true "Confirm interval, window, integer reps and actual load (or explicit null for no external load)"
// @Success 200 {object} WorkoutProgressResponse
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /sessions/{session_id}/performance-records [put]
func (ctl *Controller) UpdatePerformanceRecords(c *gin.Context) {
	sid, pid, ok := ctl.progressSession(c)
	if !ok {
		return
	}
	var req PerformanceRecordsRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.SourceVersion) == "" || len(req.Records) == 0 || len(req.Records) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_version and 1–100 records are required"})
		return
	}
	confirmed := []workoutprogress.Confirmation{}
	seen := map[string]bool{}
	for _, input := range req.Records {
		if input.IntervalID == "" || seen[input.IntervalID] || len(input.Load) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "confirm each interval once and specify actual load or null"})
			return
		}
		seen[input.IntervalID] = true
		var load *workoutprogress.Load
		if json.Unmarshal(input.Load, &load) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid actual load"})
			return
		}
		confirmed = append(confirmed, workoutprogress.Confirmation{IntervalID: input.IntervalID, Start: input.Start, End: input.End, Reps: input.Reps, Load: load, Measurement: input.Measurement, Technique: input.Technique})
	}
	badRequest := false
	err := ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var analysis db.AnalysisResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id = ? AND profile_id = ? AND archived_at IS NULL", sid, pid).First(&analysis).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errProgressSourceChanged
			}
			return err
		}
		progress, err := workoutprogress.Decode(analysis.WorkoutProgress)
		if err != nil || analysis.Status != "COMPLETED" || progress.SourceVersion != req.SourceVersion {
			return errProgressSourceChanged
		}
		performedAt, performedAtSource := analysis.WorkoutAt, analysis.WorkoutAtSource
		if performedAt == nil {
			var session db.Session
			var createdAt *time.Time
			err := tx.Select("created_at").Where("session_id = ? AND profile_id = ?", sid, pid).First(&session).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil && !session.CreatedAt.IsZero() {
				createdAt = &session.CreatedAt
			}
			resolved, source, _ := timeline.ResolveWorkoutAt(sid, createdAt, &analysis.CreatedAt)
			if !resolved.IsZero() {
				performedAt = &resolved
				performedAtSource = &source
			}
		}
		now := time.Now().UTC()
		for _, input := range confirmed {
			record, err := workoutprogress.Confirm(progress, input)
			if err != nil {
				badRequest = true
				return err
			}
			var candidate workoutprogress.Interval
			for _, in := range progress.Intervals {
				if in.ID == input.IntervalID {
					candidate = in
					break
				}
			}
			raw, _ := json.Marshal(candidate)
			if input.Measurement == "window" || input.Start < candidate.Start || input.End > candidate.End {
				observed := []workoutprogress.Interval{}
				gaps := []workoutprogress.Gap{}
				for _, in := range progress.Intervals {
					if in.Start < input.End && in.End > input.Start {
						observed = append(observed, in)
					}
				}
				for _, gap := range progress.Unassessed {
					if gap.Clock == "media" && gap.Start < input.End && gap.End > input.Start {
						gaps = append(gaps, gap)
					}
				}
				raw, _ = json.Marshal(map[string]any{"version": progress.Version, "clock": progress.Clock, "interval_id": input.IntervalID, "start_secs": input.Start, "end_secs": input.End, "source_version": progress.SourceVersion, "intervals": observed, "unassessed": gaps})
			}
			row := db.PerformanceRecord{ProfileID: pid, AnalysisID: analysis.ID, SessionID: sid, IntervalID: record.IntervalID, SourceVersion: record.SourceVersion, Status: "confirmed", Movement: record.Movement, StartSecs: record.Start, EndSecs: record.End, Reps: record.Reps, LoadKG: record.LoadKG, Measurement: record.Measurement, Technique: record.Technique, WorkoutPrescription: strings.TrimSpace(analysis.WODDescription), RepsPerMinute: record.RepsPerMinute, RateBasis: record.RateBasis, MedianCycleSeconds: record.MedianCycleSeconds, OriginalCandidate: db.JSONDocument(raw), PerformedAt: analysis.WorkoutAt, ConfirmedAt: now, CreatedAt: now, UpdatedAt: now}
			row.ElapsedSeconds = record.ElapsedSeconds
			row.PerformedAt, row.PerformedAtSource = performedAt, performedAtSource
			if record.Load != nil {
				row.LoadValue = &record.Load.Value
				row.LoadUnit = &record.Load.Unit
				row.LoadBasis = &record.Load.Basis
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "profile_id"}, {Name: "session_id"}, {Name: "interval_id"}}, DoUpdates: clause.AssignmentColumns([]string{"analysis_id", "source_version", "status", "movement", "start_secs", "end_secs", "elapsed_seconds", "reps", "load_value", "load_unit", "load_basis", "load_kg", "measurement", "technique", "workout_prescription", "reps_per_minute", "rate_basis", "median_cycle_seconds", "original_candidate", "performed_at", "performed_at_source", "confirmed_at", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errProgressSourceChanged) {
		c.JSON(http.StatusConflict, gin.H{"error": "analysis changed; review the current observations before confirming"})
		return
	}
	if badRequest {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save performance records"})
		return
	}
	ctl.GetWorkoutProgress(c)
}

// GetPerformanceBenchmarks compares only current-source owned confirmations.
// @Summary Get confirmed workout performance benchmarks
// @Tags analysis
// @Produce json
// @Param profile_id query int true "Owned profile ID"
// @Param movement query string false "Canonical movement or recognized alias"
// @Success 200 {object} PerformanceBenchmarksResponse
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /performance-benchmarks [get]
func (ctl *Controller) GetPerformanceBenchmarks(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Query("profile_id"), 10, 64)
	if err != nil || pid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}
	uid := UserIDFromContext(c)
	if uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var profile db.Profile
	if err := ctl.db.WithContext(c.Request.Context()).Where("id = ? AND user_id = ?", pid, uid).First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusForbidden, gin.H{"error": "not authorized for this profile"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load profile"})
		}
		return
	}
	rows := []db.PerformanceRecord{}
	query := ctl.db.WithContext(c.Request.Context()).Table("performance_records AS r").Select("r.*").Joins("JOIN analysis_results a ON a.id = r.analysis_id AND a.session_id = r.session_id AND a.profile_id = r.profile_id").Joins("JOIN profiles p ON p.id = r.profile_id").Where("r.profile_id = ? AND p.user_id = ? AND r.status = ? AND r.reps > 0 AND a.status = ? AND a.archived_at IS NULL AND a.workout_progress->>'source_version' = r.source_version", pid, uid, "confirmed", "COMPLETED")
	if name := strings.TrimSpace(c.Query("movement")); name != "" {
		if canonical := activity.CanonicalMovement(name); canonical != "" {
			name = canonical
		}
		query = query.Where("r.movement = ?", name)
	}
	if err := query.Order("r.performed_at DESC NULLS LAST, r.id DESC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load performance benchmarks"})
		return
	}
	records := []workoutprogress.Record{}
	for _, row := range rows {
		records = append(records, performanceRecordResponse(row))
	}
	c.JSON(http.StatusOK, PerformanceBenchmarksResponse{Records: records, Comparisons: workoutprogress.Comparisons(records), Best: workoutprogress.Best(records)})
}
