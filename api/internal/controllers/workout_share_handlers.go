package controllers

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/timeline"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type WorkoutShareSession struct {
	SessionID           string     `json:"session_id"`
	WorkoutType         string     `json:"workout_type"`
	PerformedAt         *time.Time `json:"performed_at"`
	PerformedAtSource   *string    `json:"performed_at_source"`
	WorkoutPrescription string     `json:"workout_prescription"`
	SourceVersion       string     `json:"source_version"`
}

// WorkoutShareObservation is AI evidence, including intervals subsequently
// confirmed by the user. Confirmations are supplied separately and never change it.
type WorkoutShareObservation struct {
	ID               string                        `json:"id"`
	Start            float64                       `json:"start_secs"`
	End              float64                       `json:"end_secs"`
	Activity         string                        `json:"activity"`
	Movement         string                        `json:"movement"`
	Load             *workoutprogress.Load         `json:"load"`
	ObservedReps     *int                          `json:"observed_reps"`
	FormObservations []workoutprogress.Observation `json:"form_observations"`
	ROMObservations  []workoutprogress.Observation `json:"rom_observations"`
	PaceObservations []workoutprogress.Observation `json:"pace_observations"`
}

type WorkoutShareSensorSummary struct {
	Status             string   `json:"status"`
	QualityStatus      string   `json:"quality_status"`
	AvgBPM             *float64 `json:"avg_bpm"`
	PeakBPM            *float64 `json:"peak_bpm"`
	Coverage           *float64 `json:"coverage"`
	ValidSeconds       *float64 `json:"valid_seconds"`
	UnknownSeconds     *float64 `json:"unknown_seconds"`
	ExcludedSeconds    *float64 `json:"excluded_seconds"`
	CalculationVersion *int     `json:"calculation_version"`
}

type WorkoutShareContextResponse struct {
	SchemaVersion    int                          `json:"schema_version"`
	GeneratedAt      time.Time                    `json:"generated_at"`
	Timezone         string                       `json:"timezone"`
	Session          WorkoutShareSession          `json:"session"`
	ConfirmedRecords []workoutprogress.Record     `json:"confirmed_records"`
	Comparisons      []workoutprogress.Comparison `json:"comparisons"`
	BaselineRecords  []workoutprogress.Record     `json:"baseline_records"`
	Observations     []WorkoutShareObservation    `json:"observations"`
	Unassessed       []workoutprogress.Gap        `json:"unassessed"`
	SensorSummary    *WorkoutShareSensorSummary   `json:"sensor_summary"`
	Effort           *int                         `json:"effort"`
	Warnings         []string                     `json:"warnings"`
}

type workoutShareError struct {
	status  int
	message string
}

func (e workoutShareError) Error() string { return e.message }

// GetWorkoutShareContext takes a read-only database snapshot. It neither starts
// analysis nor grants external access: clients preview and explicitly export it.
// @Summary Get a read-only workout context for explicit external sharing
// @Tags analysis
// @Produce json
// @Param session_id path string true "Owned session ID"
// @Param profile_id query int true "Owned profile ID"
// @Param timezone query string false "IANA timezone (default Asia/Seoul)"
// @Success 200 {object} WorkoutShareContextResponse
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /sessions/{session_id}/share-context [get]
func (ctl *Controller) GetWorkoutShareContext(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uid := UserIDFromContext(c)
	if uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	sid, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	pid, err := strconv.ParseUint(c.Query("profile_id"), 10, 32)
	if err != nil || pid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}
	location, err := time.LoadLocation(c.DefaultQuery("timezone", "Asia/Seoul"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid timezone"})
		return
	}
	var out WorkoutShareContextResponse
	err = ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var profile db.Profile
		if err := tx.Where("id = ? AND user_id = ?", uint(pid), uid).First(&profile).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return workoutShareError{http.StatusForbidden, "not authorized for this profile"}
			}
			return err
		}
		var analysis db.AnalysisResult
		if err := tx.Where("session_id = ?", sid).First(&analysis).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return workoutShareError{http.StatusNotFound, "workout analysis not found"}
			}
			return err
		}
		if analysis.ProfileID != uint(pid) {
			return workoutShareError{http.StatusForbidden, "not authorized for this session"}
		}
		if analysis.ArchivedAt != nil {
			return workoutShareError{http.StatusConflict, "session is archived"}
		}
		if analysis.Status != "COMPLETED" || analysis.AnalysisType != db.AnalysisTypeWOD {
			return workoutShareError{http.StatusConflict, "completed workout analysis is required"}
		}
		var session db.Session
		if err := tx.Where("session_id = ?", sid).First(&session).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if session.ID != 0 && session.ProfileID != uint(pid) {
			return workoutShareError{http.StatusForbidden, "not authorized for this session"}
		}
		out = buildWorkoutShareContext(analysis, session, location)
		var rows []db.PerformanceRecord
		if err := tx.Where("session_id = ? AND profile_id = ? AND analysis_id = ?", sid, pid, analysis.ID).
			Order("start_secs ASC, id ASC").Find(&rows).Error; err != nil {
			return err
		}
		stale := false
		for _, row := range rows {
			if row.Status != "confirmed" || out.Session.SourceVersion == "" || row.SourceVersion != out.Session.SourceVersion {
				stale = true
				continue
			}
			record := performanceRecordResponse(row)
			record.PerformedAt = out.Session.PerformedAt
			record.PerformedAtSource = out.Session.PerformedAtSource
			out.ConfirmedRecords = append(out.ConfirmedRecords, record)
		}
		out.ConfirmedRecords = workoutprogress.ActiveRecords(out.ConfirmedRecords)
		if stale {
			out.Warnings = append(out.Warnings, "stale_confirmations_excluded")
		}
		if len(out.ConfirmedRecords) == 0 {
			out.Warnings = append(out.Warnings, "no_confirmed_records")
		}
		if out.Session.PerformedAt != nil && len(out.ConfirmedRecords) > 0 {
			movements := []string{}
			for _, record := range out.ConfirmedRecords {
				if record.Movement != "" {
					movements = append(movements, record.Movement)
				}
			}
			var historyRows []db.PerformanceRecord
			if err := tx.Table("performance_records AS r").Select("r.*").
				Joins("JOIN analysis_results a ON a.id = r.analysis_id AND a.session_id = r.session_id AND a.profile_id = r.profile_id").
				Where("r.profile_id = ? AND r.session_id <> ? AND r.status = 'confirmed' AND r.performed_at < ?", pid, sid, *out.Session.PerformedAt).
				Where("r.performed_at_source IN ?", []string{timeline.WorkoutAtSourceSessionULID, timeline.WorkoutAtSourceLegacyLocalTime}).
				Where("r.performed_at = a.workout_at AND r.performed_at_source = a.workout_at_source").
				Where("r.session_id IN (?)", tx.Table("performance_records").Select("session_id").Where("profile_id = ? AND movement IN ?", pid, movements)).
				Where("a.status = 'COMPLETED' AND a.analysis_type = ? AND a.archived_at IS NULL AND a.workout_progress->>'source_version' = r.source_version", db.AnalysisTypeWOD).
				Order("r.performed_at DESC, r.session_id ASC, r.start_secs DESC, r.id DESC").Find(&historyRows).Error; err != nil {
				return err
			}
			history := make([]workoutprogress.Record, 0, len(historyRows))
			for _, row := range historyRows {
				history = append(history, performanceRecordResponse(row))
			}
			out.Comparisons, out.BaselineRecords = workoutShareComparisons(out.ConfirmedRecords, activeWorkoutShareHistory(history))
		}
		if len(out.Comparisons) == 0 {
			out.Warnings = append(out.Warnings, "no_prior_comparison")
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		var shareErr workoutShareError
		if errors.As(err, &shareErr) {
			c.JSON(shareErr.status, gin.H{"error": shareErr.message})
			return
		}
		logger.Log.Error("failed to load workout share snapshot", zap.String("session_id", sid), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load workout share context"})
		return
	}
	c.JSON(http.StatusOK, out)
}

func buildWorkoutShareContext(analysis db.AnalysisResult, session db.Session, location *time.Location) WorkoutShareContextResponse {
	performedAt, source := workoutShareTime(analysis, session)
	typeName := session.WorkoutType
	if typeName == "" {
		typeName = "wod"
		for _, part := range strings.Split(analysis.SessionID, "-") {
			switch strings.ToLower(part) {
			case "wod", "warmup", "accessory", "cooldown":
				typeName = strings.ToLower(part)
			}
		}
	}
	prescription := strings.TrimSpace(analysis.WODDescription)
	if prescription == "" {
		prescription = strings.TrimSpace(session.WODDescription)
	}
	out := WorkoutShareContextResponse{
		SchemaVersion: 1, GeneratedAt: time.Now().UTC(), Timezone: location.String(),
		Session:          WorkoutShareSession{SessionID: analysis.SessionID, WorkoutType: typeName, PerformedAt: performedAt, PerformedAtSource: source, WorkoutPrescription: prescription},
		ConfirmedRecords: []workoutprogress.Record{}, Comparisons: []workoutprogress.Comparison{}, BaselineRecords: []workoutprogress.Record{},
		Observations: []WorkoutShareObservation{}, Unassessed: []workoutprogress.Gap{}, Warnings: []string{},
	}
	if performedAt == nil {
		out.Warnings = append(out.Warnings, "workout_time_unknown")
	}
	if session.Effort != nil && *session.Effort >= 1 && *session.Effort <= 10 {
		out.Effort = session.Effort
	}
	if progress, err := workoutprogress.Decode(analysis.WorkoutProgress); err == nil {
		out.Session.SourceVersion = progress.SourceVersion
		out.Unassessed = progress.Unassessed
		for _, interval := range progress.Intervals {
			var observedReps *int
			count := 0
			for _, event := range interval.RepEvents {
				if event.Complete && event.Unit == "reps" {
					count++
				}
			}
			if count > 0 {
				observedReps = &count
			}
			out.Observations = append(out.Observations, WorkoutShareObservation{
				ID: interval.ID, Start: interval.Start, End: interval.End, Activity: interval.Activity, Movement: interval.Movement,
				Load: interval.Load, ObservedReps: observedReps,
				FormObservations: append([]workoutprogress.Observation{}, interval.FormObservations...),
				ROMObservations:  append([]workoutprogress.Observation{}, interval.ROMObservations...),
				PaceObservations: append([]workoutprogress.Observation{}, interval.PaceObservations...),
			})
		}
	}
	if len(out.Observations) == 0 {
		out.Warnings = append(out.Warnings, "observations_unavailable")
	}
	populateHeartRateSummary(&analysis)
	if hr := analysis.HeartRate; hr != nil && hr.Status != "none" {
		out.SensorSummary = &WorkoutShareSensorSummary{Status: hr.Status, QualityStatus: hr.QualityStatus, AvgBPM: hr.AvgBPM, Coverage: hr.Coverage, ValidSeconds: hr.ValidSeconds, UnknownSeconds: hr.UnknownSeconds, ExcludedSeconds: hr.ExcludedSeconds}
		if hr.PeakBPM != nil {
			out.SensorSummary.PeakBPM = workoutprogress.Float64Ptr(float64(*hr.PeakBPM))
		}
		if hr.CalculationVersion > 0 {
			out.SensorSummary.CalculationVersion = workoutprogress.IntPtr(hr.CalculationVersion)
		}
	}
	return out
}

func workoutShareTime(analysis db.AnalysisResult, session db.Session) (*time.Time, *string) {
	if analysis.WorkoutAt != nil {
		if analysis.WorkoutAtSource != nil && (*analysis.WorkoutAtSource == timeline.WorkoutAtSourceSessionULID || *analysis.WorkoutAtSource == timeline.WorkoutAtSourceLegacyLocalTime) {
			return analysis.WorkoutAt, analysis.WorkoutAtSource
		}
		return nil, analysis.WorkoutAtSource
	}
	var sessionCreatedAt *time.Time
	if !session.CreatedAt.IsZero() {
		sessionCreatedAt = &session.CreatedAt
	}
	at, source, reliable := timeline.ResolveWorkoutAt(analysis.SessionID, sessionCreatedAt, &analysis.CreatedAt)
	if reliable {
		return &at, &source
	}
	if source == "" {
		return nil, nil
	}
	return nil, &source
}

func workoutShareComparisons(current, history []workoutprogress.Record) ([]workoutprogress.Comparison, []workoutprogress.Record) {
	comparisons := []workoutprogress.Comparison{}
	baselines := []workoutprogress.Record{}
	byID := make(map[uint]workoutprogress.Record, len(history))
	for _, record := range history {
		byID[record.ID] = record
	}
	selected := map[uint]bool{}
	for _, record := range current {
		// A separate call per selected record excludes earlier sets in the same
		// workout from competing with the prior-session baseline.
		candidates := []workoutprogress.Record{}
		for _, prior := range history {
			if prior.Movement == record.Movement {
				candidates = append(candidates, prior)
			}
		}
		if comparison, ok := workoutprogress.ComparisonFor(record, candidates); ok {
			comparisons = append(comparisons, comparison)
			selected[comparison.BaselineRecordID] = true
			if comparison.HeavierLoadBaselineRecordID != nil {
				selected[*comparison.HeavierLoadBaselineRecordID] = true
			}
		}
	}
	for id := range selected {
		if record, ok := byID[id]; ok {
			baselines = append(baselines, record)
		}
	}
	sort.Slice(baselines, func(i, j int) bool { return baselines[i].ID < baselines[j].ID })
	return comparisons, baselines
}

// Supersession is session-local. Grouping prevents a quadratic scan across the
// whole profile while retaining the query's deterministic baseline tie order.
func activeWorkoutShareHistory(records []workoutprogress.Record) []workoutprogress.Record {
	groups := map[string][]workoutprogress.Record{}
	for _, record := range records {
		groups[record.SessionID] = append(groups[record.SessionID], record)
	}
	active := map[uint]bool{}
	for _, group := range groups {
		for _, record := range workoutprogress.ActiveRecords(group) {
			active[record.ID] = true
		}
	}
	out := []workoutprogress.Record{}
	for _, record := range records {
		if active[record.ID] {
			out = append(out, record)
		}
	}
	return out
}
