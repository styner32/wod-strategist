package history

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"go.uber.org/zap"
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

func List(ctx context.Context, dbConn *gorm.DB, q HistoryListQuery) ([]db.AnalysisResult, error) {
	scope := dbConn.WithContext(ctx).
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

	if q.Start != nil && q.End != nil {
		scope = scope.Where("created_at >= ? AND created_at < ?", q.Start, q.End.AddDate(0, 0, 1)).Order("created_at desc, id desc")
	} else {
		scope = scope.Order("id desc")
		if q.Limit > 0 && q.Filter.requiresInputCheck() {
			return listMissingInputPage(ctx, dbConn, scope, q)
		}
	}

	if q.BeforeID > 0 {
		scope = scope.Where("id < ?", q.BeforeID)
	}

	if q.Limit > 0 && !q.Filter.requiresInputCheck() {
		scope = scope.Limit(int(q.Limit))
	}

	results := []db.AnalysisResult{}
	if err := scope.Find(&results).Error; err != nil {
		return nil, err
	}

	if len(results) == 0 {
		// no results to process
		return results, nil
	}

	// fill in the InputStatus for each result
	err := populateHistoryInputStatus(ctx, dbConn, q.ProfileID, results)
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

	return results, nil
}

func listMissingInputPage(ctx context.Context, dbConn, scope *gorm.DB, q HistoryListQuery) ([]db.AnalysisResult, error) {
	const maxBatchSize = 200
	pageSize := int(min(q.Limit, maxBatchSize))
	batchSize := min(maxBatchSize, max(60, 3*pageSize))
	page := make([]db.AnalysisResult, 0, pageSize)
	cursor := q.BeforeID

	for {
		query := scope.Session(&gorm.Session{}).Limit(batchSize)
		if cursor > 0 {
			query = query.Where("id < ?", cursor)
		}
		var batch []db.AnalysisResult
		if err := query.Find(&batch).Error; err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return page, nil
		}
		if err := populateHistoryInputStatus(ctx, dbConn, q.ProfileID, batch); err != nil {
			return nil, err
		}
		for i := range batch {
			if !q.Filter.matchesInputStatus(batch[i].InputStatus) {
				continue
			}
			page = append(page, batch[i])
			if uint(len(page)) == q.Limit {
				return page, nil
			}
		}
		if len(batch) < batchSize {
			return page, nil
		}
		cursor = batch[len(batch)-1].ID
	}
}

func populateHistoryInputStatus(ctx context.Context, dbConn *gorm.DB, profileID uint, results []db.AnalysisResult) error {
	sessionIDs := make([]string, 0, len(results))
	seenSessions := make(map[string]struct{}, len(results))
	decodedProgress := make(map[int]workoutprogress.Progress)
	progressSessionIDs := make([]string, 0, len(results))
	seenProgressSessions := make(map[string]struct{}, len(results))

	for i := range results {
		// filter archived sessions
		if results[i].ArchivedAt != nil {
			results[i].WorkoutProgress = nil
			continue
		}

		// filter sessions with no session ID
		sessionID := results[i].TrimmedSessionID()
		if sessionID == "" {
			results[i].WorkoutProgress = nil
			continue
		}

		// dedup session IDs
		if _, ok := seenSessions[sessionID]; !ok {
			seenSessions[sessionID] = struct{}{}
			sessionIDs = append(sessionIDs, sessionID)
		} else {
			// TODO: duplicate session IDs can't happen, needs to debug if happens
			logger.Log.Warn("duplicate session ID found", zap.String("session_id", sessionID), zap.Time("created_at", results[i].CreatedAt))
		}

		// filter completed sessions with workout progress
		if results[i].IsCompleted() && results[i].WorkoutProgress != nil {
			if progress, err := workoutprogress.Decode(results[i].WorkoutProgress); err == nil {
				decodedProgress[i] = progress
				if _, ok := seenProgressSessions[sessionID]; !ok {
					seenProgressSessions[sessionID] = struct{}{}
					progressSessionIDs = append(progressSessionIDs, sessionID)
				}
			}
		}
	}

	// no active session found for the profile
	if len(sessionIDs) == 0 {
		for i := range results {
			results[i].WorkoutProgress = nil
		}
		return nil
	}

	var sessions []db.Session
	if err := dbConn.WithContext(ctx).
		Select("session_id", "profile_id", "workout_type", "wod_evaluation_version", "effort", "wod_description", "activity_summary", "activity_summary_cache_version").
		Where("profile_id = ? AND session_id IN ?", profileID, sessionIDs).
		Find(&sessions).Error; err != nil {
		return err
	}
	sessionByID := make(map[string]db.Session, len(sessions))
	for _, s := range sessions {
		sessionByID[strings.TrimSpace(s.SessionID)] = s
	}

	recordsBySession := make(map[string][]workoutprogress.Record, len(progressSessionIDs))
	if len(progressSessionIDs) > 0 {
		var recordRows []db.PerformanceRecord
		if err := dbConn.WithContext(ctx).
			Where("profile_id = ? AND session_id IN ?", profileID, progressSessionIDs).
			Order("start_secs ASC, id ASC").
			Find(&recordRows).Error; err != nil {
			return err
		}

		for _, row := range recordRows {
			sessionID := strings.TrimSpace(row.SessionID)
			recordsBySession[sessionID] = append(recordsBySession[sessionID], performanceRecordResponse(row))
		}
	}

	// merge wod description from session and analysis result
	for i := range results {
		sessionID := results[i].TrimmedSessionID()
		var sessPtr *db.Session

		s, ok := sessionByID[sessionID]
		if ok {
			sessPtr = &s
			results[i].SetSession(&s)

			if results[i].IsWODDescriptionEmpty() && !s.IsWODDescriptionEmpty() {
				results[i].WODDescription = s.WODDescription
			}
		}

		if results[i].ArchivedAt != nil {
			results[i].WorkoutProgress = nil
			continue
		}

		var progPtr *workoutprogress.Progress
		if progress, ok := decodedProgress[i]; ok {
			progCopy := progress
			progPtr = &progCopy
		}
		results[i].InputStatus = computeHistoryInputStatus(results[i], sessPtr, progPtr, recordsBySession[sessionID])
		results[i].WorkoutProgress = nil
	}
	return nil
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
	completed := row.IsCompleted()
	effortEligible := session.IsEffortEligible()
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
