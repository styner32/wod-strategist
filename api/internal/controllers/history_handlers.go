package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/wodeval"
	"github.com/wod-strategist/api/internal/worker"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func matchesHistoryMissingFilter(status *db.HistoryInputStatus, missing string) bool {
	if status == nil {
		return false
	}
	switch missing {
	case "effort":
		return status.EffortMissing
	case "performance":
		return status.PerformanceMissing
	case "any":
		return status.EffortMissing || status.PerformanceMissing
	default:
		return true
	}
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

// historyOmittedAnalysisColumns excludes unused JSON/TOAST columns from GET /history
// while retaining sensor_processing and sensor_summary for freshness verification.
var historyOmittedAnalysisColumns = []string{
	"sensor_timeline",
	"agentic_highlight_analysis",
	"injury_output_alt",
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

type historySummaryMetrics struct {
	SessionsTotal    int
	SummaryCacheHits int
	SummaryRebuilt   int
	ChunkRowsLoaded  int
	SummaryMs        int64
}

func populateHistorySummariesAndHighlights(
	ctx context.Context,
	database *gorm.DB,
	profileID uint,
	results []db.AnalysisResult,
	sessionByID map[string]db.Session,
	useSummaryCache bool,
) (historySummaryMetrics, error) {
	var metrics historySummaryMetrics
	if database == nil || len(results) == 0 {
		return metrics, nil
	}
	activeSessionIDs := make([]string, 0, len(results))
	seenActive := make(map[string]struct{}, len(results))
	highlightSessionIDs := make([]string, 0, len(results))
	seenHighlight := make(map[string]struct{}, len(results))

	for i := range results {
		if results[i].ArchivedAt != nil {
			continue
		}
		sid := strings.TrimSpace(results[i].SessionID)
		if sid == "" {
			continue
		}
		if _, ok := seenActive[sid]; !ok {
			seenActive[sid] = struct{}{}
			activeSessionIDs = append(activeSessionIDs, sid)
		}
		if strings.EqualFold(strings.TrimSpace(results[i].Status), "COMPLETED") && results[i].AnalysisType == db.AnalysisTypeWOD {
			if _, ok := seenHighlight[sid]; !ok {
				seenHighlight[sid] = struct{}{}
				highlightSessionIDs = append(highlightSessionIDs, sid)
			}
		}
	}
	metrics.SessionsTotal = len(activeSessionIDs)
	if len(activeSessionIDs) == 0 {
		return metrics, nil
	}

	var summaryElapsed time.Duration
	summaryStart := time.Now()
	summaryBySession := make(map[string]activity.Summary, len(activeSessionIDs))
	uncachedSessionIDs := make([]string, 0, len(activeSessionIDs))
	for _, sid := range activeSessionIDs {
		if useSummaryCache {
			if sess, ok := sessionByID[sid]; ok && sess.ActivitySummaryCacheVersion == activity.SummaryCacheVersion {
				if cached, valid := activity.DecodeValidCachedSummary(sess.ActivitySummary); valid {
					summaryBySession[sid] = cached
					metrics.SummaryCacheHits++
					continue
				}
			}
		}
		uncachedSessionIDs = append(uncachedSessionIDs, sid)
	}
	summaryElapsed += time.Since(summaryStart)

	if len(uncachedSessionIDs) > 0 {
		var chunkRows []db.ChunkAnalysisResult
		if err := database.WithContext(ctx).
			Select("id", "profile_id", "session_id", "file_path", "exercise_type", "status", "start_secs", "end_secs", "media_start_secs", "media_end_secs", "movement_observations").
			Where("profile_id = ? AND session_id IN ?", profileID, uncachedSessionIDs).
			Find(&chunkRows).Error; err != nil {
			return metrics, err
		}
		metrics.ChunkRowsLoaded = len(chunkRows)
		rebuildStart := time.Now()
		chunksBySession := make(map[string][]db.ChunkAnalysisResult, len(uncachedSessionIDs))
		for _, row := range chunkRows {
			sid := strings.TrimSpace(row.SessionID)
			chunksBySession[sid] = append(chunksBySession[sid], row)
		}
		for _, sid := range uncachedSessionIDs {
			var stored activity.Summary
			if sess, ok := sessionByID[sid]; ok && len(sess.ActivitySummary) > 0 {
				_ = json.Unmarshal(sess.ActivitySummary, &stored)
			}
			summary := activity.Build(chunksBySession[sid], &stored)
			summary.Reviews = []activity.ReviewedChunk{}
			summaryBySession[sid] = summary
			metrics.SummaryRebuilt++
		}
		summaryElapsed += time.Since(rebuildStart)
	}
	metrics.SummaryMs = summaryElapsed.Milliseconds()

	highlightsBySession := make(map[string][]db.HighlightResult, len(highlightSessionIDs))
	if len(highlightSessionIDs) > 0 {
		var highlightRows []db.HighlightResult
		if err := database.WithContext(ctx).
			Where("profile_id = ? AND session_id IN ? AND status = ?", profileID, highlightSessionIDs, "COMPLETED").
			Order("created_at desc, id desc").
			Find(&highlightRows).Error; err != nil {
			return metrics, err
		}
		for _, row := range highlightRows {
			sid := strings.TrimSpace(row.SessionID)
			highlightsBySession[sid] = append(highlightsBySession[sid], row)
		}
	}

	for i := range results {
		if results[i].ArchivedAt != nil {
			continue
		}
		sid := strings.TrimSpace(results[i].SessionID)
		if sid == "" {
			continue
		}
		if summary, ok := summaryBySession[sid]; ok {
			summaryCopy := summary
			results[i].ActivitySummary = &summaryCopy
		}
		if _, ok := seenHighlight[sid]; ok && strings.EqualFold(strings.TrimSpace(results[i].Status), "COMPLETED") && results[i].AnalysisType == db.AnalysisTypeWOD {
			list := highlightsBySession[sid]
			if list == nil {
				list = []db.HighlightResult{}
			} else {
				list = append([]db.HighlightResult(nil), list...)
			}
			results[i].Highlights = &list
		}
	}
	return metrics, nil
}

// Main Handler for GetHistory
// /api/v1/history
func (ctl *Controller) GetHistory(c *gin.Context) {
	if c.Query("profile_id") == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}
	pid, err := strconv.ParseUint(c.Query("profile_id"), 10, 32)
	if err != nil || pid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile_id"})
		return
	}
	if !ctl.assertOwnsProfile(c, uint(pid)) {
		return
	}
	archived := c.Query("archived")
	if archived != "" && archived != "true" && archived != "false" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "archived must be true or false"})
		return
	}
	missing := strings.TrimSpace(c.Query("missing"))
	switch missing {
	case "", "any", "effort", "performance":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing must be any, effort, or performance"})
		return
	}
	if missing != "" && archived == "true" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing filter cannot be combined with archived=true"})
		return
	}

	ctx, qStats := db.WithQueryStats(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	qBefore := qStats.Snapshot().Duration

	results := []db.AnalysisResult{}
	var sessionByID map[string]db.Session
	from, to := c.Query("from"), c.Query("to")
	if from != "" && to != "" {
		start, e1 := time.Parse("2006-01-02", from)
		end, e2 := time.Parse("2006-01-02", to)
		if e1 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from date, expected YYYY-MM-DD"})
			return
		}
		if e2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to date, expected YYYY-MM-DD"})
			return
		}
		query := ctl.db.WithContext(ctx).Omit(historyOmittedAnalysisColumns...).Where("profile_id = ?", pid)
		if archived == "true" {
			query = query.Where("archived_at IS NOT NULL")
		} else {
			query = query.Where("archived_at IS NULL")
		}
		if missing != "" {
			query = query.Where("UPPER(status) = ?", "COMPLETED")
		}
		if err := query.Where("created_at >= ? AND created_at < ?", start, end.AddDate(0, 0, 1)).Order("created_at desc").Find(&results).Error; err != nil {
			logger.Log.Error("failed to fetch history", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
			return
		}
		sessionsMap, err := populateHistoryInputStatus(ctx, ctl.db, uint(pid), results)
		if err != nil {
			logger.Log.Error("failed to populate history input status", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
			return
		}
		sessionByID = sessionsMap
		if missing != "" {
			filtered := results[:0]
			for i := range results {
				if matchesHistoryMissingFilter(results[i].InputStatus, missing) {
					filtered = append(filtered, results[i])
				}
			}
			results = filtered
		}
	} else {
		limit, e := strconv.Atoi(c.Query("limit"))
		if e != nil || limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		var beforeID uint64
		if before, e := strconv.ParseUint(c.Query("before_id"), 10, 32); e == nil && before > 0 {
			beforeID = before
		}
		if missing == "" {
			query := ctl.db.WithContext(ctx).Omit(historyOmittedAnalysisColumns...).Where("profile_id = ?", pid)
			if archived == "true" {
				query = query.Where("archived_at IS NOT NULL")
			} else {
				query = query.Where("archived_at IS NULL")
			}
			if beforeID > 0 {
				query = query.Where("id < ?", beforeID)
			}
			if err := query.Order("id desc").Limit(limit).Find(&results).Error; err != nil {
				logger.Log.Error("failed to fetch history", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
				return
			}
			sessionsMap, err := populateHistoryInputStatus(ctx, ctl.db, uint(pid), results)
			if err != nil {
				logger.Log.Error("failed to populate history input status", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
				return
			}
			sessionByID = sessionsMap
		} else {
			scanLimit := limit * 3
			if scanLimit < 60 {
				scanLimit = 60
			}
			if scanLimit > 200 {
				scanLimit = 200
			}
			cursor := beforeID
			results = make([]db.AnalysisResult, 0, limit)
			sessionByID = make(map[string]db.Session, limit)
			for len(results) < limit {
				batchQuery := ctl.db.WithContext(ctx).
					Omit(historyOmittedAnalysisColumns...).
					Where("profile_id = ? AND archived_at IS NULL AND UPPER(status) = ?", pid, "COMPLETED")
				if cursor > 0 {
					batchQuery = batchQuery.Where("id < ?", cursor)
				}
				var batch []db.AnalysisResult
				if err := batchQuery.Order("id desc").Limit(scanLimit).Find(&batch).Error; err != nil {
					logger.Log.Error("failed to fetch history", zap.Error(err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
					return
				}
				if len(batch) == 0 {
					break
				}
				batchSessions, err := populateHistoryInputStatus(ctx, ctl.db, uint(pid), batch)
				if err != nil {
					logger.Log.Error("failed to populate history input status", zap.Error(err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
					return
				}
				for i := range batch {
					if matchesHistoryMissingFilter(batch[i].InputStatus, missing) {
						sid := strings.TrimSpace(batch[i].SessionID)
						if sess, ok := batchSessions[sid]; ok {
							sessionByID[sid] = sess
						}
						results = append(results, batch[i])
						if len(results) == limit {
							break
						}
					}
				}
				cursor = uint64(batch[len(batch)-1].ID)
				if len(batch) < scanLimit {
					break
				}
			}
		}
	}
	summaryMetrics, err := populateHistorySummariesAndHighlights(ctx, ctl.db, uint(pid), results, sessionByID, ctl.historySummaryCacheEnabled)
	if err != nil {
		logger.Log.Error("failed to populate history summaries and highlights", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}
	populateMissingWODDescriptions(ctx, ctl.db, results)
	respPayload := compactAnalysisSummaries(normalizeHighlightResultsForResponseWithSchema(results, workoutLoadSchemaVersion(c)))
	encoded, err := json.Marshal(respPayload)
	if err != nil {
		logger.Log.Error("failed to marshal history response", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}
	dbQueryMs := (qStats.Snapshot().Duration - qBefore).Milliseconds()
	logger.Log.Info("history fetched",
		zap.Uint64("profile_id", pid),
		zap.Int("sessions_total", summaryMetrics.SessionsTotal),
		zap.Int("summary_cache_hits", summaryMetrics.SummaryCacheHits),
		zap.Int("summary_rebuilt", summaryMetrics.SummaryRebuilt),
		zap.Int("chunk_rows_loaded", summaryMetrics.ChunkRowsLoaded),
		zap.Int64("db_query_ms", dbQueryMs),
		zap.Int64("summary_ms", summaryMetrics.SummaryMs),
		zap.Int("response_bytes", len(encoded)),
	)
	c.Data(http.StatusOK, "application/json; charset=utf-8", encoded)
}

var errSessionProcessingActive = errors.New("session processing is active")

func (ctl *Controller) ArchiveHistory(c *gin.Context)   { ctl.setHistoryArchived(c, true) }
func (ctl *Controller) UnarchiveHistory(c *gin.Context) { ctl.setHistoryArchived(c, false) }

func (ctl *Controller) setHistoryArchived(c *gin.Context, archive bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if !ctl.assertOwnsAnalysis(c, uint(id)) {
		return
	}
	ctx := c.Request.Context()
	var row db.AnalysisResult
	if err := ctl.db.WithContext(ctx).First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "analysis not found"})
		return
	}
	var revision int64
	err = ctl.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Always lock profile before analysis, including sensor calibration writes.
		var profile db.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&profile, row.ProfileID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; err != nil {
			return err
		}
		revision = profile.WorkoutHistoryRevision
		if (row.ArchivedAt != nil) == archive {
			return nil
		}
		now := time.Now().UTC()
		if archive {
			active, err := hasActiveSessionProcessing(tx, &row)
			if err != nil {
				return err
			}
			if active {
				return errSessionProcessingActive
			}
			updates := map[string]any{"archived_at": now, "automatic_processing_cancelled_at": gorm.Expr("COALESCE(automatic_processing_cancelled_at, ?)", now), "updated_at": now}
			if row.SensorState == db.SensorStateUploading {
				updates["sensor_state"], updates["sensor_next_attempt_at"] = db.SensorStateCancelled, nil
				updates["sensor_version"] = gorm.Expr("sensor_version + 1")
				var processing map[string]any
				_ = json.Unmarshal(row.SensorProcessing, &processing)
				if processing == nil {
					processing = map[string]any{}
				}
				processing["last_error_code"] = "SESSION_ARCHIVED"
				delete(processing, "lease_token")
				delete(processing, "lease_started_at")
				delete(processing, "retry_not_before")
				raw, _ := json.Marshal(processing)
				updates["sensor_processing"] = db.JSONDocument(raw)
			}
			updates["analysis_summary"] = interruptArchiveEnrichment(row.AnalysisSummary)
			updates["agentic_highlight_analysis"] = interruptArchiveEnrichment(row.AgenticHighlightAnalysis)
			if err := tx.Model(&db.AnalysisResult{}).Where("id = ?", id).Updates(updates).Error; err != nil {
				return err
			}
			if err := tx.Model(&db.RecordingMediaRun{}).Where("profile_id = ? AND session_id = ? AND version = ? AND state NOT IN ('COMPLETED','CANCELLED')", row.ProfileID, row.SessionID, db.RecordingMediaVersion).Updates(map[string]any{"state": "CANCELLED", "next_attempt_at": nil, "lease_token": "", "lease_expires_at": nil, "safe_error": "SESSION_ARCHIVED"}).Error; err != nil {
				return err
			}
			for _, model := range []any{&db.ChunkMediaPreparation{}, &db.AnalysisVideoPreparation{}} {
				if err := tx.Model(model).Where("run_id IN (?) AND state IN ('PENDING','RUNNING')", tx.Model(&db.RecordingMediaRun{}).Select("id").Where("profile_id = ? AND session_id = ? AND version = ?", row.ProfileID, row.SessionID, db.RecordingMediaVersion)).Updates(map[string]any{"state": "CANCELLED", "next_attempt_at": nil, "lease_token": "", "lease_expires_at": nil, "safe_error": "SESSION_ARCHIVED"}).Error; err != nil {
					return err
				}
			}
			var sess db.Session
			sessErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Select("id", "activity_summary", "activity_summary_cache_version").
				Where("session_id = ? AND profile_id = ?", row.SessionID, row.ProfileID).
				First(&sess).Error
			if sessErr != nil && !errors.Is(sessErr, gorm.ErrRecordNotFound) {
				return sessErr
			}
			if sessErr == nil {
				if err := tx.Exec(
					`UPDATE sessions
					 SET activity_summary = jsonb_set(activity_summary, '{review_state}', '"unavailable"'::jsonb),
					     activity_summary_cache_version = CASE WHEN activity_summary_cache_version = ? THEN ? ELSE 0 END,
					     updated_at = ?
					 WHERE id = ? AND activity_summary->>'review_state' = 'provisional'`,
					activity.SummaryCacheVersion, activity.SummaryCacheVersion, now, sess.ID,
				).Error; err != nil {
					return err
				}
			}
		} else if err := tx.Model(&db.AnalysisResult{}).Where("id = ?", id).Updates(map[string]any{"archived_at": nil, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := db.RecomputeProfileENMOCalibrationTx(ctx, tx, row.ProfileID, false); err != nil {
			return err
		}
		revision++
		return tx.Table("profiles").Where("id = ?", row.ProfileID).Update("workout_history_revision", revision).Error
	})
	if errors.Is(err, errSessionProcessingActive) {
		c.JSON(http.StatusConflict, gin.H{"error": "session is still processing", "error_code": "SESSION_PROCESSING_ACTIVE"})
		return
	}
	if err != nil {
		logger.Log.Error("failed to change history archive state", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to change archive state"})
		return
	}
	message := "history unarchived"
	if archive {
		message = "history archived"
	}
	c.JSON(http.StatusOK, gin.H{"message": message, "history_revision": strconv.FormatInt(revision, 10)})
}

func isTerminalAnalysisStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETED", "FAILED", "CANCELLED":
		return true
	default:
		return false
	}
}

func hasActiveEnrichmentWork(raw db.JSONDocument) bool {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return false
	}
	running := func(status any) bool {
		s, _ := status.(string)
		return s == "pending" || s == "preparing" || s == "running"
	}
	if running(value["status"]) {
		return true
	}
	if items, ok := value["items"].([]any); ok {
		for _, item := range items {
			if m, ok := item.(map[string]any); ok && running(m["status"]) {
				return true
			}
		}
	}
	return false
}

func hasActiveSessionProcessing(tx *gorm.DB, row *db.AnalysisResult) (bool, error) {
	if !isTerminalAnalysisStatus(row.Status) {
		return true, nil
	}
	if row.SensorState == db.SensorStatePending || row.SensorState == db.SensorStateRunning {
		return true, nil
	}
	if hasActiveEnrichmentWork(row.AnalysisSummary) || hasActiveEnrichmentWork(row.AgenticHighlightAnalysis) {
		return true, nil
	}
	var activeMedia int64
	if err := tx.Model(&db.RecordingMediaRun{}).Where("profile_id = ? AND session_id = ? AND version = ? AND state NOT IN ('COMPLETED','FAILED','CANCELLED')", row.ProfileID, row.SessionID, db.RecordingMediaVersion).Limit(1).Count(&activeMedia).Error; err != nil {
		return false, err
	}
	if activeMedia > 0 {
		return true, nil
	}
	for _, table := range []string{"chunk_media_preparations", "analysis_video_preparations"} {
		if err := tx.Table(table+" p").Joins("JOIN recording_media_runs r ON r.id = p.run_id").Where("r.profile_id = ? AND r.session_id = ? AND r.version = ? AND p.state IN ('PENDING','RUNNING')", row.ProfileID, row.SessionID, db.RecordingMediaVersion).Limit(1).Count(&activeMedia).Error; err != nil {
			return false, err
		}
		if activeMedia > 0 {
			return true, nil
		}
	}
	for _, table := range []string{"chunk_analysis_results", "highlight_results", "chunk_reanalysis_runs", "session_reanalysis_runs", "recognition_review_runs"} {
		var count int64
		if err := tx.Table(table).Where("session_id = ? AND profile_id = ? AND UPPER(status) IN ('QUEUED','PENDING','UPLOADED','PROCESSING','MERGING','RUNNING')", row.SessionID, row.ProfileID).Limit(1).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	var activeReviews int64
	if err := tx.Table("sessions").Where("session_id = ? AND profile_id = ? AND activity_summary->>'review_state' IN ('queued','running')", row.SessionID, row.ProfileID).Limit(1).Count(&activeReviews).Error; err != nil {
		return false, err
	}
	return activeReviews > 0, nil
}

func interruptArchiveEnrichment(raw db.JSONDocument) db.JSONDocument {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return db.JSONDocument(`{}`)
	}
	active := func(status any) bool {
		s, _ := status.(string)
		return s == "pending" || s == "preparing" || s == "running" || s == "stale"
	}
	if active(value["status"]) {
		value["status"], value["error"] = "interrupted", "SESSION_ARCHIVED"
		if owned, _ := value["owned_upload"].(bool); owned {
			value["cleanup_pending"] = true
		}
	}
	if items, ok := value["items"].([]any); ok {
		for _, item := range items {
			if m, ok := item.(map[string]any); ok && active(m["status"]) {
				m["status"], m["error"] = "interrupted", "SESSION_ARCHIVED"
			}
		}
	}
	result, _ := json.Marshal(value)
	return db.JSONDocument(result)
}

// requireSessionProcessing checks entry points; worker publication transactions
// repeat the check so archive racing an enqueue cannot resurrect work.
func (ctl *Controller) requireSessionProcessing(c *gin.Context, sessionID string, profileID uint, automatic bool) bool {
	var row db.AnalysisResult
	err := ctl.db.WithContext(c.Request.Context()).Select("id,profile_id,archived_at,automatic_processing_cancelled_at").Where("session_id = ?", sessionID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to check session state"})
		return false
	}
	if profileID != 0 && row.ProfileID != profileID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized for this session"})
		return false
	}
	if row.ArchivedAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "session is archived", "error_code": "SESSION_ARCHIVED"})
		return false
	}
	if automatic && row.AutomaticProcessingCancelledAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "session processing was cancelled", "error_code": "SESSION_PROCESSING_CANCELLED"})
		return false
	}
	return true
}

func (ctl *Controller) enqueueSessionAutomaticTask(c *gin.Context, sessionID string, profileID uint, task *asynq.Task) (*asynq.TaskInfo, error) {
	return ctl.enqueueSessionAutomaticTaskWithMetadata(c, sessionID, profileID, task, nil)
}

type sessionUploadMetadata struct {
	Movements       []string
	WODDescription  string
	AppearanceHints string
}

const sessionUploadAcceptanceTimeout = 15 * time.Second

func (ctl *Controller) enqueueSessionAutomaticTaskWithMetadata(c *gin.Context, sessionID string, profileID uint, task *asynq.Task, metadata *sessionUploadMetadata) (*asynq.TaskInfo, error) {
	ctx := c.Request.Context()
	if metadata != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Once acceptance starts, a caller disconnect must not roll back source
		// metadata after Redis has accepted the task. Bound the detached work.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), sessionUploadAcceptanceTimeout)
		defer cancel()
	}
	var info *asynq.TaskInfo
	err := db.WithSessionAutomaticWork(ctx, ctl.db, profileID, sessionID, func(tx *gorm.DB) error {
		switch task.Type() {
		case worker.TypeChunkAnalysis, worker.TypeChunkAnalysisWithSession, worker.TypeMergeChunks, worker.TypeVideoAnalysis:
			if err := markLegacyRecordingMediaTx(tx, profileID, sessionID); err != nil {
				return err
			}
		}
		// Metadata is source evidence too: serialize it with archive and roll it
		// back when this request cannot enqueue its processing task.
		if metadata != nil {
			if err := persistSessionMovementHints(ctx, tx, sessionID, profileID, metadata.Movements); err != nil {
				return err
			}
			if err := persistSessionWODDescription(ctx, tx, sessionID, profileID, metadata.WODDescription); err != nil {
				return err
			}
			if err := persistSessionAppearanceHints(ctx, tx, sessionID, profileID, &AppearanceInput{Appearance: metadata.AppearanceHints}); err != nil {
				return err
			}
		}
		var err error
		info, err = ctl.queueClient.Enqueue(task)
		return err
	})
	return info, err
}

func writeSessionProcessingConflict(c *gin.Context, err error) bool {
	if errors.Is(err, errRecordingMediaConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "recording media pipeline cannot change", "error_code": "RECORDING_MEDIA_CONFLICT"})
		return true
	}
	if !errors.Is(err, db.ErrSessionWorkCancelled) {
		return false
	}
	c.JSON(http.StatusConflict, gin.H{"error": "session is archived or processing was cancelled", "error_code": "SESSION_PROCESSING_CANCELLED"})
	return true
}

func lockActiveSession(tx *gorm.DB, sessionID string, profileID uint) error {
	var row db.AnalysisResult
	err := tx.Select("id,profile_id,archived_at").Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id = ?", sessionID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.ProfileID != profileID || row.ArchivedAt != nil {
		return db.ErrSessionWorkCancelled
	}
	return nil
}
