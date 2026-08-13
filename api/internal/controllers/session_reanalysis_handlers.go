package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sessionReanalysisDailyQuota  = int64(5)
	sessionReanalysisWindow      = 24 * time.Hour
	sessionReanalysisRunLimit    = 20
	sessionReanalysisTaskMaxTry  = 2
	sessionReanalysisTaskTimeout = 60 * time.Minute
)

var (
	errSessionReanalysisActive      = errors.New("session re-analysis already active")
	errSessionReanalysisChunkActive = errors.New("chunk re-analysis still active")
	errSessionReanalysisQuota       = errors.New("session re-analysis quota reached")
	errSessionReanalysisKeyConflict = errors.New("session re-analysis idempotency conflict")
)

// CreateSessionReanalysis queues an append-only whole-workout candidate. The
// caller supplies only an idempotency key; the server selects and snapshots
// the owned session source.
func (ctl *Controller) CreateSessionReanalysis(c *gin.Context) {
	if !ctl.requireSessionReanalysisEnabled(c) {
		return
	}
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}

	req, err := decodeCreateSessionReanalysisRequest(c.Request.Body)
	if err != nil {
		logger.Log.Error("failed to parse request body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body; unexpected or invalid fields"})
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	if req.ClientRequestID == "" || len(req.ClientRequestID) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client_request_id must be between 1 and 128 characters"})
		return
	}

	if strings.TrimSpace(req.AppearanceHints) != "" {
		if err := persistSessionAppearanceHints(c.Request.Context(), ctl.db, sessionID, profileID, &AppearanceInput{Appearance: req.AppearanceHints}); err != nil {
			logger.Log.Error("failed to persist appearance hints for reanalysis", zap.String("session_id", sessionID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist appearance hints"})
			return
		}
	}

	var existing db.SessionReanalysisRun
	err = ctl.db.WithContext(c.Request.Context()).
		Where("profile_id = ? AND client_request_id = ?", profileID, req.ClientRequestID).
		First(&existing).Error
	if err == nil {
		if existing.SessionID != sessionID {
			c.JSON(http.StatusConflict, gin.H{"error": "client_request_id was already used for another session"})
			return
		}
		if err := ctl.ensureSessionReanalysisTask(c.Request.Context(), &existing); err != nil {
			logger.Log.Error("failed to recover idempotent session re-analysis task", zap.Uint("run_id", existing.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue session re-analysis"})
			return
		}
		c.JSON(http.StatusAccepted, CreateSessionReanalysisResponse{
			RunID: existing.ID, TaskID: existing.TaskID, Status: existing.Status,
		})
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Log.Error("failed to check session re-analysis idempotency", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session re-analysis"})
		return
	}

	readiness, sourceURI, err := ctl.loadSessionReanalysisReadiness(c.Request.Context(), sessionID, profileID)
	if err != nil {
		logger.Log.Error("failed to check session re-analysis readiness", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session re-analysis"})
		return
	}
	if !readiness.VideoAvailable || sourceURI == "" {
		// Preserve idempotency if another request committed between the initial
		// lookup and object listing.
		if lookupErr := ctl.db.WithContext(c.Request.Context()).
			Where("profile_id = ? AND client_request_id = ?", profileID, req.ClientRequestID).
			First(&existing).Error; lookupErr == nil && existing.SessionID == sessionID {
			if err := ctl.ensureSessionReanalysisTask(c.Request.Context(), &existing); err != nil {
				logger.Log.Error("failed to recover idempotent session re-analysis task", zap.Uint("run_id", existing.ID), zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue session re-analysis"})
				return
			}
			c.JSON(http.StatusAccepted, CreateSessionReanalysisResponse{RunID: existing.ID, TaskID: existing.TaskID, Status: existing.Status})
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "the session video is unavailable"})
		return
	}

	originalSnapshot, err := ctl.captureOriginalSessionAnalysis(c.Request.Context(), sessionID, profileID)
	if err != nil {
		logger.Log.Error("failed to snapshot original session analysis", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session re-analysis"})
		return
	}
	run := db.SessionReanalysisRun{
		SessionID:                sessionID,
		ProfileID:                profileID,
		ClientRequestID:          req.ClientRequestID,
		TaskID:                   uuid.NewString(),
		Status:                   db.SessionReanalysisStatusQueued,
		SourceGCSURI:             sourceURI,
		SourceContextSnapshot:    db.JSONDocument(`{}`),
		OriginalAnalysisSnapshot: originalSnapshot,
		SessionScore:             `{}`,
		Model:                    strings.TrimSpace(req.Model),
	}
	idempotentExisting := false
	createErr := ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// Serialize quota and active-run checks across every session for this
		// profile. Without this lock, concurrent requests for different sessions
		// can both observe four recent runs and exceed the rolling quota.
		var lockedProfile db.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&lockedProfile, profileID).Error; err != nil {
			return err
		}

		var concurrent db.SessionReanalysisRun
		err := tx.Where("profile_id = ? AND client_request_id = ?", profileID, req.ClientRequestID).First(&concurrent).Error
		if err == nil {
			if concurrent.SessionID != sessionID {
				return errSessionReanalysisKeyConflict
			}
			run = concurrent
			idempotentExisting = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var count int64
		if err := tx.Model(&db.ChunkReanalysisRun{}).
			Where("session_id = ? AND profile_id = ? AND status IN ?", sessionID, profileID,
				[]string{db.ChunkReanalysisStatusQueued, db.ChunkReanalysisStatusRunning}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errSessionReanalysisChunkActive
		}
		if err := tx.Model(&db.SessionReanalysisRun{}).
			Where("session_id = ? AND profile_id = ? AND status IN ?", sessionID, profileID,
				[]string{db.SessionReanalysisStatusQueued, db.SessionReanalysisStatusRunning}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errSessionReanalysisActive
		}
		if err := tx.Model(&db.SessionReanalysisRun{}).
			Where("profile_id = ? AND created_at >= ?", profileID, time.Now().Add(-sessionReanalysisWindow)).Count(&count).Error; err != nil {
			return err
		}
		if count >= sessionReanalysisDailyQuota {
			return errSessionReanalysisQuota
		}
		return tx.Create(&run).Error
	})
	if createErr != nil {
		switch {
		case errors.Is(createErr, errSessionReanalysisChunkActive):
			c.JSON(http.StatusConflict, gin.H{"error": "chunk re-analyses must finish before whole-workout re-analysis"})
		case errors.Is(createErr, errSessionReanalysisActive):
			c.JSON(http.StatusConflict, gin.H{"error": "a whole-workout re-analysis is already active for this session"})
		case errors.Is(createErr, errSessionReanalysisQuota):
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "daily session re-analysis limit reached"})
		case errors.Is(createErr, errSessionReanalysisKeyConflict), isSessionReanalysisUniqueConflict(createErr):
			c.JSON(http.StatusConflict, gin.H{"error": "a whole-workout re-analysis is already active or the request was already submitted"})
		default:
			logger.Log.Error("failed to create session re-analysis run", zap.Error(createErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session re-analysis"})
		}
		return
	}
	if err := ctl.ensureSessionReanalysisTask(c.Request.Context(), &run); err != nil {
		if !idempotentExisting {
			ctl.markSessionReanalysisEnqueueFailed(c.Request.Context(), run.ID)
		}
		logger.Log.Error("failed to enqueue session re-analysis", zap.Uint("run_id", run.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue session re-analysis"})
		return
	}
	c.JSON(http.StatusAccepted, CreateSessionReanalysisResponse{
		RunID: run.ID, TaskID: run.TaskID, Status: run.Status,
	})
}

func (ctl *Controller) ListSessionReanalyses(c *gin.Context) {
	if !ctl.requireSessionReanalysisEnabled(c) {
		return
	}
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}

	runs := make([]db.SessionReanalysisRun, 0)
	if err := ctl.db.WithContext(c.Request.Context()).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("created_at DESC, id DESC").Limit(sessionReanalysisRunLimit).Find(&runs).Error; err != nil {
		logger.Log.Error("failed to list session re-analyses", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list session re-analyses"})
		return
	}
	readiness, _, err := ctl.loadSessionReanalysisReadiness(c.Request.Context(), sessionID, profileID)
	if err != nil {
		logger.Log.Error("failed to load session re-analysis readiness", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list session re-analyses"})
		return
	}

	responses := make([]SessionReanalysisRunResponse, 0, len(runs))
	for i := range runs {
		responses = append(responses, sessionReanalysisRunResponse(runs[i]))
	}
	c.JSON(http.StatusOK, ListSessionReanalysesResponse{Runs: responses, Readiness: readiness})
}

func (ctl *Controller) GetSessionReanalysis(c *gin.Context) {
	if !ctl.requireSessionReanalysisEnabled(c) {
		return
	}
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}
	runID, err := strconv.ParseUint(strings.TrimSpace(c.Param("run_id")), 10, 64)
	if err != nil || runID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run_id"})
		return
	}

	var run db.SessionReanalysisRun
	if err := ctl.db.WithContext(c.Request.Context()).Where(
		"id = ? AND session_id = ? AND profile_id = ?", uint(runID), sessionID, profileID,
	).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session re-analysis not found"})
			return
		}
		logger.Log.Error("failed to load session re-analysis", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load session re-analysis"})
		return
	}
	c.JSON(http.StatusOK, sessionReanalysisRunResponse(run))
}

func (ctl *Controller) loadSessionReanalysisReadiness(ctx context.Context, sessionID string, profileID uint) (SessionReanalysisReadinessResponse, string, error) {
	readiness := SessionReanalysisReadinessResponse{}
	if err := ctl.db.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).
		Where("session_id = ? AND profile_id = ? AND status IN ?", sessionID, profileID,
			[]string{db.ChunkReanalysisStatusQueued, db.ChunkReanalysisStatusRunning}).
		Count(&readiness.ActiveChunkRuns).Error; err != nil {
		return readiness, "", err
	}

	var active db.SessionReanalysisRun
	err := ctl.db.WithContext(ctx).
		Where("session_id = ? AND profile_id = ? AND status IN ?", sessionID, profileID,
			[]string{db.SessionReanalysisStatusQueued, db.SessionReanalysisStatusRunning}).
		Order("created_at DESC").First(&active).Error
	if err == nil {
		readiness.ActiveSessionRunID = &active.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return readiness, "", err
	}

	var recentCount int64
	if err := ctl.db.WithContext(ctx).Model(&db.SessionReanalysisRun{}).
		Where("profile_id = ? AND created_at >= ?", profileID, time.Now().Add(-sessionReanalysisWindow)).
		Count(&recentCount).Error; err != nil {
		return readiness, "", err
	}

	sourceURI := ""
	if strings.TrimSpace(ctl.bucketName) != "" {
		objects, listErr := ctl.listSessionReanalysisVideoObjects(ctx, profileID, sessionID)
		if listErr != nil {
			logger.Log.Warn("failed to list session video for re-analysis readiness",
				zap.String("session_id", sessionID), zap.Error(listErr))
		} else if objectName := selectSessionReanalysisVideoObject(objects); objectName != "" {
			readiness.VideoAvailable = true
			sourceURI = fmt.Sprintf("gs://%s/%s", ctl.bucketName, objectName)
		}
	}

	switch {
	case readiness.ActiveSessionRunID != nil:
		readiness.BlockedReason = "a whole-workout re-analysis is already active"
	case readiness.ActiveChunkRuns > 0:
		readiness.BlockedReason = "chunk re-analyses are still active"
	case !readiness.VideoAvailable:
		readiness.BlockedReason = "session video is unavailable"
	case recentCount >= sessionReanalysisDailyQuota:
		readiness.BlockedReason = "daily session re-analysis limit reached"
	default:
		readiness.CanCreate = true
	}
	return readiness, sourceURI, nil
}

func (ctl *Controller) listSessionReanalysisVideoObjects(ctx context.Context, profileID uint, sessionID string) ([]string, error) {
	objects, err := ctl.storageClient.ListObjects(ctx, fmt.Sprintf("videos/%d/%s/", profileID, sessionID))
	if err != nil {
		return nil, err
	}
	if selectSessionReanalysisVideoObject(objects) != "" {
		return objects, nil
	}
	legacy, err := ctl.storageClient.ListObjects(ctx, "videos/"+sessionID+"_")
	if err != nil {
		return nil, err
	}
	return append(objects, legacy...), nil
}

// selectSessionReanalysisVideoObject only accepts known whole-session assets.
// A random mobile chunk filename is not distinguishable by prefix alone, so
// arbitrary MP4s and derived hardsubs/highlights must never be a fallback.
func selectSessionReanalysisVideoObject(objects []string) string {
	best := ""
	bestScore := 0
	for _, object := range objects {
		base := strings.ToLower(filepath.Base(object))
		score := 0
		switch {
		case base == "analysis.mp4":
			score = 40
		case base == "merged.mp4":
			score = 30
		case strings.Contains(base, "_merged_") && strings.HasSuffix(base, ".mp4"):
			score = 20
		case strings.Contains(base, "_encoded") && strings.HasSuffix(base, ".mp4"):
			score = 10
		}
		if score > bestScore {
			best, bestScore = object, score
		}
	}
	return best
}

func (ctl *Controller) captureOriginalSessionAnalysis(ctx context.Context, sessionID string, profileID uint) (db.JSONDocument, error) {
	snapshot := map[string]any{}
	var analysis db.AnalysisResult
	err := ctl.db.WithContext(ctx).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("created_at DESC, id DESC").First(&analysis).Error
	if err == nil {
		snapshot = map[string]any{
			"analysis_id":        analysis.ID,
			"status":             analysis.Status,
			"output":             analysis.Output,
			"highlight_segments": analysis.HighlightSegments,
			"session_score":      analysis.SessionScore,
			"wod_description":    analysis.WODDescription,
			"analysis_type":      analysis.AnalysisType,
			"created_at":         analysis.CreatedAt,
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return db.JSONDocument(raw), nil
}

func sessionReanalysisRunResponse(run db.SessionReanalysisRun) SessionReanalysisRunResponse {
	response := SessionReanalysisRunResponse{
		ID: run.ID, SessionID: run.SessionID, TaskID: run.TaskID, Status: run.Status,
		Model: run.Model, PromptVersion: run.PromptVersion, PromptHash: run.PromptHash,
		SchemaVersion: run.SchemaVersion, InputTokens: run.PromptTokens,
		OutputTokens: run.CandidateTokens, DurationMs: run.DurationMs, Error: run.SafeError,
		CreatedAt: run.CreatedAt, StartedAt: run.StartedAt, CompletedAt: run.CompletedAt, UpdatedAt: run.UpdatedAt,
	}
	if run.Status == db.SessionReanalysisStatusCompleted {
		normalizedHighlights := normalizeHighlightJSONForResponse(run.HighlightSegments)
		highlights := any(normalizedHighlights)
		var parsedHighlights any
		if strings.TrimSpace(normalizedHighlights) != "" && json.Unmarshal([]byte(normalizedHighlights), &parsedHighlights) == nil {
			highlights = parsedHighlights
		}
		response.Candidate = &SessionReanalysisCandidateResponse{
			Output: run.Output, HighlightSegments: highlights,
			SessionScore: run.SessionScore, WorkoutType: run.WorkoutType,
		}
		response.TokenUsage = &ChunkReanalysisTokenUsageResponse{
			PromptTokens: run.PromptTokens, CandidateTokens: run.CandidateTokens, TotalTokens: run.TotalTokens,
		}
	}
	return response
}

func (ctl *Controller) requireSessionReanalysisEnabled(c *gin.Context) bool {
	if ctl.enableSessionReanalysis {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session re-analysis is disabled"})
	return false
}

// ensureSessionReanalysisTask makes a queued run recoverable through the
// endpoint's idempotency key. The stable task ID is persisted before enqueue;
// retrying the request either restores a missing task or observes the exact
// task already present in Asynq.
func (ctl *Controller) ensureSessionReanalysisTask(ctx context.Context, run *db.SessionReanalysisRun) error {
	if run.Status != db.SessionReanalysisStatusQueued {
		return nil
	}
	if run.TaskID == "" {
		candidate := uuid.NewString()
		result := ctl.db.WithContext(ctx).Model(&db.SessionReanalysisRun{}).
			Where("id = ? AND task_id = ''", run.ID).Update("task_id", candidate)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			run.TaskID = candidate
		} else if err := ctl.db.WithContext(ctx).Select("task_id", "status").First(run, run.ID).Error; err != nil {
			return err
		}
		if run.Status != db.SessionReanalysisStatusQueued {
			return nil
		}
	}

	task, err := worker.NewSessionDebugReanalysisTask(run.ID)
	if err != nil {
		return err
	}
	_, err = ctl.queueClient.Enqueue(task,
		asynq.TaskID(run.TaskID),
		asynq.MaxRetry(sessionReanalysisTaskMaxTry),
		asynq.Timeout(sessionReanalysisTaskTimeout))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

func (ctl *Controller) markSessionReanalysisEnqueueFailed(ctx context.Context, runID uint) {
	now := time.Now().UTC()
	_ = ctl.db.WithContext(ctx).Model(&db.SessionReanalysisRun{}).Where("id = ?", runID).Updates(map[string]any{
		"status": db.SessionReanalysisStatusFailed, "safe_error": "The session re-analysis task could not be queued.",
		"completed_at": now,
	}).Error
}

func isSessionReanalysisUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "23505") || strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "idx_session_reanalysis_runs_one_active_per_session")
}

func decodeCreateSessionReanalysisRequest(reader io.Reader) (CreateSessionReanalysisRequest, error) {
	var request CreateSessionReanalysisRequest
	decoder := json.NewDecoder(io.LimitReader(reader, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return CreateSessionReanalysisRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return CreateSessionReanalysisRequest{}, errors.New("request body must contain one JSON object")
	}
	return request, nil
}
