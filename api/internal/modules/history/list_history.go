package history

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/wodeval"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"gorm.io/gorm"
)

// OmittedAnalysisColumns excludes unused JSON/TOAST columns from GET /history
// while retaining sensor_processing and sensor_summary for freshness verification.
var OmittedAnalysisColumns = []string{
	"sensor_timeline",
	"agentic_highlight_analysis",
	"injury_output_alt",
}

type HistoryFilter string

const (
	FilterActive            HistoryFilter = "active"
	FilterArchived          HistoryFilter = "archived"
	FilterInputNeeded       HistoryFilter = "input_needed"
	FilterEffortNeeded      HistoryFilter = "effort_needed"
	FilterPerformanceNeeded HistoryFilter = "performance_needed"
)

var ErrInvalidFilter = errors.New("invalid history filter")

type HistoryListQuery struct {
	ProfileID uint
	Filter    HistoryFilter
	Start     *time.Time
	End       *time.Time
	Limit     uint
	BeforeID  uint
}

// check if the filter requires sessions that need input
func (f HistoryFilter) requiresInputCheck() bool {
	switch f {
	case FilterInputNeeded, FilterEffortNeeded, FilterPerformanceNeeded:
		return true
	default:
		return false
	}
}

// check if the calculated InputStatus matches the current filter condition
func (f HistoryFilter) matchesInputStatus(status *db.HistoryInputStatus) bool {
	if status == nil {
		return false
	}

	switch f {
	case FilterInputNeeded:
		return status.EffortMissing || status.PerformanceMissing
	case FilterEffortNeeded:
		return status.EffortMissing
	case FilterPerformanceNeeded:
		return status.PerformanceMissing
	default:
		return true
	}
}

func ListByDateRange(ctx context.Context, database *gorm.DB, q HistoryListQuery) ([]db.AnalysisResult, error) {
	scope := database.WithContext(ctx).
		Omit(OmittedAnalysisColumns...).
		Where("profile_id = ?", q.ProfileID)

	switch q.Filter {
	case "", FilterActive:
		scope = scope.Where("archived_at IS NULL")
	case FilterArchived:
		scope = scope.Where("archived_at IS NOT NULL")
	case FilterInputNeeded, FilterEffortNeeded, FilterPerformanceNeeded:
		scope = scope.Where("archived_at IS NULL").Where("UPPER(status) = ?", "COMPLETED")
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidFilter, q.Filter)
	}

	results := []db.AnalysisResult{}
	scope = scope.Order("created_at desc")
	if q.Start != nil && q.End != nil {
		scope = scope.Where("created_at >= ? AND created_at < ?", q.Start, q.End.AddDate(0, 0, 1))
	}

	if q.BeforeID > 0 {
		scope = scope.Where("id < ?", q.BeforeID)
	}

	if q.Limit > 0 && !q.Filter.requiresInputCheck() {
		scope = scope.Limit(int(q.Limit))
	}

	if err := scope.Find(&results).Error; err != nil {
		return nil, err
	}

	// fill in the InputStatus for each result
	sessionByID, err := populateHistoryInputStatus(ctx, database, q.ProfileID, results)
	if err != nil {
		return nil, err
	}

	// filter the results based on the current filter condition
	if q.Filter.requiresInputCheck() {
		filtered := results[:0]
		for i := range results {
			if q.Filter.matchesInputStatus(results[i].InputStatus) {
				filtered = append(filtered, results[i])
				if q.Limit > 0 && uint(len(filtered)) == q.Limit {
					break
				}
			}
		}
		results = filtered
	}

	// TODO: use sessionByID to get the session details
	_ = sessionByID

	return results, nil
}

func populateHistoryInputStatus(ctx context.Context, database *gorm.DB, profileID uint, results []db.AnalysisResult) (map[string]db.Session, error) {
	if database == nil || len(results) == 0 {
		for i := range results {
			results[i].WorkoutProgress = nil
		}
		return nil, nil
	}
	sessionIDs := make([]string, 0, len(results))
	seenSessions := make(map[string]struct{}, len(results))
	decodedProgress := make(map[int]workoutprogress.Progress)
	progressSessionIDs := make([]string, 0, len(results))
	seenProgressSessions := make(map[string]struct{}, len(results))

	for i := range results {
		if results[i].ArchivedAt != nil {
			results[i].WorkoutProgress = nil
			continue
		}
		sid := strings.TrimSpace(results[i].SessionID)
		if sid == "" {
			results[i].WorkoutProgress = nil
			continue
		}
		if _, ok := seenSessions[sid]; !ok {
			seenSessions[sid] = struct{}{}
			sessionIDs = append(sessionIDs, sid)
		}
		if strings.EqualFold(strings.TrimSpace(results[i].Status), "COMPLETED") && len(results[i].WorkoutProgress) > 0 {
			if progress, err := workoutprogress.Decode(results[i].WorkoutProgress); err == nil {
				decodedProgress[i] = progress
				if _, ok := seenProgressSessions[sid]; !ok {
					seenProgressSessions[sid] = struct{}{}
					progressSessionIDs = append(progressSessionIDs, sid)
				}
			}
		}
	}
	if len(sessionIDs) == 0 {
		for i := range results {
			results[i].WorkoutProgress = nil
		}
		return nil, nil
	}

	var sessions []db.Session
	if err := database.WithContext(ctx).
		Select("session_id", "profile_id", "workout_type", "wod_evaluation_version", "effort", "wod_description", "activity_summary", "activity_summary_cache_version").
		Where("profile_id = ? AND session_id IN ?", profileID, sessionIDs).
		Find(&sessions).Error; err != nil {
		return nil, err
	}
	sessionByID := make(map[string]db.Session, len(sessions))
	for _, s := range sessions {
		sessionByID[strings.TrimSpace(s.SessionID)] = s
	}

	recordsBySession := make(map[string][]workoutprogress.Record, len(progressSessionIDs))
	if len(progressSessionIDs) > 0 {
		var recordRows []db.PerformanceRecord
		if err := database.WithContext(ctx).
			Where("profile_id = ? AND session_id IN ?", profileID, progressSessionIDs).
			Order("start_secs ASC, id ASC").
			Find(&recordRows).Error; err != nil {
			return nil, err
		}
		for _, row := range recordRows {
			sid := strings.TrimSpace(row.SessionID)
			recordsBySession[sid] = append(recordsBySession[sid], performanceRecordResponse(row))
		}
	}

	for i := range results {
		if results[i].ArchivedAt != nil {
			results[i].WorkoutProgress = nil
			continue
		}
		sid := strings.TrimSpace(results[i].SessionID)
		var sessPtr *db.Session
		if s, ok := sessionByID[sid]; ok {
			if strings.TrimSpace(results[i].WODDescription) == "" && strings.TrimSpace(s.WODDescription) != "" {
				results[i].WODDescription = s.WODDescription
			}
			sessCopy := s
			sessPtr = &sessCopy
		}
		var progPtr *workoutprogress.Progress
		if progress, ok := decodedProgress[i]; ok {
			progCopy := progress
			progPtr = &progCopy
		}
		results[i].InputStatus = computeHistoryInputStatus(results[i], sessPtr, progPtr, recordsBySession[sid])
		results[i].WorkoutProgress = nil
	}
	return sessionByID, nil
}

func performanceRecordResponse(row db.PerformanceRecord) workoutprogress.Record {
	record := workoutprogress.Record{ID: row.ID, SessionID: row.SessionID, IntervalID: row.IntervalID, SourceVersion: row.SourceVersion, Status: row.Status, Outcome: workoutprogress.NormalizedOutcome(row.Outcome), Movement: row.Movement, Start: row.StartSecs, End: row.EndSecs, Reps: row.Reps, LoadKG: row.LoadKG, Measurement: row.Measurement, Technique: row.Technique, WorkoutPrescription: row.WorkoutPrescription, RepsPerMinute: row.RepsPerMinute, RateBasis: row.RateBasis, MedianCycleSeconds: row.MedianCycleSeconds, PerformedAt: row.PerformedAt, ConfirmedAt: row.ConfirmedAt}
	record.ElapsedSeconds = row.ElapsedSeconds
	record.PerformedAtSource = row.PerformedAtSource
	record.RestComparisons = workoutprogress.RestComparisonsFromCandidate(row.OriginalCandidate, record)
	if row.LoadValue != nil && row.LoadUnit != nil && row.LoadBasis != nil {
		record.Load = &workoutprogress.Load{Value: *row.LoadValue, Unit: *row.LoadUnit, Basis: *row.LoadBasis, Status: "confirmed"}
	}
	return record
}

func computeHistoryInputStatus(
	row db.AnalysisResult,
	session *db.Session,
	progress *workoutprogress.Progress,
	records []workoutprogress.Record,
) *db.HistoryInputStatus {
	if row.ArchivedAt != nil {
		return nil
	}
	completed := strings.EqualFold(strings.TrimSpace(row.Status), "COMPLETED")
	effortEligible := session != nil && session.WorkoutType == "wod" && session.WODEvaluationVersion == wodeval.Version
	var effort *int
	if effortEligible && session.Effort != nil {
		val := *session.Effort
		effort = &val
	}
	status := &db.HistoryInputStatus{
		EffortEligible: effortEligible,
		Effort:         effort,
		EffortMissing:  completed && effortEligible && effort == nil,
	}
	if completed && progress != nil {
		summary := workoutprogress.SummarizeConfirmation(*progress, records)
		status.ExerciseIntervalCount = summary.ExerciseIntervalCount
		status.ConfirmedIntervalCount = summary.ConfirmedIntervalCount
		status.NeedsConfirmationCount = summary.NeedsConfirmationCount
		status.PerformanceMissing = summary.PerformanceMissing
	}
	return status
}
