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
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	chunkReanalysisDailyQuota  = int64(20)
	chunkReanalysisWindow      = 24 * time.Hour
	chunkReanalysisRunLimit    = 50
	chunkReanalysisTaskMaxTry  = 2
	chunkReanalysisTaskTimeout = 30 * time.Minute
)

type chunkReanalysisTarget struct {
	ID                uint
	SessionID         string
	ProfileID         uint
	FilePath          string
	ExerciseType      string
	Status            string
	Output            string
	ObservedSignals   string
	HeartRateBPM      int
	StartSecs         *float64
	EndSecs           *float64
	MediaStartSecs    *float64
	MediaEndSecs      *float64
	WorkoutConfidence float64
	MotionScore       *float64
	SkipReason        string
}

// GetChunkPlayURL signs the server-resolved video source for one owned chunk.
// It never accepts a storage URI from the caller.
func (ctl *Controller) GetChunkPlayURL(c *gin.Context) {
	if ctl.storageClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is not configured"})
		return
	}

	sessionID, chunkID, ok := parseChunkReanalysisPath(c)
	if !ok {
		return
	}
	target, ok := ctl.loadOwnedChunkReanalysisTarget(c, sessionID, chunkID)
	if !ok {
		return
	}

	objectName, sourceKind, mediaStart, mediaEnd, err := ctl.resolveChunkPlaybackSource(c.Request.Context(), target)
	if err != nil {
		if errors.Is(err, errChunkIntervalUnavailable) {
			c.JSON(http.StatusConflict, gin.H{"error": "exact media interval is unavailable"})
			return
		}
		if errors.Is(err, errChunkVideoUnavailable) {
			c.JSON(http.StatusNotFound, gin.H{"error": "video is unavailable"})
			return
		}
		logger.Log.Error("failed to resolve chunk playback source", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve video"})
		return
	}

	signedURL, err := ctl.storageClient.GenerateSignedURL(objectName, http.MethodGet, playURLExpiry)
	if err != nil {
		logger.Log.Error("failed to sign chunk playback URL",
			zap.String("session_id", sessionID),
			zap.Uint("chunk_id", chunkID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate play URL"})
		return
	}

	c.JSON(http.StatusOK, ChunkPlayURLResponse{
		SessionID:      sessionID,
		ChunkID:        chunkID,
		PlayURL:        signedURL,
		SourceKind:     sourceKind,
		MediaStartSecs: mediaStart,
		MediaEndSecs:   mediaEnd,
		ExpiresAt:      time.Now().Add(playURLExpiry).UTC().Format(time.RFC3339),
	})
}

// CreateChunkReanalysis queues an unbiased, current-analyzer debug attempt.
// The only caller-controlled value is an idempotency key.
func (ctl *Controller) CreateChunkReanalysis(c *gin.Context) {
	if !ctl.requireChunkReanalysisEnabled(c) {
		return
	}
	if ctl.db == nil || ctl.queueClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "re-analysis service is not configured"})
		return
	}

	sessionID, chunkID, ok := parseChunkReanalysisPath(c)
	if !ok {
		return
	}
	target, ok := ctl.loadOwnedChunkReanalysisTarget(c, sessionID, chunkID)
	if !ok {
		return
	}

	req, err := decodeCreateChunkReanalysisRequest(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body; only client_request_id is allowed"})
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	if req.ClientRequestID == "" || len(req.ClientRequestID) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client_request_id must be between 1 and 128 characters"})
		return
	}

	var existing db.ChunkReanalysisRun
	err = ctl.db.WithContext(c.Request.Context()).
		Where("profile_id = ? AND client_request_id = ?", target.ProfileID, req.ClientRequestID).
		First(&existing).Error
	if err == nil {
		if existing.SessionID != sessionID || existing.ChunkAnalysisResultID != chunkID {
			c.JSON(http.StatusConflict, gin.H{"error": "client_request_id was already used for another chunk"})
			return
		}
		c.JSON(http.StatusAccepted, CreateChunkReanalysisResponse{
			RunID: existing.ID, TaskID: existing.TaskID, Status: existing.Status,
		})
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Log.Error("failed to check re-analysis idempotency", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create re-analysis"})
		return
	}

	var activeCount int64
	if err := ctl.db.WithContext(c.Request.Context()).Model(&db.ChunkReanalysisRun{}).
		Where("chunk_analysis_result_id = ? AND status IN ?", chunkID,
			[]string{db.ChunkReanalysisStatusQueued, db.ChunkReanalysisStatusRunning}).
		Count(&activeCount).Error; err != nil {
		logger.Log.Error("failed to check active re-analysis", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create re-analysis"})
		return
	}
	if activeCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "a re-analysis is already active for this chunk"})
		return
	}

	var recentCount int64
	if err := ctl.db.WithContext(c.Request.Context()).Model(&db.ChunkReanalysisRun{}).
		Where("profile_id = ? AND created_at >= ?", target.ProfileID, time.Now().Add(-chunkReanalysisWindow)).
		Count(&recentCount).Error; err != nil {
		logger.Log.Error("failed to check re-analysis quota", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create re-analysis"})
		return
	}
	if recentCount >= chunkReanalysisDailyQuota {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "daily re-analysis limit reached"})
		return
	}

	originalSnapshot, err := json.Marshal(map[string]any{
		"chunk_id":           target.ID,
		"status":             target.Status,
		"exercise_type":      target.ExerciseType,
		"output":             target.Output,
		"observed_signals":   parseJSONObject(target.ObservedSignals),
		"heart_rate_bpm":     target.HeartRateBPM,
		"workout_confidence": target.WorkoutConfidence,
		"motion_score":       target.MotionScore,
		"skip_reason":        target.SkipReason,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create re-analysis"})
		return
	}

	run := db.ChunkReanalysisRun{
		SessionID:                  sessionID,
		ProfileID:                  target.ProfileID,
		ChunkAnalysisResultID:      target.ID,
		ClientRequestID:            req.ClientRequestID,
		Status:                     db.ChunkReanalysisStatusQueued,
		SourceContextSnapshot:      db.JSONDocument(`{}`),
		OriginalPredictionSnapshot: db.JSONDocument(originalSnapshot),
		StructuredCandidate:        db.JSONDocument(`{}`),
	}
	if err := ctl.db.WithContext(c.Request.Context()).Create(&run).Error; err != nil {
		if isChunkReanalysisUniqueConflict(err) {
			var concurrent db.ChunkReanalysisRun
			if lookupErr := ctl.db.WithContext(c.Request.Context()).
				Where("profile_id = ? AND client_request_id = ?", target.ProfileID, req.ClientRequestID).
				First(&concurrent).Error; lookupErr == nil &&
				concurrent.SessionID == sessionID && concurrent.ChunkAnalysisResultID == chunkID {
				c.JSON(http.StatusAccepted, CreateChunkReanalysisResponse{
					RunID: concurrent.ID, TaskID: concurrent.TaskID, Status: concurrent.Status,
				})
				return
			}
			c.JSON(http.StatusConflict, gin.H{"error": "a re-analysis is already active or the request was already submitted"})
			return
		}
		logger.Log.Error("failed to create re-analysis run", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create re-analysis"})
		return
	}

	task, err := worker.NewChunkDebugReanalysisTask(run.ID)
	if err != nil {
		ctl.markReanalysisEnqueueFailed(c.Request.Context(), run.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create re-analysis task"})
		return
	}
	info, err := ctl.queueClient.Enqueue(task,
		asynq.MaxRetry(chunkReanalysisTaskMaxTry),
		asynq.Timeout(chunkReanalysisTaskTimeout))
	if err != nil {
		ctl.markReanalysisEnqueueFailed(c.Request.Context(), run.ID)
		logger.Log.Error("failed to enqueue chunk re-analysis", zap.Uint("run_id", run.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue re-analysis"})
		return
	}

	run.TaskID = info.ID
	if err := ctl.db.WithContext(c.Request.Context()).Model(&db.ChunkReanalysisRun{}).
		Where("id = ?", run.ID).Update("task_id", info.ID).Error; err != nil {
		logger.Log.Warn("failed to persist chunk re-analysis task ID", zap.Uint("run_id", run.ID), zap.Error(err))
	}

	c.JSON(http.StatusAccepted, CreateChunkReanalysisResponse{
		RunID: run.ID, TaskID: info.ID, Status: db.ChunkReanalysisStatusQueued,
	})
}

func (ctl *Controller) ListChunkReanalyses(c *gin.Context) {
	if !ctl.requireChunkReanalysisEnabled(c) {
		return
	}
	sessionID, chunkID, ok := parseChunkReanalysisPath(c)
	if !ok {
		return
	}
	target, ok := ctl.loadOwnedChunkReanalysisTarget(c, sessionID, chunkID)
	if !ok {
		return
	}

	var runs []db.ChunkReanalysisRun
	if err := ctl.db.WithContext(c.Request.Context()).
		Where("session_id = ? AND profile_id = ? AND chunk_analysis_result_id = ?", sessionID, target.ProfileID, chunkID).
		Order("created_at DESC").Limit(chunkReanalysisRunLimit).Find(&runs).Error; err != nil {
		logger.Log.Error("failed to list chunk re-analyses", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list re-analyses"})
		return
	}

	response := make([]ChunkReanalysisRunResponse, 0, len(runs))
	for i := range runs {
		response = append(response, chunkReanalysisRunResponse(runs[i]))
	}
	c.JSON(http.StatusOK, ListChunkReanalysesResponse{Runs: response})
}

func (ctl *Controller) GetChunkReanalysis(c *gin.Context) {
	if !ctl.requireChunkReanalysisEnabled(c) {
		return
	}
	sessionID, chunkID, ok := parseChunkReanalysisPath(c)
	if !ok {
		return
	}
	target, ok := ctl.loadOwnedChunkReanalysisTarget(c, sessionID, chunkID)
	if !ok {
		return
	}
	runID, err := strconv.ParseUint(c.Param("run_id"), 10, 64)
	if err != nil || runID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run_id"})
		return
	}

	var run db.ChunkReanalysisRun
	if err := ctl.db.WithContext(c.Request.Context()).Where(
		"id = ? AND session_id = ? AND profile_id = ? AND chunk_analysis_result_id = ?",
		uint(runID), sessionID, target.ProfileID, chunkID,
	).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "re-analysis not found"})
			return
		}
		logger.Log.Error("failed to load chunk re-analysis", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load re-analysis"})
		return
	}
	c.JSON(http.StatusOK, chunkReanalysisRunResponse(run))
}

func (ctl *Controller) requireChunkReanalysisEnabled(c *gin.Context) bool {
	if ctl.enableChunkReanalysis {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chunk re-analysis is disabled"})
	return false
}

func parseChunkReanalysisPath(c *gin.Context) (string, uint, bool) {
	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id"})
		return "", 0, false
	}
	parsed, err := strconv.ParseUint(c.Param("chunk_id"), 10, 64)
	if err != nil || parsed == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chunk_id"})
		return "", 0, false
	}
	return sessionID, uint(parsed), true
}

func (ctl *Controller) loadOwnedChunkReanalysisTarget(c *gin.Context, sessionID string, chunkID uint) (*chunkReanalysisTarget, bool) {
	if ctl.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database is not configured"})
		return nil, false
	}

	query := ctl.db.WithContext(c.Request.Context()).Table("chunk_analysis_results AS chunks").
		Select(`chunks.id, chunks.session_id, chunks.profile_id, chunks.file_path,
			chunks.exercise_type, chunks.status, chunks.output, chunks.observed_signals,
			chunks.heart_rate_bpm, chunks.start_secs, chunks.end_secs,
			chunks.media_start_secs, chunks.media_end_secs, chunks.workout_confidence,
			chunks.motion_score, chunks.skip_reason`).
		Joins("JOIN profiles ON profiles.id = chunks.profile_id").
		Where("chunks.id = ? AND chunks.session_id = ?", chunkID, sessionID)
	if userID := UserIDFromContext(c); userID != 0 {
		query = query.Where("profiles.user_id = ?", userID)
	}

	var target chunkReanalysisTarget
	if err := query.Take(&target).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "chunk not found"})
			return nil, false
		}
		logger.Log.Error("failed to load owned chunk", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load chunk"})
		return nil, false
	}

	// A Session row is optional for legacy recordings. If it exists, it must
	// agree with the production chunk's profile.
	var sessionProfileID uint
	err := ctl.db.WithContext(c.Request.Context()).Model(&db.Session{}).
		Where("session_id = ?", sessionID).Pluck("profile_id", &sessionProfileID).Error
	if err != nil {
		logger.Log.Error("failed to verify session profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load chunk"})
		return nil, false
	}
	if sessionProfileID != 0 && sessionProfileID != target.ProfileID {
		c.JSON(http.StatusNotFound, gin.H{"error": "chunk not found"})
		return nil, false
	}

	return &target, true
}

var (
	errChunkVideoUnavailable    = errors.New("chunk video unavailable")
	errChunkIntervalUnavailable = errors.New("chunk interval unavailable")
)

func (ctl *Controller) resolveChunkPlaybackSource(ctx context.Context, target *chunkReanalysisTarget) (string, string, *float64, *float64, error) {
	if validMediaInterval(target.MediaStartSecs, target.MediaEndSecs) {
		objects, err := ctl.listSessionVideoObjects(ctx, target.ProfileID, target.SessionID)
		if err == nil {
			if objectName := selectSessionVideoObject(objects, true); objectName != "" {
				return objectName, db.ChunkReanalysisSourceSessionVideo,
					cloneFloat64(target.MediaStartSecs), cloneFloat64(target.MediaEndSecs), nil
			}
		}
		if target.FilePath == "" {
			if err != nil {
				return "", "", nil, nil, err
			}
			return "", "", nil, nil, errChunkVideoUnavailable
		}
	}

	if target.FilePath == "" {
		return "", "", nil, nil, errChunkIntervalUnavailable
	}
	bucket, objectName, err := storage.ParseGCSURI(target.FilePath)
	if err != nil || objectName == "" || (ctl.bucketName != "" && bucket != ctl.bucketName) {
		return "", "", nil, nil, errChunkVideoUnavailable
	}
	start := 0.0
	// The complete retained chunk object is authoritative. Do not convert its
	// capture-clock timestamps into a media duration; browser playback should
	// play the whole object when its probed duration is unavailable here.
	return objectName, db.ChunkReanalysisSourceChunk, &start, nil, nil
}

func (ctl *Controller) listSessionVideoObjects(ctx context.Context, profileID uint, sessionID string) ([]string, error) {
	newPrefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
	objects, err := ctl.storageClient.ListObjects(ctx, newPrefix)
	if err != nil {
		return nil, err
	}
	if selectSessionVideoObject(objects, true) != "" {
		return objects, nil
	}
	return ctl.storageClient.ListObjects(ctx, "videos/"+sessionID+"_")
}

func selectSessionVideoObject(objects []string, preferAnalysis bool) string {
	best := ""
	bestScore := 0
	for _, object := range objects {
		base := strings.ToLower(filepath.Base(object))
		if !strings.HasSuffix(base, ".mp4") || strings.HasPrefix(base, "chunk_") ||
			strings.HasPrefix(base, "split_chunk_") || strings.HasPrefix(base, "hl_") {
			continue
		}
		score := 50
		switch {
		case base == "analysis.mp4":
			if preferAnalysis {
				score = 110
			} else {
				score = 80
			}
		case base == "merged.mp4":
			if preferAnalysis {
				score = 100
			} else {
				score = 110
			}
		case strings.Contains(base, "_merged_"):
			score = 95
		case base == "hardsubbed.mp4" || strings.Contains(base, "_hardsubbed_"):
			score = 40
		case strings.Contains(base, "_encoded"):
			score = 70
		}
		if score > bestScore {
			best = object
			bestScore = score
		}
	}
	return best
}

func validMediaInterval(start, end *float64) bool {
	return start != nil && end != nil && *start >= 0 && *end > *start
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func parseJSONObject(raw string) map[string]any {
	result := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &result)
	}
	return result
}

func chunkReanalysisRunResponse(run db.ChunkReanalysisRun) ChunkReanalysisRunResponse {
	response := ChunkReanalysisRunResponse{
		ID:             run.ID,
		SessionID:      run.SessionID,
		ChunkID:        run.ChunkAnalysisResultID,
		TaskID:         run.TaskID,
		Status:         run.Status,
		SourceKind:     run.SourceKind,
		MediaStartSecs: run.MediaStartSecs,
		MediaEndSecs:   run.MediaEndSecs,
		Model:          run.Model,
		PromptVersion:  run.PromptVersion,
		PromptHash:     run.PromptHash,
		SchemaVersion:  run.SchemaVersion,
		RawOutput:      run.RawOutput,
		DurationMs:     run.DurationMs,
		Error:          run.SafeError,
		CreatedAt:      run.CreatedAt,
		StartedAt:      run.StartedAt,
		CompletedAt:    run.CompletedAt,
	}
	if run.Status == db.ChunkReanalysisStatusCompleted {
		candidate := ChunkReanalysisCandidateResponse{ObservedSignals: map[string]any{}}
		if json.Unmarshal([]byte(run.StructuredCandidate), &candidate) == nil {
			response.Candidate = &candidate
		}
		response.TokenUsage = &ChunkReanalysisTokenUsageResponse{
			PromptTokens:    run.PromptTokens,
			CandidateTokens: run.CandidateTokens,
			TotalTokens:     run.TotalTokens,
		}
	}
	return response
}

func (ctl *Controller) markReanalysisEnqueueFailed(ctx context.Context, runID uint) {
	now := time.Now().UTC()
	_ = ctl.db.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).Where("id = ?", runID).Updates(map[string]any{
		"status":       db.ChunkReanalysisStatusFailed,
		"safe_error":   "The re-analysis task could not be queued.",
		"completed_at": now,
	}).Error
}

func isChunkReanalysisUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "23505") || strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "idx_chunk_reanalysis_runs_one_active_per_chunk")
}

func decodeCreateChunkReanalysisRequest(reader io.Reader) (CreateChunkReanalysisRequest, error) {
	var request CreateChunkReanalysisRequest
	decoder := json.NewDecoder(io.LimitReader(reader, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return CreateChunkReanalysisRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return CreateChunkReanalysisRequest{}, errors.New("request body must contain one JSON object")
	}
	return request, nil
}
