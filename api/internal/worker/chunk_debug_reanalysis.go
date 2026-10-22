package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/storage"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	TypeChunkDebugReanalysis = "chunk:debug-reanalysis"

	chunkDebugPromptVersion = "chunk-debug-observations-v2"
	chunkDebugSchemaVersion = "chunk-reanalysis-candidate-v1"
	chunkDebugMaxRetries    = 2
	geminiFileCacheTTL      = 47 * time.Hour
)

type ChunkDebugReanalysisPayload struct {
	RunID uint `json:"run_id"`
}

type chunkDebugTarget struct {
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
	WODDescription    string
	WorkoutType       string
	MovementHints     db.JSONDocument
	ProfileInjuries   *string
}

type chunkDebugCandidate struct {
	ExerciseType    string         `json:"exercise_type"`
	Output          string         `json:"output"`
	ObservedSignals map[string]any `json:"observed_signals"`
}

type chunkDebugMedia struct {
	GCSURI string
	Kind   string
	Start  time.Duration
	End    time.Duration
}

type reusableGeminiFile struct {
	URI       string
	Name      string
	MIMEType  string
	Expires   *time.Time
	Duration  time.Duration
	LocalPath string
	// releaseLocal returns LocalPath's share of the retained-media budget.
	releaseLocal func()
}

// removeLocal deletes the retained local copy and returns its budget.
func (f reusableGeminiFile) removeLocal() {
	if f.LocalPath != "" {
		_ = os.Remove(f.LocalPath)
	}
	if f.releaseLocal != nil {
		f.releaseLocal()
	}
}

// Reanalysis keeps a local copy in the worker's memory-backed temp dir for the
// whole task so frame grids can read it. Copies that do not fit this budget,
// shared by concurrent runs, are deleted after upload or probe; those runs use
// VideoMetadata analysis.
const maxRetainedReanalysisMediaBytes int64 = 1 << 30

var retainedReanalysisMediaBytes atomic.Int64

// retainReanalysisMedia reserves size bytes of the budget and returns the
// release func, or nil when the copy does not fit.
func retainReanalysisMedia(size int64) func() {
	if size <= 0 {
		return nil
	}
	for {
		current := retainedReanalysisMediaBytes.Load()
		if current+size > maxRetainedReanalysisMediaBytes {
			return nil
		}
		if retainedReanalysisMediaBytes.CompareAndSwap(current, current+size) {
			var once sync.Once
			return func() { once.Do(func() { retainedReanalysisMediaBytes.Add(-size) }) }
		}
	}
}

// keepLocalForFrameGrids keeps file.LocalPath only while it fits the
// retained-media budget.
func keepLocalForFrameGrids(file reusableGeminiFile) reusableGeminiFile {
	if file.LocalPath == "" {
		return file
	}
	if info, err := os.Stat(file.LocalPath); err == nil {
		if release := retainReanalysisMedia(info.Size()); release != nil {
			file.releaseLocal = release
			return file
		}
	}
	_ = os.Remove(file.LocalPath)
	file.LocalPath = ""
	return file
}

func NewChunkDebugReanalysisTask(runID uint) (*asynq.Task, error) {
	if runID == 0 {
		return nil, fmt.Errorf("run_id is required")
	}
	payload, err := json.Marshal(ChunkDebugReanalysisPayload{RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("marshal chunk debug re-analysis payload: %w", err)
	}
	return asynq.NewTask(TypeChunkDebugReanalysis, payload), nil
}

// HandleChunkDebugReanalysisTask analyzes one exact media interval and stores a
// debug candidate without touching production chunk or final-analysis rows.
func (w *Worker) HandleChunkDebugReanalysisTask(ctx context.Context, task *asynq.Task) error {
	started := time.Now()
	var payload ChunkDebugReanalysisPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil || payload.RunID == 0 {
		return fmt.Errorf("invalid chunk debug re-analysis payload: %w", asynq.SkipRetry)
	}
	if w.DB == nil {
		return fmt.Errorf("chunk debug re-analysis database is not configured: %w", asynq.SkipRetry)
	}

	retryCount, _ := asynq.GetRetryCount(ctx)
	var run db.ChunkReanalysisRun
	if err := w.DB.WithContext(ctx).First(&run, payload.RunID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("chunk debug re-analysis run not found: %w", asynq.SkipRetry)
		}
		return fmt.Errorf("load chunk debug re-analysis run: %w", err)
	}
	if isChunkReanalysisTerminal(run.Status) {
		return nil
	}
	if w.StorageClient == nil || w.GeminiClient == nil {
		if err := w.completeChunkDebugRun(ctx, run.ID, db.ChunkReanalysisStatusFailed, started, map[string]any{
			"safe_error": "The re-analysis worker is not configured.",
		}); err != nil {
			return err
		}
		return fmt.Errorf("chunk debug re-analysis dependencies are not configured: %w", asynq.SkipRetry)
	}

	now := time.Now().UTC()
	claim := w.DB.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).
		Where("id = ? AND status = ?", run.ID, db.ChunkReanalysisStatusQueued).
		Updates(map[string]any{
			"status":     db.ChunkReanalysisStatusRunning,
			"started_at": now,
			"safe_error": "",
		})
	if claim.Error != nil {
		return fmt.Errorf("claim chunk debug re-analysis run: %w", claim.Error)
	}
	if claim.RowsAffected == 0 {
		return nil
	}

	target, err := w.loadChunkDebugTarget(ctx, &run)
	if err != nil {
		return w.failChunkDebugRun(ctx, &run, retryCount, started,
			db.ChunkReanalysisStatusFailed, "The source chunk could not be loaded.", err)
	}

	media, terminalStatus, err := w.resolveChunkDebugMedia(ctx, target)
	if err != nil {
		if terminalStatus != "" {
			if persistErr := w.completeChunkDebugRun(ctx, run.ID, terminalStatus, started, map[string]any{
				"safe_error": safeChunkDebugError(terminalStatus),
			}); persistErr != nil {
				return persistErr
			}
			return nil
		}
		return w.failChunkDebugRun(ctx, &run, retryCount, started,
			db.ChunkReanalysisStatusVideoUnavailable, "The session video could not be loaded.", err)
	}

	if err := w.DB.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"source_kind":      media.Kind,
		"source_gcs_uri":   media.GCSURI,
		"media_start_secs": media.Start.Seconds(),
		"media_end_secs":   nullablePositiveDurationSeconds(media.End),
	}).Error; err != nil {
		return w.failChunkDebugRun(ctx, &run, retryCount, started,
			db.ChunkReanalysisStatusFailed, "The re-analysis state could not be saved.", err)
	}

	reusePrevious := shouldReuseChunkDebugCandidate(run.SourceContextSnapshot)
	prompt := w.buildChunkDebugReanalysisPrompt(target)
	// The analysis mode is part of the reuse identity: frame-grid and video
	// candidates of the same prompt are different observations.
	// Before the source is prepared either mode is possible; reuse accepts a
	// candidate of either mode and keeps that candidate's own prompt_hash.
	reusableHashes := []string{chunkDebugPromptHash(prompt, debugAnalysisModeFrameGrid), chunkDebugPromptHash(prompt, debugAnalysisModeVideo)}
	defaultClientModel := ""
	if provider, ok := w.GeminiClient.(interface{ Model() string }); ok {
		defaultClientModel = provider.Model()
	}
	selectedModel := resolveReanalysisModelWithDefault(run.Model, defaultClientModel)

	buildSnapshot := func(m chunkDebugMedia) []byte {
		payload := map[string]any{
			"session_id":       target.SessionID,
			"profile_id":       target.ProfileID,
			"profile_context":  w.lookupProfileString(target.ProfileID),
			"appearance":       w.buildTargetPersonContext(target.ProfileID, target.SessionID),
			"profile_injuries": target.ProfileInjuries,
			"wod_description":  target.WODDescription,
			"workout_type":     target.WorkoutType,
			"movement_hints":   movementHintsFromDocument(target.MovementHints),
			"heart_rate_bpm":   target.HeartRateBPM,
			"source_kind":      m.Kind,
			"source_gcs_uri":   m.GCSURI,
			"media_start_secs": m.Start.Seconds(),
			"media_end_secs":   m.End.Seconds(),
			"prompt_version":   chunkDebugPromptVersion,
			"schema_version":   chunkDebugSchemaVersion,
		}
		if reusePrevious {
			payload["reuse_previous"] = true
		}
		raw, _ := json.Marshal(payload)
		return raw
	}

	if reusePrevious && media.End > media.Start {
		if cached, ok := w.findReusableChunkDebugCandidate(ctx, &run, selectedModel, reusableHashes, media); ok {
			contextSnapshot := buildSnapshot(media)
			if err := w.DB.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).Where("id = ?", run.ID).Updates(map[string]any{
				"source_context_snapshot": string(contextSnapshot),
				"media_start_secs":        media.Start.Seconds(),
				"media_end_secs":          media.End.Seconds(),
				"prompt_version":          chunkDebugPromptVersion,
				"prompt_hash":             cached.PromptHash,
				"schema_version":          chunkDebugSchemaVersion,
			}).Error; err != nil {
				return w.failChunkDebugRun(ctx, &run, retryCount, started,
					db.ChunkReanalysisStatusFailed, "The re-analysis state could not be saved.", err)
			}
			w.recordStageMetrics(target.SessionID, target.ProfileID, "chunk_reanalysis", "current", 0, 1, 0, time.Since(started))
			return w.completeChunkDebugRun(ctx, run.ID, db.ChunkReanalysisStatusCompleted, started, map[string]any{
				"raw_output":           cached.RawOutput,
				"structured_candidate": string(cached.StructuredCandidate),
				"model":                cached.Model,
				"prompt_tokens":        0,
				"candidate_tokens":     0,
				"total_tokens":         0,
				"safe_error":           "",
			})
		}
	}

	file, uploadBytes, err := w.prepareChunkDebugGeminiFile(ctx, &run, media)
	if err != nil {
		return w.failChunkDebugRun(ctx, &run, retryCount, started,
			db.ChunkReanalysisStatusVideoUnavailable, "The session video could not be prepared.", err)
	}
	defer file.removeLocal()

	if media.Kind == db.ChunkReanalysisSourceChunk {
		// An individual chunk is itself the exact interval. Its Gemini-probed
		// duration is authoritative even when legacy capture offsets are not.
		media.Start = 0
		if fileDuration := fileVideoDuration(file); fileDuration > 0 {
			media.End = fileDuration
		}
	}
	if media.End <= media.Start {
		if err := w.completeChunkDebugRun(ctx, run.ID, db.ChunkReanalysisStatusIntervalUnavailable, started, map[string]any{
			"safe_error": safeChunkDebugError(db.ChunkReanalysisStatusIntervalUnavailable),
		}); err != nil {
			return err
		}
		return nil
	}
	if duration := fileVideoDuration(file); duration > 0 && media.End > duration+250*time.Millisecond {
		if err := w.completeChunkDebugRun(ctx, run.ID, db.ChunkReanalysisStatusIntervalUnavailable, started, map[string]any{
			"safe_error": safeChunkDebugError(db.ChunkReanalysisStatusIntervalUnavailable),
		}); err != nil {
			return err
		}
		return nil
	}

	contextSnapshot := buildSnapshot(media)
	plannedMode := chunkDebugAnalysisMode(file.LocalPath != "", media.End-media.Start)
	promptHashHex := chunkDebugPromptHash(prompt, plannedMode)

	if err := w.DB.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"source_context_snapshot": string(contextSnapshot),
		"media_start_secs":        media.Start.Seconds(),
		"media_end_secs":          media.End.Seconds(),
		"prompt_version":          chunkDebugPromptVersion,
		"prompt_hash":             promptHashHex,
		"schema_version":          chunkDebugSchemaVersion,
	}).Error; err != nil {
		return w.failChunkDebugRun(ctx, &run, retryCount, started,
			db.ChunkReanalysisStatusFailed, "The re-analysis state could not be saved.", err)
	}

	if reusePrevious {
		if cached, ok := w.findReusableChunkDebugCandidate(ctx, &run, selectedModel, reusableHashes, media); ok {
			w.recordStageMetrics(target.SessionID, target.ProfileID, "chunk_reanalysis", "current", 0, 1, uploadBytes, time.Since(started))
			return w.completeChunkDebugRun(ctx, run.ID, db.ChunkReanalysisStatusCompleted, started, map[string]any{
				"raw_output":           cached.RawOutput,
				"structured_candidate": string(cached.StructuredCandidate),
				"prompt_hash":          cached.PromptHash,
				"model":                cached.Model,
				"prompt_tokens":        0,
				"candidate_tokens":     0,
				"total_tokens":         0,
				"safe_error":           "",
			})
		}
	}

	var (
		analysis string
		sheets   []FrameGridSheet
		usage    = (*gemini.TokenUsage)(nil)
	)
	usedMode := plannedMode
	if plannedMode == debugAnalysisModeFrameGrid {
		gridOpts := FrameGridOptions{}
		if media.Kind == db.ChunkReanalysisSourceChunk && target.MediaStartSecs != nil && *target.MediaStartSecs >= 0 {
			offset := secondsDuration(*target.MediaStartSecs)
			gridOpts.TimestampOffset = &offset
		}
		analysis, sheets, usage, err = w.AnalyzeVideoIntervalWithFrameGrids(
			ctx, file.LocalPath, media.Start, media.End, prompt, selectedModel, gridOpts,
		)
	}
	if plannedMode == debugAnalysisModeVideo || (err != nil && len(sheets) == 0) {
		if err != nil {
			w.logger.Warn("frame grid extraction failed for chunk debug run; falling back to video segment analysis",
				zap.Uint("run_id", run.ID), zap.Error(err))
		}
		usedMode = debugAnalysisModeVideo
		analysis, usage, err = w.GeminiClient.AnalyzeSegmentWithModel(
			ctx, file.URI, chunkDebugMIMEType(file.MIMEType), media.Start, media.End, prompt, selectedModel,
		)
	}
	w.saveTokenUsage(target.SessionID, target.ProfileID, "chunk:reanalysis", usage)
	if err != nil || strings.TrimSpace(analysis) == "" {
		if err == nil {
			err = errors.New("Gemini returned an empty chunk re-analysis")
		}
		return w.failChunkDebugRun(ctx, &run, retryCount, started,
			db.ChunkReanalysisStatusFailed, "The analyzer could not produce a candidate.", err)
	}

	candidate := parseChunkDebugCandidate(analysis)
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		return w.failChunkDebugRun(ctx, &run, retryCount, started,
			db.ChunkReanalysisStatusFailed, "The analyzer candidate could not be saved.", err)
	}

	model := selectedModel
	promptTokens := int32(0)
	candidateTokens := int32(0)
	totalTokens := int32(0)
	if usage != nil {
		if usage.Model != "" {
			model = usage.Model
		}
		promptTokens = usage.PromptTokens
		candidateTokens = usage.CandidateTokens
		totalTokens = usage.TotalTokens
	}

	w.recordStageMetrics(target.SessionID, target.ProfileID, "chunk_reanalysis", "current", 1, 0, uploadBytes, time.Since(started))

	if err := w.completeChunkDebugRun(ctx, run.ID, db.ChunkReanalysisStatusCompleted, started, map[string]any{
		"raw_output":           analysis,
		"structured_candidate": string(candidateJSON),
		"prompt_hash":          chunkDebugPromptHash(prompt, usedMode),
		"model":                model,
		"prompt_tokens":        promptTokens,
		"candidate_tokens":     candidateTokens,
		"total_tokens":         totalTokens,
		"safe_error":           "",
	}); err != nil {
		return err
	}

	w.logger.Info("Chunk debug re-analysis completed",
		zap.Uint("run_id", run.ID),
		zap.String("session_id", target.SessionID),
		zap.Uint("chunk_id", target.ID),
		zap.String("source_kind", media.Kind),
		zap.Duration("media_start", media.Start),
		zap.Duration("media_end", media.End))
	return nil
}

func (w *Worker) loadChunkDebugTarget(ctx context.Context, run *db.ChunkReanalysisRun) (*chunkDebugTarget, error) {
	var target chunkDebugTarget
	err := w.DB.WithContext(ctx).Table("chunk_analysis_results AS chunks").
		Select(`chunks.id, chunks.session_id, chunks.profile_id, chunks.file_path,
			chunks.exercise_type, chunks.status, chunks.output, chunks.observed_signals,
			chunks.heart_rate_bpm, chunks.start_secs, chunks.end_secs,
			chunks.media_start_secs, chunks.media_end_secs, chunks.workout_confidence,
			chunks.motion_score, chunks.skip_reason,
			CASE
				WHEN sessions.wod_description ~ '[^[:space:]]' THEN sessions.wod_description
				WHEN analysis_results.wod_description ~ '[^[:space:]]' THEN analysis_results.wod_description
				ELSE ''
			END AS wod_description,
			COALESCE(sessions.workout_type, '') AS workout_type,
			COALESCE(sessions.movement_hints, '[]'::jsonb) AS movement_hints,
			profiles.injuries AS profile_injuries`).
		Joins("JOIN profiles ON profiles.id = chunks.profile_id").
		Joins("LEFT JOIN sessions ON sessions.session_id = chunks.session_id AND sessions.profile_id = chunks.profile_id").
		Joins("LEFT JOIN analysis_results ON analysis_results.session_id = chunks.session_id AND analysis_results.profile_id = chunks.profile_id").
		Where("chunks.id = ? AND chunks.session_id = ? AND chunks.profile_id = ?",
			run.ChunkAnalysisResultID, run.SessionID, run.ProfileID).
		Take(&target).Error
	if err != nil {
		return nil, fmt.Errorf("load exact source chunk: %w", err)
	}
	return &target, nil
}

func (w *Worker) resolveChunkDebugMedia(ctx context.Context, target *chunkDebugTarget) (chunkDebugMedia, string, error) {
	if !debugMediaIntervalValid(target.MediaStartSecs, target.MediaEndSecs) {
		start, end, err := w.reconstructChunkDebugMediaInterval(ctx, target)
		if err == nil {
			target.MediaStartSecs = &start
			target.MediaEndSecs = &end
		} else {
			w.logger.Warn("could not reconstruct legacy merged-media interval; using exact chunk fallback",
				zap.String("session_id", target.SessionID),
				zap.Uint("chunk_id", target.ID),
				zap.Error(err))
		}
	}

	if debugMediaIntervalValid(target.MediaStartSecs, target.MediaEndSecs) {
		objects, err := w.listChunkDebugSessionObjects(ctx, target.ProfileID, target.SessionID)
		if err != nil {
			if strings.TrimSpace(target.FilePath) == "" {
				return chunkDebugMedia{}, "", err
			}
			w.logger.Warn("could not list session video; using exact retained chunk fallback",
				zap.String("session_id", target.SessionID),
				zap.Uint("chunk_id", target.ID),
				zap.Error(err))
			objects = nil
		}
		objectName := selectChunkDebugSessionVideo(objects)
		if objectName != "" {
			return chunkDebugMedia{
				GCSURI: fmt.Sprintf("gs://%s/%s", w.BucketName, objectName),
				Kind:   db.ChunkReanalysisSourceSessionVideo,
				Start:  secondsDuration(*target.MediaStartSecs),
				End:    secondsDuration(*target.MediaEndSecs),
			}, "", nil
		}
		// A retained individual chunk is still an exact source even if the
		// session-level object was removed by lifecycle policy.
		if strings.TrimSpace(target.FilePath) == "" {
			return chunkDebugMedia{}, db.ChunkReanalysisStatusVideoUnavailable, errors.New("no session video object")
		}
	}

	if strings.TrimSpace(target.FilePath) == "" {
		return chunkDebugMedia{}, db.ChunkReanalysisStatusIntervalUnavailable, errors.New("no verified media interval or chunk object")
	}
	bucket, _, err := storage.ParseGCSURI(target.FilePath)
	if err != nil || (w.BucketName != "" && bucket != w.BucketName) {
		return chunkDebugMedia{}, db.ChunkReanalysisStatusVideoUnavailable, errors.New("invalid source chunk URI")
	}
	return chunkDebugMedia{
		GCSURI: target.FilePath,
		Kind:   db.ChunkReanalysisSourceChunk,
		Start:  0,
		// UploadVideo supplies the retained object's actual processed duration.
		// Capture-clock deltas are deliberately not used as media duration.
		End: 0,
	}, "", nil
}

// reconstructChunkDebugMediaInterval rebuilds the concat timeline up to the
// target chunk. It never maps capture-clock offsets directly onto merged.mp4.
func (w *Worker) reconstructChunkDebugMediaInterval(ctx context.Context, target *chunkDebugTarget) (float64, float64, error) {
	if strings.TrimSpace(target.FilePath) == "" {
		return 0, 0, errors.New("target chunk has no retained object")
	}
	intervals, err := w.retainedChunkTimeline(ctx, target.SessionID, target.ProfileID, target.FilePath)
	if err != nil {
		return 0, 0, err
	}
	// Cache every verified prefix interval so later debug runs avoid repeated
	// downloads. A partial prefix remains valid even if a later chunk is absent.
	if err := w.persistConcatMediaIntervals(ctx, target.SessionID, target.ProfileID, intervals); err != nil {
		return 0, 0, fmt.Errorf("save reconstructed intervals: %w", err)
	}
	targetInterval := intervals[len(intervals)-1]
	return targetInterval.Start, targetInterval.End, nil
}

// retainedChunkTimeline rebuilds the cumulative concat timeline from retained
// original chunks. Each media duration is probed from the actual GCS object, and
// each URI is counted once because merge includes duplicate rows' URI once.
// With stopAt set, the walk ends at that object. Any unavailable object fails
// the reconstruction; a partial timeline is never returned.
func (w *Worker) retainedChunkTimeline(ctx context.Context, sessionID string, profileID uint, stopAt string) ([]concatMediaInterval, error) {
	var chunks []struct {
		ID       uint
		FilePath string
	}
	if err := w.DB.WithContext(ctx).Table("chunk_analysis_results").
		Select("id, file_path").
		Where("session_id = ? AND profile_id = ? AND status IN ? AND file_path <> '' AND file_path NOT LIKE ?",
			sessionID, profileID, []string{"COMPLETED", "FAILED"}, "%/split_chunk_%").
		// Mirror the order used to build the legacy concat manifest. Capture
		// timestamps determine ordering only; their values are never used as
		// offsets in the merged media.
		Order("start_secs ASC NULLS LAST, file_path ASC, id ASC").Find(&chunks).Error; err != nil {
		return nil, fmt.Errorf("load retained chunks for timeline reconstruction: %w", err)
	}
	if len(chunks) == 0 {
		return nil, errors.New("no retained chunks for timeline reconstruction")
	}

	intervals := make([]concatMediaInterval, 0, len(chunks))
	seen := make(map[string]bool, len(chunks))
	cumulative := 0.0
	for _, chunk := range chunks {
		if seen[chunk.FilePath] {
			continue
		}
		seen[chunk.FilePath] = true
		bucket, _, err := storage.ParseGCSURI(chunk.FilePath)
		if err != nil || (w.BucketName != "" && bucket != w.BucketName) {
			return nil, fmt.Errorf("invalid retained chunk URI for chunk %d", chunk.ID)
		}
		localPath, err := createTempFile("chunk-timeline", ".mp4")
		if err != nil {
			return nil, err
		}
		downloadErr := w.StorageClient.DownloadFile(ctx, chunk.FilePath, localPath)
		if downloadErr != nil {
			_ = os.Remove(localPath)
			return nil, fmt.Errorf("download retained chunk %d: %w", chunk.ID, downloadErr)
		}
		duration := probeVideoDuration(ctx, localPath)
		_ = os.Remove(localPath)
		if duration <= 0 {
			return nil, fmt.Errorf("probe retained chunk %d duration", chunk.ID)
		}
		intervals = append(intervals, concatMediaInterval{FilePath: chunk.FilePath, Start: cumulative, End: cumulative + duration})
		cumulative += duration
		if stopAt != "" && chunk.FilePath == stopAt {
			return intervals, nil
		}
	}
	if stopAt != "" {
		return nil, errors.New("target chunk is not in the retained concat order")
	}
	return intervals, nil
}

func (w *Worker) listChunkDebugSessionObjects(ctx context.Context, profileID uint, sessionID string) ([]string, error) {
	objects, err := w.StorageClient.ListObjects(ctx, fmt.Sprintf("videos/%d/%s/", profileID, sessionID))
	if err != nil {
		return nil, err
	}
	if selectChunkDebugSessionVideo(objects) != "" {
		return objects, nil
	}
	return w.StorageClient.ListObjects(ctx, "videos/"+sessionID+"_")
}

func selectChunkDebugSessionVideo(objects []string) string {
	best := ""
	bestScore := 0
	for _, object := range objects {
		base := strings.ToLower(filepath.Base(object))
		score := 0
		switch {
		case base == "analysis.mp4":
			score = 110
		case base == "merged.mp4":
			score = 100
		case strings.Contains(base, "_merged_") && strings.HasSuffix(base, ".mp4"):
			score = 95
		case strings.Contains(base, "_encoded") && strings.HasSuffix(base, ".mp4"):
			score = 80
		}
		if score > bestScore {
			best = object
			bestScore = score
		}
	}
	return best
}

func (w *Worker) prepareChunkDebugGeminiFile(ctx context.Context, run *db.ChunkReanalysisRun, media chunkDebugMedia) (reusableGeminiFile, int64, error) {
	if media.Kind == db.ChunkReanalysisSourceSessionVideo {
		if cached := w.findReusableChunkDebugGeminiFile(ctx, run, media.GCSURI); cached.URI != "" {
			// A reused Files object avoids a second download of the whole session
			// video, so this run uses VideoMetadata analysis (no local media).
			return cached, 0, nil
		}
	}

	localPath, err := createTempFile("chunk-debug-reanalysis", ".mp4")
	if err != nil {
		return reusableGeminiFile{}, 0, err
	}
	if err := w.StorageClient.DownloadFile(ctx, media.GCSURI, localPath); err != nil {
		_ = os.Remove(localPath)
		return reusableGeminiFile{}, 0, fmt.Errorf("download debug source: %w", err)
	}
	var uploadBytes int64
	if info, err := os.Stat(localPath); err == nil {
		uploadBytes = info.Size()
	}

	upload, err := w.GeminiClient.UploadVideo(ctx, localPath)
	if err != nil {
		_ = os.Remove(localPath)
		if upload != nil && upload.FileName != "" {
			if deleteErr := w.GeminiClient.DeleteFile(context.Background(), upload.FileName); deleteErr != nil {
				w.logger.Warn("failed to clean up incomplete debug upload", zap.Error(deleteErr))
			}
		}
		return reusableGeminiFile{}, uploadBytes, fmt.Errorf("upload debug source: %w", err)
	}
	expires := time.Now().UTC().Add(geminiFileCacheTTL)
	if err := w.DB.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"gemini_file_uri":        upload.FileURI,
		"gemini_file_name":       upload.FileName,
		"gemini_mime_type":       upload.MIMEType,
		"gemini_file_expires_at": expires,
	}).Error; err != nil {
		w.logger.Warn("failed to persist debug Gemini cache metadata", zap.Uint("run_id", run.ID), zap.Error(err))
	}
	return keepLocalForFrameGrids(reusableGeminiFile{
		URI: upload.FileURI, Name: upload.FileName, MIMEType: upload.MIMEType,
		Expires: &expires, Duration: upload.VideoDuration, LocalPath: localPath,
	}), uploadBytes, nil
}

func (w *Worker) findReusableChunkDebugGeminiFile(ctx context.Context, run *db.ChunkReanalysisRun, sourceURI string) reusableGeminiFile {
	cutoff := time.Now().UTC().Add(time.Minute)
	if run.SourceGCSURI == sourceURI && run.GeminiFileURI != "" && run.GeminiFileName != "" &&
		run.GeminiFileExpiresAt != nil && run.GeminiFileExpiresAt.After(cutoff) &&
		w.geminiFileStillExists(ctx, run.GeminiFileName) {
		return reusableGeminiFile{
			URI: run.GeminiFileURI, Name: run.GeminiFileName,
			MIMEType: run.GeminiMIMEType, Expires: run.GeminiFileExpiresAt,
		}
	}

	var previous db.ChunkReanalysisRun
	err := w.DB.WithContext(ctx).
		Where("session_id = ? AND profile_id = ? AND source_gcs_uri = ? AND gemini_file_uri <> '' AND gemini_file_name <> '' AND gemini_file_expires_at > ?",
			run.SessionID, run.ProfileID, sourceURI, cutoff).
		Where("id <> ?", run.ID).
		Order("created_at DESC").First(&previous).Error
	if err == nil && w.geminiFileStillExists(ctx, previous.GeminiFileName) {
		return reusableGeminiFile{
			URI: previous.GeminiFileURI, Name: previous.GeminiFileName,
			MIMEType: previous.GeminiMIMEType, Expires: previous.GeminiFileExpiresAt,
		}
	}

	var sessionRun db.SessionReanalysisRun
	err = w.DB.WithContext(ctx).
		Where("session_id = ? AND profile_id = ? AND source_gcs_uri = ? AND gemini_file_uri <> '' AND gemini_file_name <> '' AND gemini_file_expires_at > ?",
			run.SessionID, run.ProfileID, sourceURI, cutoff).
		Order("created_at DESC").First(&sessionRun).Error
	if err == nil && w.geminiFileStillExists(ctx, sessionRun.GeminiFileName) {
		return reusableGeminiFile{
			URI: sessionRun.GeminiFileURI, Name: sessionRun.GeminiFileName,
			MIMEType: sessionRun.GeminiMIMEType, Expires: sessionRun.GeminiFileExpiresAt,
		}
	}
	return reusableGeminiFile{}
}

const (
	debugAnalysisModeFrameGrid = "frame-grid-v1"
	debugAnalysisModeVideo     = "video"
)

// chunkDebugAnalysisMode plans frame grids when local media exists and the
// interval keeps at least minReanalysisFrameGridFPS; otherwise VideoMetadata video.
func chunkDebugAnalysisMode(hasLocalMedia bool, interval time.Duration) string {
	if hasLocalMedia && frameGridEligible(interval, FrameGridOptions{}) {
		return debugAnalysisModeFrameGrid
	}
	return debugAnalysisModeVideo
}

func chunkDebugPromptHash(prompt, mode string) string {
	sum := sha256.Sum256([]byte(prompt + "\n#analysis-mode:" + mode))
	return hex.EncodeToString(sum[:])
}

func shouldReuseChunkDebugCandidate(rawSnapshot []byte) bool {
	if len(rawSnapshot) == 0 {
		return false
	}
	var flags struct {
		ReusePrevious bool `json:"reuse_previous"`
	}
	return json.Unmarshal(rawSnapshot, &flags) == nil && flags.ReusePrevious
}

func (w *Worker) findReusableChunkDebugCandidate(ctx context.Context, run *db.ChunkReanalysisRun, selectedModel string, promptHashes []string, media chunkDebugMedia) (db.ChunkReanalysisRun, bool) {
	if w.DB == nil {
		return db.ChunkReanalysisRun{}, false
	}
	var prev db.ChunkReanalysisRun
	err := w.DB.WithContext(ctx).
		Where("session_id = ? AND profile_id = ? AND chunk_analysis_result_id = ? AND id <> ?",
			run.SessionID, run.ProfileID, run.ChunkAnalysisResultID, run.ID).
		Where("status = ? AND source_gcs_uri = ? AND prompt_hash IN ? AND schema_version = ?",
			db.ChunkReanalysisStatusCompleted, media.GCSURI, promptHashes, chunkDebugSchemaVersion).
		Where("ABS(COALESCE(media_start_secs, -1) - ?) < 0.005 AND ABS(COALESCE(media_end_secs, -1) - ?) < 0.005",
			media.Start.Seconds(), media.End.Seconds()).
		Where("raw_output <> '' AND structured_candidate IS NOT NULL").
		Order("completed_at DESC, id DESC").
		First(&prev).Error
	if err != nil {
		return db.ChunkReanalysisRun{}, false
	}
	if prev.Model != "" && selectedModel != "" && !strings.EqualFold(prev.Model, selectedModel) {
		return db.ChunkReanalysisRun{}, false
	}
	return prev, true
}

func (w *Worker) geminiFileStillExists(ctx context.Context, name string) bool {
	exists, err := w.GeminiClient.FileExists(ctx, name)
	if err != nil {
		w.logger.Warn("failed to validate reusable Gemini file", zap.String("file_name", name), zap.Error(err))
		return false
	}
	return exists
}

func (w *Worker) buildChunkDebugReanalysisPrompt(target *chunkDebugTarget) string {
	prompt := baseChunkAnalysisPrompt()
	prompt += levelPolicyForFitnessLevel(w.lookupFitnessLevel(target.ProfileID))
	if wod := buildWODContext(target.WODDescription); wod != "" {
		prompt += wod
	}
	prompt += buildMovementHintsContext(movementHintsFromDocument(target.MovementHints))
	if target.ProfileInjuries != nil && strings.TrimSpace(*target.ProfileInjuries) != "" {
		prompt += fmt.Sprintf("\n\n## 알려진 부상 사항\n%s\n(영상에서 해당 부위의 위험한 자세가 직접 보일 때만 경고하세요.)", *target.ProfileInjuries)
	}
	prompt += fmt.Sprintf("\n\n## 개인 프로필\n%s", w.lookupProfileString(target.ProfileID))
	prompt += w.buildTargetPersonContext(target.ProfileID, target.SessionID)
	if target.HeartRateBPM > 0 {
		prompt += fmt.Sprintf(`

## 청크 심박수 참고값
이 청크에 저장된 심박수는 %d bpm입니다. 측정 시점의 정밀도는 보장되지 않습니다.
심박수만으로 피로를 판정하거나 피로 구간을 선택하지 마세요. 관찰 단계에서는 이 참고값을
피로·힘·강도 또는 자세 변화 원인의 근거로 사용하지 마세요.`, target.HeartRateBPM)
	}
	prompt += `

## 디버그 재분석 판정 규칙
- 기존 분석 결과와 사용자의 교정 내용은 제공되지 않습니다. 현재 영상 근거만 독립적으로 판정하세요.
- 입력된 WOD 설명은 불완전하고 비배타적인 참고 정보입니다. 영상에서 명확히 보이는 다른 종목도 그대로 식별하세요.
- 대상 인물이 기구에 직접 접촉하고 해당 동작 패턴을 수행하는지 확인하세요. 주변 기구나 배경 인물은 종목 근거가 아닙니다.
- 걷기, 휴식, 준비, 장비 세팅은 [NO_EXERCISE]로 판정하고 피로 동작으로 분류하지 마세요.
- 대상 인물이 운동 중인 것은 분명하지만 종목을 뒷받침할 시각적 근거가 부족하면 [EXERCISE: Unknown]을 사용하세요.
- 대상 인물이 운동 중인지조차 시각적으로 확인할 수 없는 경우에만 [NO_EXERCISE]를 사용하세요.`
	prompt += ObservedSignalsPrompt
	prompt += ObservationOnlyOutputCheckPrompt
	return prompt
}

func parseChunkDebugCandidate(analysis string) chunkDebugCandidate {
	observed := map[string]any{}
	_ = json.Unmarshal([]byte(parseObservedSignals(analysis)), &observed)
	output := stripObservedSignals(stripExerciseTag(analysis))
	return chunkDebugCandidate{
		ExerciseType:    parseChunkExercise(analysis),
		Output:          output,
		ObservedSignals: observed,
	}
}

func (w *Worker) failChunkDebugRun(ctx context.Context, run *db.ChunkReanalysisRun, retryCount int, started time.Time, terminalStatus, safeError string, cause error) error {
	if retryCount < chunkDebugMaxRetries {
		_ = w.DB.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":      db.ChunkReanalysisStatusQueued,
			"safe_error":  "",
			"duration_ms": time.Since(started).Milliseconds(),
		}).Error
		return cause
	}
	if err := w.completeChunkDebugRun(ctx, run.ID, terminalStatus, started, map[string]any{"safe_error": safeError}); err != nil {
		return err
	}
	w.logger.Error("Chunk debug re-analysis failed permanently", zap.Uint("run_id", run.ID), zap.Error(cause))
	return fmt.Errorf("chunk debug re-analysis failed: %v: %w", cause, asynq.SkipRetry)
}

func (w *Worker) completeChunkDebugRun(ctx context.Context, runID uint, status string, started time.Time, values map[string]any) error {
	now := time.Now().UTC()
	values["status"] = status
	values["duration_ms"] = time.Since(started).Milliseconds()
	values["completed_at"] = now
	if err := w.DB.WithContext(ctx).Model(&db.ChunkReanalysisRun{}).Where("id = ?", runID).Updates(values).Error; err != nil {
		w.logger.Error("failed to persist terminal chunk re-analysis state", zap.Uint("run_id", runID), zap.Error(err))
		return fmt.Errorf("persist terminal chunk re-analysis state: %w", err)
	}
	return nil
}

func isChunkReanalysisTerminal(status string) bool {
	switch status {
	case db.ChunkReanalysisStatusCompleted,
		db.ChunkReanalysisStatusFailed,
		db.ChunkReanalysisStatusVideoUnavailable,
		db.ChunkReanalysisStatusIntervalUnavailable:
		return true
	default:
		return false
	}
}

func safeChunkDebugError(status string) string {
	switch status {
	case db.ChunkReanalysisStatusVideoUnavailable:
		return "The session video is unavailable."
	case db.ChunkReanalysisStatusIntervalUnavailable:
		return "An exact media interval is unavailable for this chunk."
	default:
		return "The chunk could not be re-analyzed."
	}
}

func debugMediaIntervalValid(start, end *float64) bool {
	return start != nil && end != nil && *start >= 0 && *end > *start
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func nullablePositiveDurationSeconds(duration time.Duration) any {
	if duration <= 0 {
		return nil
	}
	return duration.Seconds()
}

func fileVideoDuration(file reusableGeminiFile) time.Duration {
	return file.Duration
}

func chunkDebugMIMEType(value string) string {
	if strings.TrimSpace(value) == "" {
		return "video/mp4"
	}
	return value
}
