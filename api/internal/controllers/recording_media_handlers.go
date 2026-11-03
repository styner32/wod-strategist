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
	SourceGeneration string `json:"source_generation"`
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
	if writeSessionProcessingConflict(c, err) {
		return
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		c.JSON(http.StatusConflict, gin.H{"error": "recording media identity is already registered", "error_code": "RECORDING_MEDIA_CONFLICT"})
		return
	}
	switch {
	case errors.Is(err, errRecordingMediaConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "error_code": "RECORDING_MEDIA_CONFLICT"})
	case errors.Is(err, errRecordingMediaSource):
		c.JSON(http.StatusConflict, gin.H{"error": "uploaded chunk is unavailable or empty", "error_code": "RECORDING_MEDIA_SOURCE_UNAVAILABLE"})
	default:
		logger.Log.Error("recording media admission failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to accept recording media"})
	}
}

func (ctl *Controller) kickRecordingMedia(c *gin.Context, runID uint) {
	if ctl.queueClient == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()
	if err := worker.DispatchRecordingMedia(ctx, ctl.db, ctl.queueClient, runID); err != nil {
		// The accepted DB intent is authoritative; recovery retries a lost kick.
		logger.Log.Warn("recording media dispatch deferred to recovery", zap.Uint("run_id", runID), zap.Error(err))
	}
}

func (ctl *Controller) handleRecordingMediaChunk(c *gin.Context, req ChunkCompleteRequest) bool {
	if req.MediaPipelineVersion == 0 {
		if req.ClientChunkID != "" || req.CaptureOrder != 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "chunk identity requires media_pipeline_version 1"})
			return true
		}
		return false
	}
	if req.MediaPipelineVersion != db.RecordingMediaVersion || !clientChunkIDPattern.MatchString(req.ClientChunkID) || req.CaptureOrder < 1 || req.CaptureOrder > 10000 || !activity.ValidInterval(req.StartSecs, req.EndSecs) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recording media version, chunk identity, order, or interval"})
		return true
	}
	if err := c.Request.Context().Err(); err != nil {
		ctl.recordingMediaError(c, err)
		return true
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), sessionUploadAcceptanceTimeout)
	defer cancel()
	var run db.RecordingMediaRun
	var chunk db.RecordingChunk
	err := db.WithSessionAutomaticWork(ctx, ctl.db, req.ProfileID, req.SessionID, func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("profile_id = ? AND session_id = ?", req.ProfileID, req.SessionID).First(&run).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := ctl.ensureNewRecordingMedia(ctx, tx, req); err != nil {
				return err
			}
			now := time.Now().UTC()
			run = db.RecordingMediaRun{ProfileID: req.ProfileID, SessionID: req.SessionID, Version: db.RecordingMediaVersion, State: "RECORDING", Manifest: db.JSONDocument(`[]`), Intervals: db.JSONDocument(`[]`), NextAttemptAt: &now}
			if err := tx.Create(&run).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if run.Version != db.RecordingMediaVersion {
			return fmt.Errorf("%w: existing recordings cannot change pipeline version", errRecordingMediaConflict)
		}
		err = tx.Where("run_id = ? AND client_chunk_id = ?", run.ID, req.ClientChunkID).First(&chunk).Error
		if err == nil {
			if chunk.CaptureOrder != req.CaptureOrder || chunk.CaptureStartSecs != req.StartSecs || chunk.CaptureEndSecs != req.EndSecs {
				return fmt.Errorf("%w: chunk identity already has a different capture interval or order", errRecordingMediaConflict)
			}
			// The first accepted physical upload owns this logical capture. A
			// retried notification does not replace its immutable source bytes.
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if run.State != "RECORDING" || run.ManifestHash != "" {
			return fmt.Errorf("%w: recording manifest is already sealed", errRecordingMediaConflict)
		}
		var count int64
		if err := tx.Model(&db.RecordingChunk{}).Where("run_id = ? AND capture_order = ?", run.ID, req.CaptureOrder).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("%w: capture order already belongs to another chunk", errRecordingMediaConflict)
		}
		bucket, object, err := storage.ParseGCSURI(req.GCSURI)
		if err != nil || bucket != ctl.bucketName || !validRecordingSourceObject(object, req.ProfileID, req.SessionID) {
			return fmt.Errorf("%w: invalid original chunk object", errRecordingMediaConflict)
		}
		if err := tx.Model(&db.RecordingChunk{}).Where("run_id = ? AND source_object = ?", run.ID, object).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("%w: a physical upload cannot represent multiple chunks", errRecordingMediaConflict)
		}
		attrs, err := ctl.storageClient.ObjectAttrs(ctx, object)
		if errors.Is(err, gcs.ErrObjectNotExist) {
			return errRecordingMediaSource
		}
		if err != nil {
			return fmt.Errorf("read uploaded chunk metadata: %w", err)
		}
		if attrs == nil || attrs.Generation <= 0 || attrs.Size <= 0 {
			return errRecordingMediaSource
		}
		chunk = db.RecordingChunk{RunID: run.ID, ProfileID: req.ProfileID, SessionID: req.SessionID, ClientChunkID: req.ClientChunkID, CaptureOrder: req.CaptureOrder, SourceURI: req.GCSURI, SourceObject: object, SourceGeneration: attrs.Generation, SourceBytes: attrs.Size, CaptureStartSecs: req.StartSecs, CaptureEndSecs: req.EndSecs}
		if err := tx.Create(&chunk).Error; err != nil {
			return err
		}
		var sessions int64
		if err := tx.Model(&db.Session{}).Where("profile_id = ? AND session_id = ?", req.ProfileID, req.SessionID).Count(&sessions).Error; err != nil {
			return err
		}
		var task *asynq.Task
		if sessions > 0 {
			task, err = worker.NewChunkAnalysisWithSessionTask(req.SessionID, chunk.SourceURI, req.ProfileID, req.StartSecs, req.EndSecs, req.HeartRateBPM, req.WorkoutConfidence)
		} else {
			task, err = worker.NewChunkAnalysisTask(req.SessionID, chunk.SourceURI, req.WorkoutType, req.Movements, req.Injuries, req.ProfileID, req.StartSecs, req.EndSecs, req.HeartRateBPM, req.WODDescription, req.WorkoutConfidence)
		}
		if err != nil {
			return err
		}
		var payload map[string]any
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		payload["live_analysis_version"] = req.LiveAnalysisVersion
		payload["recording_chunk_id"] = chunk.ID
		payload["source_generation"] = chunk.SourceGeneration
		payload["media_run_id"] = run.ID
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		chunk.AIType, chunk.AIPayload = task.Type(), db.JSONDocument(encoded)
		if err := tx.Model(&chunk).Updates(map[string]any{"ai_type": chunk.AIType, "ai_payload": chunk.AIPayload}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		prep := db.ChunkMediaPreparation{RunID: run.ID, ChunkID: chunk.ID, SourceGeneration: chunk.SourceGeneration, RecipeVersion: "concat-v1", ProfileKey: "native", State: "PENDING", NextAttemptAt: &now}
		if err := tx.Create(&prep).Error; err != nil {
			return err
		}
		if err := tx.Model(&run).Update("next_attempt_at", now).Error; err != nil {
			return err
		}
		return persistRecordingMediaMetadata(ctx, tx, req.SessionID, req.ProfileID, req.Movements, req.WODDescription, req.AppearanceHints)
	})
	if err != nil {
		ctl.recordingMediaError(c, err)
		return true
	}
	ctl.kickRecordingMedia(c, run.ID)
	c.JSON(http.StatusAccepted, RecordingMediaChunkReceipt{Message: "Chunk accepted", TaskID: fmt.Sprintf("recording:%d:chunk-ai:%d", run.ID, chunk.ID), SessionID: req.SessionID, MediaRunID: strconv.FormatUint(uint64(run.ID), 10), ChunkID: strconv.FormatUint(uint64(chunk.ID), 10), SourceGeneration: strconv.FormatInt(chunk.SourceGeneration, 10), GCSURI: chunk.SourceURI})
	return true
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

func (ctl *Controller) ensureNewRecordingMedia(ctx context.Context, tx *gorm.DB, req ChunkCompleteRequest) error {
	var existingRuns int64
	if err := tx.Model(&db.RecordingMediaRun{}).Where("session_id = ?", req.SessionID).Count(&existingRuns).Error; err != nil {
		return err
	}
	if existingRuns != 0 {
		return fmt.Errorf("%w: this session is already registered to another profile", errRecordingMediaConflict)
	}
	for _, table := range []string{"sessions", "analysis_results", "chunk_analysis_results"} {
		var count int64
		if err := tx.Table(table).Where("session_id = ?", req.SessionID).Limit(1).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("%w: only new recordings can use this media pipeline", errRecordingMediaConflict)
		}
	}
	prefix := fmt.Sprintf("videos/%d/%s/", req.ProfileID, req.SessionID)
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

func (ctl *Controller) handleRecordingMediaMerge(c *gin.Context, req MergeChunksRequest) bool {
	if req.MediaPipelineVersion != 0 && req.MediaPipelineVersion != db.RecordingMediaVersion {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported media_pipeline_version"})
		return true
	}
	var existing db.RecordingMediaRun
	err := ctl.db.WithContext(c.Request.Context()).Where("profile_id = ? AND session_id = ?", req.ProfileID, req.SessionID).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		ctl.recordingMediaError(c, err)
		return true
	}
	if req.MediaPipelineVersion == 0 && (errors.Is(err, gorm.ErrRecordNotFound) || existing.Version == 0) {
		if len(req.Chunks) > 0 || req.ExpectedChunkCount != 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "recording manifest requires media_pipeline_version 1"})
			return true
		}
		return false
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || existing.Version != db.RecordingMediaVersion {
		ctl.recordingMediaError(c, fmt.Errorf("%w: recording has no v1 chunk admission", errRecordingMediaConflict))
		return true
	}
	var requested []db.RecordingMediaManifestEntry
	if req.MediaPipelineVersion == db.RecordingMediaVersion {
		if req.ExpectedChunkCount < 1 || req.ExpectedChunkCount > 10000 || req.ExpectedChunkCount != len(req.Chunks) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected_chunk_count must match the complete ordered receipt list"})
			return true
		}
		requested, err = parseRecordingMediaManifest(req.Chunks)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return true
		}
	}
	if err := c.Request.Context().Err(); err != nil {
		ctl.recordingMediaError(c, err)
		return true
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
		return true
	}
	ctl.kickRecordingMedia(c, run.ID)
	c.JSON(http.StatusAccepted, MergeChunksResponse{Message: "Recording merge accepted", TaskID: fmt.Sprintf("recording-media-%d", run.ID), SessionID: req.SessionID, MediaRunID: strconv.FormatUint(uint64(run.ID), 10)})
	return true
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
	return ctl.handleRecordingMediaMerge(c, MergeChunksRequest{SessionID: sessionID, ProfileID: profileID})
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
