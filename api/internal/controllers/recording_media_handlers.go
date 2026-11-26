package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errRecordingMediaConflict = errors.New("recording media protocol conflict")
	errRecordingMediaSource   = errors.New("recording media source is unavailable")
	clientChunkIDPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// IDs and GCS generations travel as strings across the JavaScript boundary.
type RecordingMediaReceiptInput struct {
	ChunkID          string `json:"chunk_id"`
	SourceGeneration string `json:"source_generation"`
}

type RecordingMediaChunkReceipt struct {
	Message          string `json:"message"`
	TaskID           string `json:"task_id"`
	SessionID        string `json:"session_id"`
	MediaRunID       string `json:"media_run_id"`
	ChunkID          string `json:"chunk_id"`
	SourceGeneration string `json:"source_generation"` // google cloud storage generation
	GCSURI           string `json:"gcs_uri"`
}

// Legacy admission is latched in the same transaction as the original queue
// acceptance, including before an AI worker has produced its first result.
func markLegacyRecordingMediaTx(tx *gorm.DB, profileID uint, sessionID string) error {
	var run db.RecordingMediaRun
	err := tx.Where("session_id = ?", sessionID).First(&run).Error
	if err == nil {
		if run.Version != 0 || run.ProfileID != profileID {
			return errRecordingMediaConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	err = tx.Create(&db.RecordingMediaRun{ProfileID: profileID, SessionID: sessionID, Version: 0, State: "LEGACY", Manifest: db.JSONDocument(`[]`), Intervals: db.JSONDocument(`[]`)}).Error
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return errRecordingMediaConflict
	}
	return err
}

func (ctl *Controller) recordingMediaError(c *gin.Context, err error) {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		logger.Log.Warn("recording media conflict", zap.Error(err))
		c.JSON(http.StatusConflict, gin.H{"error": "recording media identity is already registered", "error_code": "RECORDING_MEDIA_CONFLICT"})
		return
	}
	switch {
	case errors.Is(err, errRecordingMediaConflict):
		logger.Log.Warn("recording media conflict", zap.Error(err))
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "error_code": "RECORDING_MEDIA_CONFLICT"})
	case errors.Is(err, errRecordingMediaSource):
		logger.Log.Warn("recording media source unavailable", zap.Error(err))
		c.JSON(http.StatusConflict, gin.H{"error": "uploaded chunk is unavailable or empty", "error_code": "RECORDING_MEDIA_SOURCE_UNAVAILABLE"})
	default:
		if writeSessionProcessingConflict(c, err) {
			return
		}
		logger.Log.Error("recording media admission failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to accept recording media"})
	}
}

func (ctl *Controller) kickRecordingMedia(c *gin.Context, runID uint) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()

	if err := worker.DispatchRecordingMedia(ctx, ctl.db, ctl.queueClient, runID); err != nil {
		// The accepted DB intent is authoritative; recovery retries a lost kick.
		logger.Log.Warn("recording media dispatch deferred to recovery", zap.Uint("run_id", runID), zap.Error(err))
	}
}

func recordingMediaChunkReceipt(runID uint, sessionID string, chunk db.RecordingChunk) RecordingMediaChunkReceipt {
	return RecordingMediaChunkReceipt{
		Message:          "Chunk accepted",
		TaskID:           fmt.Sprintf("recording:%d:chunk-ai:%d", runID, chunk.ID),
		SessionID:        sessionID,
		MediaRunID:       strconv.FormatUint(uint64(runID), 10),
		ChunkID:          strconv.FormatUint(uint64(chunk.ID), 10),
		SourceGeneration: strconv.FormatInt(chunk.SourceGeneration, 10),
		GCSURI:           chunk.SourceURI,
	}
}

func (ctl *Controller) getRecordingChunkIfExists(conn *gorm.DB, req ChunkCompleteRequest, run *db.RecordingMediaRun) (*db.RecordingChunk, error) {
	if run.Version != db.RecordingMediaVersion {
		return nil, fmt.Errorf("%w: existing recordings cannot change pipeline version", errRecordingMediaConflict)
	}

	var chunk db.RecordingChunk
	err := conn.Where("run_id = ? AND client_chunk_id = ?", run.ID, req.ClientChunkID).First(&chunk).Error
	if err == nil {
		if chunk.CaptureOrder != req.CaptureOrder || chunk.CaptureStartSecs != req.StartSecs || chunk.CaptureEndSecs != req.EndSecs {
			return nil, fmt.Errorf("%w: chunk identity already has a different capture interval or order", errRecordingMediaConflict)
		}
		return &chunk, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if run.State != "RECORDING" || run.ManifestHash != "" {
		return nil, fmt.Errorf("%w: recording manifest is already sealed", errRecordingMediaConflict)
	}

	var count int64
	if err := conn.Model(&db.RecordingChunk{}).Where("run_id = ? AND capture_order = ?", run.ID, req.CaptureOrder).Count(&count).Error; err != nil {
		return nil, err
	}

	if count != 0 {
		return nil, fmt.Errorf("%w: capture order already belongs to another chunk", errRecordingMediaConflict)
	}

	return nil, nil
}

func (ctl *Controller) precheckRecordingMediaChunk(ctx context.Context, req ChunkCompleteRequest) (object string, needOutputCheck bool, existingRunID uint, existingChunk *db.RecordingChunk, err error) {
	if err := db.SessionAutomaticWorkAllowed(ctx, ctl.db, req.ProfileID, req.SessionID); err != nil {
		logger.Log.Warn("session automatic work not allowed", zap.Uint("profile_id", req.ProfileID), zap.String("session_id", req.SessionID), zap.Error(err))
		return "", false, 0, nil, err
	}

	conn := ctl.db.WithContext(ctx)
	var preRun db.RecordingMediaRun
	err = conn.Where("profile_id = ? AND session_id = ?", req.ProfileID, req.SessionID).First(&preRun).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := ensureNewRecordingMediaDB(conn, req.ProfileID, req.SessionID); err != nil {
			return "", false, 0, nil, err
		}
		bucket, parsedObject, parseErr := storage.ParseGCSURI(req.GCSURI)
		if parseErr != nil || bucket != ctl.bucketName || !validRecordingSourceObject(parsedObject, req.ProfileID, req.SessionID) {
			return "", false, 0, nil, fmt.Errorf("%w: invalid original chunk object", errRecordingMediaConflict)
		}
		return parsedObject, true, 0, nil, nil
	}
	if err != nil {
		return "", false, 0, nil, err
	}

	var count int64
	preChunk, err := ctl.getRecordingChunkIfExists(conn, req, &preRun)
	if err != nil {
		return "", false, 0, nil, err
	}

	if preChunk != nil {
		return "", false, preRun.ID, preChunk, nil
	}

	bucket, parsedObject, parseErr := storage.ParseGCSURI(req.GCSURI)
	if parseErr != nil || bucket != ctl.bucketName || !validRecordingSourceObject(parsedObject, req.ProfileID, req.SessionID) {
		return "", false, 0, nil, fmt.Errorf("%w: invalid original chunk object", errRecordingMediaConflict)
	}
	if err := conn.Model(&db.RecordingChunk{}).Where("run_id = ? AND source_object = ?", preRun.ID, parsedObject).Count(&count).Error; err != nil {
		return "", false, 0, nil, err
	}
	if count != 0 {
		return "", false, 0, nil, fmt.Errorf("%w: a physical upload cannot represent multiple chunks", errRecordingMediaConflict)
	}
	return parsedObject, false, 0, nil, nil
}

func (ctl *Controller) admitRecordingMediaChunkTx(
	ctx context.Context,
	tx *gorm.DB,
	req ChunkCompleteRequest,
	object string,
	attrs *gcs.ObjectAttrs,
) (*db.RecordingMediaRun, *db.RecordingChunk, error) {
	var run db.RecordingMediaRun
	var chunk db.RecordingChunk

	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("profile_id = ? AND session_id = ?", req.ProfileID, req.SessionID).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := ensureNewRecordingMediaDB(tx, req.ProfileID, req.SessionID); err != nil {
			return nil, nil, err
		}
		now := time.Now().UTC()
		run = db.RecordingMediaRun{ProfileID: req.ProfileID, SessionID: req.SessionID, Version: db.RecordingMediaVersion, State: "RECORDING", Manifest: db.JSONDocument(`[]`), Intervals: db.JSONDocument(`[]`), NextAttemptAt: &now}
		if err := tx.Create(&run).Error; err != nil {
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	}

	var count int64
	existing, err := ctl.getRecordingChunkIfExists(tx, req, &run)
	if err != nil {
		return nil, nil, err
	}

	if existing != nil {
		// The first accepted physical upload owns this logical capture. A
		// retried notification does not replace its immutable source bytes.
		chunk = *existing
		return &run, &chunk, nil
	}

	if err := tx.Model(&db.RecordingChunk{}).Where("run_id = ? AND source_object = ?", run.ID, object).Count(&count).Error; err != nil {
		return nil, nil, err
	}

	if count != 0 {
		return nil, nil, fmt.Errorf("%w: a physical upload cannot represent multiple chunks", errRecordingMediaConflict)
	}

	chunk = db.RecordingChunk{RunID: run.ID, ProfileID: req.ProfileID, SessionID: req.SessionID, ClientChunkID: req.ClientChunkID, CaptureOrder: req.CaptureOrder, SourceURI: req.GCSURI, SourceObject: object, SourceGeneration: attrs.Generation, SourceBytes: attrs.Size, CaptureStartSecs: req.StartSecs, CaptureEndSecs: req.EndSecs}
	if err := tx.Create(&chunk).Error; err != nil {
		return nil, nil, err
	}

	var sessions int64
	if err := tx.Model(&db.Session{}).Where("profile_id = ? AND session_id = ?", req.ProfileID, req.SessionID).Count(&sessions).Error; err != nil {
		return nil, nil, err
	}
	var task *asynq.Task
	if sessions > 0 {
		task, err = worker.NewChunkAnalysisWithSessionTask(req.SessionID, chunk.SourceURI, req.ProfileID, req.StartSecs, req.EndSecs, req.HeartRateBPM, req.WorkoutConfidence)
	} else {
		task, err = worker.NewChunkAnalysisTask(req.SessionID, chunk.SourceURI, req.WorkoutType, req.Movements, req.Injuries, req.ProfileID, req.StartSecs, req.EndSecs, req.HeartRateBPM, req.WODDescription, req.WorkoutConfidence)
	}
	if err != nil {
		return nil, nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return nil, nil, err
	}

	payload["recording_chunk_id"] = chunk.ID
	payload["source_generation"] = chunk.SourceGeneration
	payload["media_run_id"] = run.ID
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}

	chunk.AIType, chunk.AIPayload = task.Type(), db.JSONDocument(encoded)
	if err := tx.Model(&chunk).Updates(map[string]any{"ai_type": chunk.AIType, "ai_payload": chunk.AIPayload}).Error; err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	prep := db.ChunkMediaPreparation{RunID: run.ID, ChunkID: chunk.ID, SourceGeneration: chunk.SourceGeneration, RecipeVersion: "concat-v1", ProfileKey: "native", State: "PENDING", NextAttemptAt: &now}
	if err := tx.Create(&prep).Error; err != nil {
		return nil, nil, err
	}
	if err := tx.Model(&run).Update("next_attempt_at", now).Error; err != nil {
		return nil, nil, err
	}

	if err := persistRecordingMediaMetadata(ctx, tx, req.SessionID, req.ProfileID, req.Movements, req.WODDescription, req.AppearanceHints); err != nil {
		return nil, nil, err
	}

	return &run, &chunk, nil
}

func (ctl *Controller) handleRecordingMediaChunk(c *gin.Context, req ChunkCompleteRequest) {
	if !clientChunkIDPattern.MatchString(req.ClientChunkID) || req.CaptureOrder < 1 || req.CaptureOrder > 10000 || !activity.ValidInterval(req.StartSecs, req.EndSecs) {
		logger.Log.Warn("invalid recording media chunk identity, order, or interval", zap.Uint("profile_id", req.ProfileID), zap.String("session_id", req.SessionID), zap.String("client_chunk_id", req.ClientChunkID), zap.Int("capture_order", req.CaptureOrder), zap.Float64("start_secs", req.StartSecs), zap.Float64("end_secs", req.EndSecs))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recording media version, chunk identity, order, or interval"})
		return
	}

	// this is for mobile app to cancel the request if the context is cancelled
	// mobile app often can be backgrounded and the request can be cancelled
	if err := c.Request.Context().Err(); err != nil {
		ctl.recordingMediaError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), sessionUploadAcceptanceTimeout)
	defer cancel()

	// Stage 1: Read-only DB validation (no row locks, zero GCS calls on conflict or retry).
	object, needOutputCheck, existingRunID, existingChunk, err := ctl.precheckRecordingMediaChunk(ctx, req)
	if err != nil {
		ctl.recordingMediaError(c, err)
		return
	}

	if existingChunk != nil {
		ctl.kickRecordingMedia(c, existingRunID)
		c.JSON(http.StatusAccepted, recordingMediaChunkReceipt(existingRunID, req.SessionID, *existingChunk))
		return
	}

	// Stage 2: External GCS metadata verification outside any DB transaction.
	if needOutputCheck {
		if err := ctl.ensureNoRecordingMediaOutputs(ctx, req.ProfileID, req.SessionID); err != nil {
			ctl.recordingMediaError(c, err)
			return
		}
	}

	attrs, err := ctl.storageClient.ObjectAttrs(ctx, object)
	if errors.Is(err, gcs.ErrObjectNotExist) {
		ctl.recordingMediaError(c, errRecordingMediaSource)
		return
	}

	if err != nil {
		ctl.recordingMediaError(c, fmt.Errorf("read uploaded chunk metadata: %w", err))
		return
	}

	if attrs == nil || attrs.Generation <= 0 || attrs.Size <= 0 {
		ctl.recordingMediaError(c, errRecordingMediaSource)
		return
	}

	// Stage 3: Short SQL transaction with zero external network I/O.
	var run db.RecordingMediaRun
	var chunk db.RecordingChunk
	err = db.WithSessionAutomaticWork(ctx, ctl.db, req.ProfileID, req.SessionID, func(tx *gorm.DB) error {
		runPtr, chunkPtr, txErr := ctl.admitRecordingMediaChunkTx(ctx, tx, req, object, attrs)
		if txErr != nil {
			return txErr
		}
		run = *runPtr
		chunk = *chunkPtr
		return nil
	})

	if err != nil {
		ctl.recordingMediaError(c, err)
		return
	}

	ctl.kickRecordingMedia(c, run.ID)
	c.JSON(http.StatusAccepted, recordingMediaChunkReceipt(run.ID, req.SessionID, chunk))
}

func validRecordingSourceObject(object string, profileID uint, sessionID string) bool {
	prefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
	if !strings.HasPrefix(object, prefix) {
		return false
	}
	name := strings.TrimPrefix(object, prefix)
	if name == "" || strings.Contains(name, "/") || isRecordingMediaOutput(name) {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".mp4" || ext == ".mov"
}

func isRecordingMediaOutput(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return base == "merged.mp4" || base == "analysis.mp4" || base == "hardsubbed.mp4" || strings.HasPrefix(base, "split_chunk_") || strings.HasPrefix(base, "hl_") || strings.Contains(base, "_merged_") || strings.Contains(base, "_encoded_") || strings.Contains(base, "_hardsubbed_")
}

func ensureNewRecordingMediaDB(conn *gorm.DB, profileID uint, sessionID string) error {
	var existingRuns int64
	if err := conn.Model(&db.RecordingMediaRun{}).Where("session_id = ?", sessionID).Count(&existingRuns).Error; err != nil {
		return err
	}

	if existingRuns != 0 {
		return fmt.Errorf("%w: this session is already registered to another profile", errRecordingMediaConflict)
	}

	var existingSession db.Session
	err := conn.Select("id, profile_id, status, idempotency_key, effort, activity_summary").
		Where("session_id = ?", sessionID).
		First(&existingSession).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if err == nil {
		if existingSession.ProfileID != profileID {
			return fmt.Errorf("%w: this session is already registered to another profile", errRecordingMediaConflict)
		}

		if !isFreshRecordingSession(existingSession) {
			return fmt.Errorf("%w: only new recordings can use this media pipeline", errRecordingMediaConflict)
		}
	}

	analysisResults := &db.AnalysisResult{}
	err = conn.Select("profile_id, status, output, sensor_state, sensor_version, archived_at, automatic_processing_cancelled_at").
		Where("session_id = ?", sessionID).
		First(analysisResults).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if err == nil {
		// allow pending analysis results to be reused for new recordings due to sensor upload
		if !isSensorPlaceholderAnalysis(analysisResults, profileID) {
			return fmt.Errorf("%w: only new recordings can use this media pipeline", errRecordingMediaConflict)
		}
	}

	var count int64
	if err := conn.Table("chunk_analysis_results").Where("session_id = ?", sessionID).Limit(1).Count(&count).Error; err != nil {
		return err
	}

	if count != 0 {
		return fmt.Errorf("%w: only new recordings can use this media pipeline", errRecordingMediaConflict)
	}
	return nil
}

func isSensorPlaceholderAnalysis(analysisResult *db.AnalysisResult, profileID uint) bool {
	if analysisResult == nil || analysisResult.ProfileID != profileID {
		return false
	}
	if analysisResult.Status != "PENDING" || strings.TrimSpace(analysisResult.Output) != "" {
		return false
	}
	if analysisResult.ArchivedAt != nil || analysisResult.AutomaticProcessingCancelledAt != nil {
		return false
	}
	return analysisResult.SensorVersion > 0 && analysisResult.SensorState != "" && analysisResult.SensorState != db.SensorStateNone
}

func isFreshRecordingSession(sess db.Session) bool {
	if sess.Status != db.SessionStatusStarted || sess.Effort != nil || hasSessionActivitySummary(sess.ActivitySummary) {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(sess.IdempotencyKey))
	return !strings.HasPrefix(key, "backfill:") && !strings.HasPrefix(key, "live:") && !strings.HasPrefix(key, "wod:")
}

func hasSessionActivitySummary(doc db.NullableJSONDocument) bool {
	trimmed := strings.TrimSpace(string(doc))
	return trimmed != "" && trimmed != "null"
}

func (ctl *Controller) ensureNoRecordingMediaOutputs(ctx context.Context, profileID uint, sessionID string) error {
	prefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
	objects, err := ctl.storageClient.ListObjects(ctx, prefix)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if isRecordingMediaOutput(object) {
			return fmt.Errorf("%w: this session already contains processed video", errRecordingMediaConflict)
		}
	}
	return nil
}

func persistRecordingMediaMetadata(ctx context.Context, tx *gorm.DB, sessionID string, profileID uint, movements []string, description, appearance string) error {
	if err := persistSessionMovementHints(ctx, tx, sessionID, profileID, movements); err != nil {
		return err
	}
	if err := persistSessionWODDescription(ctx, tx, sessionID, profileID, description); err != nil {
		return err
	}
	return persistSessionAppearanceHints(ctx, tx, sessionID, profileID, &AppearanceInput{Appearance: appearance})
}

func parseRecordingMediaManifest(receipts []RecordingMediaReceiptInput) ([]db.RecordingMediaManifestEntry, error) {
	manifest := make([]db.RecordingMediaManifestEntry, 0, len(receipts))
	seen := make(map[uint]bool, len(receipts))
	for _, receipt := range receipts {
		id, err := strconv.ParseUint(receipt.ChunkID, 10, strconv.IntSize)
		generation, genErr := strconv.ParseInt(receipt.SourceGeneration, 10, 64)
		if err != nil || genErr != nil || id == 0 || generation <= 0 || seen[uint(id)] || strconv.FormatUint(id, 10) != receipt.ChunkID || strconv.FormatInt(generation, 10) != receipt.SourceGeneration {
			return nil, errors.New("invalid or duplicate recording chunk receipt")
		}
		seen[uint(id)] = true
		manifest = append(manifest, db.RecordingMediaManifestEntry{ChunkID: uint(id), SourceGeneration: generation})
	}
	return manifest, nil
}

func (ctl *Controller) handleRecordingMediaMerge(c *gin.Context, req MergeChunksRequest) {
	var existing db.RecordingMediaRun
	err := ctl.db.WithContext(c.Request.Context()).Where("profile_id = ? AND session_id = ?", req.ProfileID, req.SessionID).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		ctl.recordingMediaError(c, err)
		return
	}

	if errors.Is(err, gorm.ErrRecordNotFound) || existing.Version != db.RecordingMediaVersion {
		ctl.recordingMediaError(c, fmt.Errorf("%w: recording has no v1 chunk admission", errRecordingMediaConflict))
		return
	}

	var requested []db.RecordingMediaManifestEntry
	isSealedRetry := existing.ManifestHash != "" && req.ExpectedChunkCount == 0 && len(req.Chunks) == 0
	if !isSealedRetry {
		if req.ExpectedChunkCount < 1 || req.ExpectedChunkCount > 10000 || req.ExpectedChunkCount != len(req.Chunks) {
			logger.Log.Warn("invalid merge manifest count", zap.Uint("profile_id", req.ProfileID), zap.String("session_id", req.SessionID), zap.Int("expected_chunk_count", req.ExpectedChunkCount), zap.Int("chunks_len", len(req.Chunks)))
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected_chunk_count must match the complete ordered receipt list"})
			return
		}
		requested, err = parseRecordingMediaManifest(req.Chunks)
		if err != nil {
			logger.Log.Warn("invalid merge manifest receipts", zap.Uint("profile_id", req.ProfileID), zap.String("session_id", req.SessionID), zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if err := c.Request.Context().Err(); err != nil {
		ctl.recordingMediaError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), sessionUploadAcceptanceTimeout)
	defer cancel()
	var run db.RecordingMediaRun
	err = db.WithSessionAutomaticWork(ctx, ctl.db, req.ProfileID, req.SessionID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, existing.ID).Error; err != nil {
			return err
		}
		if run.Version != db.RecordingMediaVersion || run.State == "CANCELLED" {
			return errRecordingMediaConflict
		}
		if run.ManifestHash != "" {
			if requested != nil {
				raw, _ := json.Marshal(requested)
				digest := sha256.Sum256(raw)
				if hex.EncodeToString(digest[:]) != run.ManifestHash {
					return fmt.Errorf("%w: recording manifest cannot change after sealing", errRecordingMediaConflict)
				}
			}
			return rearmRecordingMediaRunTx(tx, &run)
		}
		if len(requested) == 0 || run.State != "RECORDING" {
			return fmt.Errorf("%w: an unsealed recording requires the complete receipt list", errRecordingMediaConflict)
		}
		var chunks []db.RecordingChunk
		if err := tx.Where("run_id = ?", run.ID).Order("capture_order ASC").Find(&chunks).Error; err != nil {
			return err
		}
		if len(chunks) != len(requested) {
			return fmt.Errorf("%w: manifest must include every admitted chunk", errRecordingMediaConflict)
		}
		for i, chunk := range chunks {
			if chunk.CaptureOrder != i+1 || requested[i].ChunkID != chunk.ID || requested[i].SourceGeneration != chunk.SourceGeneration {
				return fmt.Errorf("%w: manifest does not match accepted capture order and generations", errRecordingMediaConflict)
			}
		}
		raw, err := json.Marshal(requested)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		payload := worker.VideoAnalysisPayload{MediaRunID: run.ID, SessionID: req.SessionID, ProfileID: req.ProfileID, FilePath: fmt.Sprintf("gs://%s/videos/%d/%s/", ctl.bucketName, req.ProfileID, req.SessionID), WorkoutType: worker.NormalizeWorkoutType(req.WorkoutType), Movements: req.Movements, Injuries: req.Injuries, EnableTTS: req.EnableTTS, WODDescription: req.WODDescription, PipelineMode: req.PipelineMode, MergeRequestedAt: time.Now().UTC()}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		run.Manifest, run.ManifestHash, run.Payload, run.State, run.NextAttemptAt = db.JSONDocument(raw), hex.EncodeToString(digest[:]), db.JSONDocument(encoded), "WAITING_PREPARATION", &now
		if err := tx.Model(&run).Updates(map[string]any{"manifest": run.Manifest, "manifest_hash": run.ManifestHash, "payload": run.Payload, "state": run.State, "next_attempt_at": now}).Error; err != nil {
			return err
		}
		return persistRecordingMediaMetadata(ctx, tx, req.SessionID, req.ProfileID, req.Movements, req.WODDescription, req.AppearanceHints)
	})
	if err != nil {
		ctl.recordingMediaError(c, err)
		return
	}
	ctl.kickRecordingMedia(c, run.ID)
	c.JSON(http.StatusAccepted, MergeChunksResponse{Message: "Recording merge accepted", TaskID: fmt.Sprintf("recording-media-%d", run.ID), SessionID: req.SessionID, MediaRunID: strconv.FormatUint(uint64(run.ID), 10)})
}

// A retry only rearms a previously sealed v1 run; it never discovers old media.
func (ctl *Controller) handleRecordingMediaRetry(c *gin.Context, sessionID string, profileID uint) bool {
	var run db.RecordingMediaRun
	err := ctl.db.WithContext(c.Request.Context()).Where("profile_id = ? AND session_id = ? AND version = ?", profileID, sessionID, db.RecordingMediaVersion).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}
	if err != nil {
		ctl.recordingMediaError(c, err)
		return true
	}
	ctl.handleRecordingMediaMerge(c, MergeChunksRequest{SessionID: sessionID, ProfileID: profileID})
	return true
}

func rearmRecordingMediaRunTx(tx *gorm.DB, run *db.RecordingMediaRun) error {
	if run.State != "FAILED" {
		return nil
	}
	now := time.Now().UTC()
	// Immutable source/artifact identities survive retries. The coordinator
	// resumes from those checkpoints rather than rebuilding an earlier stage.
	for _, model := range []any{&db.ChunkMediaPreparation{}, &db.AnalysisVideoPreparation{}} {
		if err := tx.Model(model).Where("run_id = ? AND state = 'FAILED'", run.ID).Updates(map[string]any{"state": "PENDING", "attempts": 0, "dispatch_version": gorm.Expr("dispatch_version + 1"), "safe_error": "", "lease_token": "", "lease_expires_at": nil, "next_attempt_at": now}).Error; err != nil {
			return err
		}
	}
	run.State = "WAITING_PREPARATION"
	if run.MergedGeneration == 0 && run.MergedStagingGeneration > 0 {
		run.State = "MERGING"
	}
	if run.MergedGeneration > 0 {
		run.State = "PREPARING_ANALYSIS"
		var ready int64
		if err := tx.Model(&db.AnalysisVideoPreparation{}).Where("run_id = ? AND state = 'READY'", run.ID).Count(&ready).Error; err != nil {
			return err
		}
		if ready > 0 {
			run.State = "READY_FOR_ANALYSIS"
		}
	}
	run.Attempts, run.NextAttemptAt = 0, &now
	updates := map[string]any{"state": run.State, "attempts": 0, "dispatch_version": gorm.Expr("dispatch_version + 1"), "safe_error": "", "lease_token": "", "lease_expires_at": nil, "next_attempt_at": now}
	if run.FailedStage == "analysis" {
		run.AnalysisDispatched = false
		updates["analysis_dispatched"] = false
		run.AnalysisAttempts = 0
		updates["analysis_attempts"] = 0
	}
	return tx.Model(run).Updates(updates).Error
}
