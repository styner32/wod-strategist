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
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RecordingChunkQueueName         = "media:chunk"
	RecordingSessionQueueName       = "media:session"
	TypePrepareRecordingChunk       = "media:prepare_chunk"
	TypeCoordinateRecordingMedia    = "media:coordinate"
	TypeMergeRecordingMedia         = "media:merge"
	TypePrepareAnalysisVideo        = "media:prepare_analysis"
	recordingConcatRecipe           = "concat-v1"
	recordingAnalysisPreset         = "h264-720w30-crf28-aac64-v1"
	recordingMediaLeaseDuration     = 2 * time.Minute
	recordingMediaHeartbeatInterval = 30 * time.Second
	recordingMediaMaxAttempts       = 3
)

var errRecordingMediaStale = errors.New("recording media delivery is no longer current")

type recordingMediaTask struct {
	RunID           uint  `json:"run_id"`
	PreparationID   uint  `json:"preparation_id,omitempty"`
	DispatchVersion int64 `json:"dispatch_version"`
}

type recordingConcatProfile struct {
	Width        int  `json:"width"`
	Height       int  `json:"height"`
	IncludeAudio bool `json:"include_audio"`
}

func recordingJSON(value any) db.JSONDocument {
	data, _ := json.Marshal(value)
	return db.JSONDocument(data)
}

func recordingProfileKey(profile recordingConcatProfile) string {
	digest := sha256.Sum256(append([]byte(recordingConcatRecipe+":"), recordingJSON(profile)...))
	return hex.EncodeToString(digest[:])
}

func recordingAnalysisTaskID(runID, preparationID uint, dispatchVersion int64) string {
	return fmt.Sprintf("recording:%d:analysis:%d:%d", runID, preparationID, dispatchVersion)
}

func recordingMediaTaskID(kind string, payload recordingMediaTask) string {
	return fmt.Sprintf("recording:%d:%s:%d:%d", payload.RunID, kind, payload.PreparationID, payload.DispatchVersion)
}

func recordingBaseQueue(queue QueueClient) QueueClient {
	if guarded, ok := queue.(*sessionWorkQueue); ok {
		return guarded.QueueClient
	}
	return queue
}

func enqueueRecordingMedia(queue QueueClient, kind string, payload recordingMediaTask) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	opts := []asynq.Option{asynq.MaxRetry(0)}
	switch kind {
	case TypePrepareRecordingChunk:
		opts = append(opts, asynq.Queue(RecordingChunkQueueName), asynq.Timeout(5*time.Minute))
	case TypeCoordinateRecordingMedia:
		opts = append(opts, asynq.Timeout(time.Minute), asynq.Unique(time.Minute))
	case TypeMergeRecordingMedia:
		opts = append(opts, asynq.Queue(RecordingSessionQueueName), asynq.Timeout(time.Hour))
	case TypePrepareAnalysisVideo:
		opts = append(opts, asynq.Queue(RecordingSessionQueueName), asynq.Timeout(45*time.Minute))
	default:
		return errors.New("unknown recording media task")
	}
	if kind != TypeCoordinateRecordingMedia {
		opts = append(opts, asynq.TaskID(recordingMediaTaskID(kind, payload)), asynq.Retention(24*time.Hour))
	}
	_, err = queue.Enqueue(asynq.NewTask(kind, data, opts...))
	if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// DispatchRecordingMedia publishes the durable outbox after ingress commits.
// Repeating it is safe; recovery also calls it after a queue outage or restart.
func DispatchRecordingMedia(ctx context.Context, database *gorm.DB, queue QueueClient, runID uint) error {
	if database == nil || queue == nil {
		return errors.New("recording media dependencies are unavailable")
	}
	queue = recordingBaseQueue(queue)
	var run db.RecordingMediaRun
	if err := database.WithContext(ctx).Where("id = ? AND version = ?", runID, db.RecordingMediaVersion).First(&run).Error; err != nil {
		return err
	}
	if run.State == "CANCELLED" || run.State == "COMPLETED" {
		return nil
	}
	if run.State == "RECORDING" {
		now := time.Now().UTC()
		var hasPending bool
		if err := database.WithContext(ctx).Raw(`
			SELECT EXISTS (
				SELECT 1 FROM recording_chunks WHERE run_id = ? AND ai_dispatched = false
			) OR EXISTS (
				SELECT 1 FROM chunk_media_preparations WHERE run_id = ? AND state = 'PENDING' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			)
		`, run.ID, run.ID, now).Scan(&hasPending).Error; err != nil {
			return err
		}
		if !hasPending {
			return nil
		}
	}
	return db.WithSessionAutomaticWork(ctx, database, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		if run.State != "FAILED" {
			var chunks []db.RecordingChunk
			if err := tx.Where("run_id = ? AND ai_dispatched = ?", run.ID, false).Order("capture_order ASC").Find(&chunks).Error; err != nil {
				return err
			}
			for _, chunk := range chunks {
				var payload VideoAnalysisPayload
				if err := json.Unmarshal(chunk.AIPayload, &payload); err != nil {
					return err
				}
				payload.MediaRunID, payload.RecordingChunkID, payload.SourceGeneration = run.ID, chunk.ID, chunk.SourceGeneration
				data, err := json.Marshal(payload)
				if err != nil {
					return err
				}
				_, err = queue.Enqueue(asynq.NewTask(chunk.AIType, data), asynq.TaskID(fmt.Sprintf("recording:%d:chunk-ai:%d", run.ID, chunk.ID)), asynq.Retention(24*time.Hour))
				if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
					return err
				}
				if err := tx.Model(&db.RecordingChunk{}).Where("id = ?", chunk.ID).Update("ai_dispatched", true).Error; err != nil {
					return err
				}
			}
			var chunksToPrepare []db.ChunkMediaPreparation
			if err := tx.Where("run_id = ? AND state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", run.ID, "PENDING", time.Now().UTC()).Find(&chunksToPrepare).Error; err != nil {
				return err
			}
			for _, preparation := range chunksToPrepare {
				if err := enqueueRecordingMedia(queue, TypePrepareRecordingChunk, recordingMediaTask{run.ID, preparation.ID, preparation.DispatchVersion}); err != nil {
					return err
				}
			}
		}
		if run.State == "MERGING" && run.LeaseToken == "" && (run.NextAttemptAt == nil || !run.NextAttemptAt.After(time.Now())) {
			return enqueueRecordingMedia(queue, TypeMergeRecordingMedia, recordingMediaTask{RunID: run.ID, DispatchVersion: run.DispatchVersion})
		}
		var analyses []db.AnalysisVideoPreparation
		if run.State != "FAILED" {
			if err := tx.Where("run_id = ? AND state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", run.ID, "PENDING", time.Now().UTC()).Find(&analyses).Error; err != nil {
				return err
			}
		}
		for _, preparation := range analyses {
			if err := enqueueRecordingMedia(queue, TypePrepareAnalysisVideo, recordingMediaTask{run.ID, preparation.ID, preparation.DispatchVersion}); err != nil {
				return err
			}
		}
		if run.State == "WAITING_PREPARATION" || run.State == "MERGED" || run.State == "PREPARING_ANALYSIS" || run.State == "READY_FOR_ANALYSIS" || (run.State == "FAILED" && run.MergedGeneration > 0 && !run.ActivityDispatched) {
			return enqueueRecordingMedia(queue, TypeCoordinateRecordingMedia, recordingMediaTask{RunID: run.ID, DispatchVersion: run.DispatchVersion})
		}
		return nil
	})
}

func (w *Worker) loadRecordingMediaTask(ctx context.Context, task *asynq.Task) (recordingMediaTask, db.RecordingMediaRun, error) {
	var payload recordingMediaTask
	if task == nil || json.Unmarshal(task.Payload(), &payload) != nil || payload.RunID == 0 {
		return payload, db.RecordingMediaRun{}, asynq.SkipRetry
	}
	var run db.RecordingMediaRun
	if err := w.DB.WithContext(ctx).Where("id = ? AND version = ?", payload.RunID, db.RecordingMediaVersion).First(&run).Error; err != nil {
		return payload, run, err
	}
	if err := db.SessionAutomaticWorkAllowed(ctx, w.DB, run.ProfileID, run.SessionID); err != nil {
		return payload, run, err
	}
	if run.State == "CANCELLED" || run.State == "COMPLETED" {
		return payload, run, errRecordingMediaStale
	}
	return payload, run, nil
}

func (w *Worker) claimRecordingMedia(ctx context.Context, run db.RecordingMediaRun, table string, id uint, version int64, expectedState string) (string, error) {
	token := uuid.NewString()
	now := time.Now().UTC()
	err := w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		var current db.RecordingMediaRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND version = ?", run.ID, db.RecordingMediaVersion).First(&current).Error; err != nil {
			return err
		}
		if current.State == "CANCELLED" || current.State == "FAILED" || current.State == "COMPLETED" {
			return errRecordingMediaStale
		}
		result := tx.Table(table).Where("id = ? AND dispatch_version = ? AND state = ? AND lease_token = '' AND attempts < ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", id, version, expectedState, recordingMediaMaxAttempts, now).
			Updates(map[string]any{"state": map[bool]string{true: "MERGING", false: "RUNNING"}[table == "recording_media_runs"], "lease_token": token, "lease_expires_at": now.Add(recordingMediaLeaseDuration), "attempts": gorm.Expr("attempts + 1"), "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errRecordingMediaStale
		}
		return nil
	})
	return token, err
}

func (w *Worker) recordingMediaContext(ctx context.Context, run db.RecordingMediaRun, table string, id uint, token string) (context.Context, func(), error) {
	current := func(checkCtx context.Context) (bool, error) {
		var count int64
		database := w.DB
		if transaction, ok := checkCtx.Value(sessionPublicationTransactionKey{}).(*gorm.DB); ok {
			database = transaction
		}
		err := database.WithContext(checkCtx).Table(table).Where("id = ? AND lease_token = ? AND lease_expires_at > ?", id, token, time.Now().UTC()).Count(&count).Error
		return count == 1, err
	}
	guarded, done, err := w.withSessionWorkContext(withLimitedMediaThreads(ctx), run.SessionID, run.ProfileID, false, current)
	if err != nil {
		return guarded, done, err
	}
	workCtx, cancel := context.WithCancelCause(guarded)
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(recordingMediaHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-workCtx.Done():
				return
			case <-ticker.C:
				if err := checkSessionWork(workCtx); err != nil {
					cancel(err)
					return
				}
				result := w.DB.WithContext(workCtx).Table(table).Where("id = ? AND lease_token = ?", id, token).Updates(map[string]any{"lease_expires_at": time.Now().UTC().Add(recordingMediaLeaseDuration), "updated_at": time.Now().UTC()})
				if result.Error != nil {
					cancel(result.Error)
					return
				}
				if result.RowsAffected != 1 {
					cancel(errRecordingMediaStale)
					return
				}
			}
		}
	}()
	return workCtx, func() { close(stop); cancel(context.Canceled); done() }, nil
}

func recordingMediaIgnored(err error) bool {
	return errors.Is(err, errRecordingMediaStale) || errors.Is(err, db.ErrSessionWorkCancelled) || errors.Is(err, gorm.ErrRecordNotFound)
}

// Media attempts are owned by the durable row rather than Asynq retries.
func (w *Worker) failRecordingMedia(ctx context.Context, run db.RecordingMediaRun, table string, id uint, token, stage string, cause error) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var row struct{ Attempts int }
	if err := w.DB.WithContext(cleanup).Table(table).Select("attempts").Where("id = ? AND lease_token = ?", id, token).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	state := "PENDING"
	if table == "recording_media_runs" {
		state = "MERGING"
	}
	var next *time.Time
	if row.Attempts >= recordingMediaMaxAttempts || errors.Is(cause, asynq.SkipRetry) {
		state = "FAILED"
	} else {
		delay := 30 * time.Second
		if row.Attempts > 1 {
			delay = 2 * time.Minute
		}
		at := time.Now().UTC().Add(delay)
		next = &at
	}
	if errors.Is(db.SessionAutomaticWorkAllowed(cleanup, w.DB, run.ProfileID, run.SessionID), db.ErrSessionWorkCancelled) {
		state = "CANCELLED"
	}
	parentFailed := false
	err := w.DB.WithContext(cleanup).Transaction(func(tx *gorm.DB) error {
		result := tx.Table(table).Where("id = ? AND lease_token = ?", id, token).Updates(map[string]any{"state": state, "lease_token": "", "lease_expires_at": nil, "next_attempt_at": next, "dispatch_version": gorm.Expr("dispatch_version + 1"), "safe_error": "영상 처리 중 오류가 발생했습니다.", "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if state == "FAILED" || state == "CANCELLED" {
			parent := tx.Model(&db.RecordingMediaRun{}).Where("id = ? AND state NOT IN ?", run.ID, []string{"CANCELLED", "COMPLETED"})
			if state == "FAILED" && table != "recording_media_runs" {
				// A preparation failure must not close admission while recording.
				// Once sealed, the coordinator reports this failed dependency.
				parent = parent.Where("state <> ?", "RECORDING")
			}
			result := parent.Updates(map[string]any{"state": state, "failed_stage": stage, "safe_error": "영상 처리 중 오류가 발생했습니다.", "lease_token": "", "lease_expires_at": nil})
			parentFailed = state == "FAILED" && result.RowsAffected > 0
			return result.Error
		}
		return nil
	})
	if err == nil && parentFailed {
		err = w.persistRecordingMediaFailure(cleanup, run.ID)
	}
	w.logger.Warn("Recording media attempt failed", zap.Uint("run_id", run.ID), zap.String("stage", stage), zap.Int("attempt", row.Attempts), zap.Error(cause))
	return err
}

func (w *Worker) HandlePrepareRecordingChunkTask(ctx context.Context, task *asynq.Task) error {
	p, run, err := w.loadRecordingMediaTask(ctx, task)
	if err != nil {
		if recordingMediaIgnored(err) {
			return nil
		}
		return err
	}
	var prep db.ChunkMediaPreparation
	if err := w.DB.WithContext(ctx).Where("id = ? AND run_id = ?", p.PreparationID, run.ID).First(&prep).Error; err != nil {
		return err
	}
	token, err := w.claimRecordingMedia(ctx, run, "chunk_media_preparations", prep.ID, p.DispatchVersion, "PENDING")
	if err != nil {
		if recordingMediaIgnored(err) {
			return nil
		}
		return err
	}
	workCtx, done, err := w.recordingMediaContext(ctx, run, "chunk_media_preparations", prep.ID, token)
	if err != nil {
		return w.failRecordingMedia(ctx, run, "chunk_media_preparations", prep.ID, token, "chunk_preparation", err)
	}
	defer done()
	started := time.Now()
	scoped := w.scopedSessionWorker(workCtx)
	if err = scoped.prepareRecordingChunk(workCtx, run, prep, token); err != nil {
		return w.failRecordingMedia(workCtx, run, "chunk_media_preparations", prep.ID, token, "chunk_preparation", err)
	}
	var completed db.ChunkMediaPreparation
	if w.DB.WithContext(ctx).First(&completed, prep.ID).Error == nil {
		w.recordStageMetrics(run.SessionID, run.ProfileID, "media:prepare_chunk", prep.RecipeVersion, 0, 0, completed.OutputBytes, time.Since(started))
	}
	w.logger.Info("Recording chunk preparation completed", zap.Uint("run_id", run.ID), zap.Uint("preparation_id", prep.ID), zap.String("profile_key", prep.ProfileKey), zap.Duration("elapsed", time.Since(started)))
	return DispatchRecordingMedia(workCtx, w.DB, w.QueueClient, run.ID)
}

func (w *Worker) prepareRecordingChunk(ctx context.Context, run db.RecordingMediaRun, prep db.ChunkMediaPreparation, token string) error {
	var chunk db.RecordingChunk
	if err := w.DB.WithContext(ctx).Where("id = ? AND run_id = ? AND source_generation = ?", prep.ChunkID, run.ID, prep.SourceGeneration).First(&chunk).Error; err != nil {
		return err
	}
	tmp, err := createTempDir("recording-chunk")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	source := filepath.Join(tmp, "source.mp4")
	if err := w.downloadPinnedMedia(ctx, chunk.SourceURI, chunk.SourceGeneration, source); err != nil {
		return err
	}
	var profile chunkStreamProfile
	var timeline videoTimeline
	trim := chunk.LeadingTrimSecs
	if chunk.ProbeReady {
		if err := json.Unmarshal(chunk.SourceProfile, &profile); err != nil {
			return err
		}
		if err := json.Unmarshal(chunk.SourceTimeline, &timeline); err != nil {
			return err
		}
	} else {
		profile, err = probeChunkStreamProfile(ctx, source)
		if err != nil {
			return err
		}
		timeline, err = probeVideoTimeline(ctx, source)
		if err != nil {
			return err
		}
		trim, err = probeLeadingBlackTrimSecs(ctx, source, profile, timeline)
		if err != nil {
			return err
		}
		if err := w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
			return tx.Model(&db.RecordingChunk{}).Where("id = ? AND source_generation = ?", chunk.ID, prep.SourceGeneration).Updates(map[string]any{"source_profile": recordingJSON(profile), "source_timeline": recordingJSON(timeline), "leading_trim_secs": trim, "probe_ready": true}).Error
		}); err != nil {
			return err
		}
	}
	target := recordingConcatProfile{evenDimension(profile.DisplayWidth), evenDimension(profile.DisplayHeight), profile.HasAudio}
	if prep.ProfileKey != "native" {
		if err := json.Unmarshal(prep.ResolvedProfile, &target); err != nil {
			return err
		}
	}
	prepared, err := prepareChunkForConcatWithEvidence(ctx, w.logger, tmp, 0, source, profile, target.Width, target.Height, target.IncludeAudio, timeline, trim)
	if err != nil {
		return err
	}
	signature, err := encodeFrameSignature(prepared.signature)
	if err != nil {
		return err
	}
	object := fmt.Sprintf("videos/%d/%s/derived/chunks/%d/%s.mp4", run.ProfileID, run.SessionID, prep.ID, token)
	defer w.cleanupRecordingAttemptObject(ctx, object, "chunk_media_preparations", prep.ID, "output_object")
	if _, err := w.StorageClient.UploadFromFile(ctx, prepared.normPath, object); err != nil {
		return err
	}
	attrs, err := w.StorageClient.ObjectAttrs(ctx, object)
	if err != nil {
		return err
	}
	if attrs.Generation <= 0 || attrs.Size <= 0 {
		return errors.New("prepared chunk metadata unavailable")
	}
	return w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		result := tx.Model(&db.ChunkMediaPreparation{}).Where("id = ? AND state = 'RUNNING' AND lease_token = ?", prep.ID, token).Updates(map[string]any{"state": "READY", "resolved_profile": recordingJSON(target), "timeline": recordingJSON(videoTimeline{Start: prepared.signature.Start, End: prepared.signature.End, Tick: 1.0 / 30000, FrameCount: prepared.signature.FrameCount}), "signature": signature, "output_object": object, "output_generation": attrs.Generation, "output_bytes": attrs.Size, "lease_token": "", "lease_expires_at": nil, "next_attempt_at": nil, "safe_error": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errRecordingMediaStale
		}
		return nil
	})
}

func (w *Worker) HandleCoordinateRecordingMediaTask(ctx context.Context, task *asynq.Task) error {
	_, run, err := w.loadRecordingMediaTask(ctx, task)
	if err != nil {
		if recordingMediaIgnored(err) {
			return nil
		}
		return err
	}
	guarded, done, err := w.withSessionWorkContext(ctx, run.SessionID, run.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(guarded, err)
	}
	defer done()
	scoped := w.scopedSessionWorker(guarded)
	if run.State == "WAITING_PREPARATION" {
		err = scoped.coordinateRecordingPreparation(guarded, run)
	} else if run.MergedGeneration > 0 {
		err = scoped.coordinateRecordingAnalysis(guarded, run)
	}
	if err != nil {
		return sessionWorkResult(guarded, err)
	}
	if err := scoped.persistRecordingMediaFailure(guarded, run.ID); err != nil {
		return sessionWorkResult(guarded, err)
	}
	return sessionWorkResult(guarded, DispatchRecordingMedia(guarded, w.DB, w.QueueClient, run.ID))
}

func recordingManifest(run db.RecordingMediaRun) ([]db.RecordingMediaManifestEntry, error) {
	var manifest []db.RecordingMediaManifestEntry
	if err := json.Unmarshal(run.Manifest, &manifest); err != nil {
		return nil, err
	}
	if len(manifest) == 0 {
		return nil, errors.New("sealed recording manifest is empty")
	}
	return manifest, nil
}

func (w *Worker) coordinateRecordingPreparation(ctx context.Context, run db.RecordingMediaRun) error {
	return w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", run.ID).First(&run).Error; err != nil {
			return err
		}
		if run.State != "WAITING_PREPARATION" {
			return nil
		}
		manifest, err := recordingManifest(run)
		if err != nil {
			return err
		}
		var failedNative int64
		if err := tx.Model(&db.ChunkMediaPreparation{}).Where("run_id = ? AND recipe_version = ? AND profile_key = ? AND state = ?", run.ID, recordingConcatRecipe, "native", "FAILED").Count(&failedNative).Error; err != nil {
			return err
		}
		if failedNative > 0 {
			return tx.Model(&run).Updates(map[string]any{"state": "FAILED", "failed_stage": "chunk_preparation", "safe_error": "청크 영상을 준비하지 못했습니다."}).Error
		}
		profiles := make([]chunkStreamProfile, len(manifest))
		for i, entry := range manifest {
			var chunk db.RecordingChunk
			if err := tx.Where("id = ? AND run_id = ? AND source_generation = ?", entry.ChunkID, run.ID, entry.SourceGeneration).First(&chunk).Error; err != nil {
				return err
			}
			if !chunk.ProbeReady {
				return nil
			}
			if err := json.Unmarshal(chunk.SourceProfile, &profiles[i]); err != nil {
				return err
			}
		}
		width, height, audio := concatPreparationSettings(profiles)
		target := recordingConcatProfile{width, height, audio}
		key := recordingProfileKey(target)
		ready := true
		for _, entry := range manifest {
			var native db.ChunkMediaPreparation
			if err := tx.Where("run_id = ? AND chunk_id = ? AND source_generation = ? AND recipe_version = ? AND profile_key = ?", run.ID, entry.ChunkID, entry.SourceGeneration, recordingConcatRecipe, "native").First(&native).Error; err != nil {
				return err
			}
			var nativeProfile recordingConcatProfile
			_ = json.Unmarshal(native.ResolvedProfile, &nativeProfile)
			if native.State == "FAILED" {
				return tx.Model(&run).Updates(map[string]any{"state": "FAILED", "failed_stage": "chunk_preparation", "safe_error": "청크 영상을 준비하지 못했습니다."}).Error
			}
			if native.State == "READY" && nativeProfile == target {
				continue
			}
			if native.State == "PENDING" || native.State == "RUNNING" {
				ready = false
				continue
			}
			var prep db.ChunkMediaPreparation
			err := tx.Where("run_id = ? AND chunk_id = ? AND source_generation = ? AND recipe_version = ? AND profile_key = ?", run.ID, entry.ChunkID, entry.SourceGeneration, recordingConcatRecipe, key).First(&prep).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				now := time.Now().UTC()
				prep = db.ChunkMediaPreparation{RunID: run.ID, ChunkID: entry.ChunkID, SourceGeneration: entry.SourceGeneration, RecipeVersion: recordingConcatRecipe, ProfileKey: key, ResolvedProfile: recordingJSON(target), State: "PENDING", NextAttemptAt: &now}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&prep).Error; err != nil {
					return err
				}
				ready = false
				continue
			}
			if err != nil {
				return err
			}
			if prep.State == "FAILED" {
				return tx.Model(&run).Updates(map[string]any{"state": "FAILED", "failed_stage": "chunk_preparation", "safe_error": "청크 영상을 준비하지 못했습니다."}).Error
			}
			if prep.State != "READY" {
				ready = false
			}
		}
		updates := map[string]any{"output_profile": recordingJSON(target)}
		if ready {
			now := time.Now().UTC()
			updates["state"] = "MERGING"
			updates["attempts"] = 0
			updates["dispatch_version"] = gorm.Expr("dispatch_version + 1")
			updates["next_attempt_at"] = &now
		}
		return tx.Model(&run).Updates(updates).Error
	})
}

func (w *Worker) HandleMergeRecordingMediaTask(ctx context.Context, task *asynq.Task) error {
	p, run, err := w.loadRecordingMediaTask(ctx, task)
	if err != nil {
		if recordingMediaIgnored(err) {
			return nil
		}
		return err
	}
	token, err := w.claimRecordingMedia(ctx, run, "recording_media_runs", run.ID, p.DispatchVersion, "MERGING")
	if err != nil {
		if recordingMediaIgnored(err) {
			return nil
		}
		return err
	}
	run.LeaseToken = token
	workCtx, done, err := w.recordingMediaContext(ctx, run, "recording_media_runs", run.ID, token)
	if err != nil {
		return w.failRecordingMedia(ctx, run, "recording_media_runs", run.ID, token, "merge", err)
	}
	defer done()
	started := time.Now()
	scoped := w.scopedSessionWorker(workCtx)
	if err := scoped.mergeRecordingMedia(workCtx, run); err != nil {
		return w.failRecordingMedia(workCtx, run, "recording_media_runs", run.ID, token, "merge", err)
	}
	var completed db.RecordingMediaRun
	var uploadedBytes int64
	if w.DB.WithContext(ctx).First(&completed, run.ID).Error == nil && run.MergedStagingGeneration == 0 {
		uploadedBytes = completed.MergedBytes
	}
	w.recordStageMetrics(run.SessionID, run.ProfileID, "media:merge", recordingConcatRecipe, 0, 0, uploadedBytes, time.Since(started))
	w.logger.Info("Recording merge completed", zap.Uint("run_id", run.ID), zap.Bool("reused_staging", run.MergedStagingGeneration > 0), zap.Duration("elapsed", time.Since(started)))
	return DispatchRecordingMedia(workCtx, w.DB, w.QueueClient, run.ID)
}

func (w *Worker) mergeRecordingMedia(ctx context.Context, run db.RecordingMediaRun) error {
	if run.MergedStagingGeneration <= 0 {
		manifest, err := recordingManifest(run)
		if err != nil {
			return err
		}
		var target recordingConcatProfile
		if err := json.Unmarshal(run.OutputProfile, &target); err != nil {
			return err
		}
		key := recordingProfileKey(target)
		tmp, err := createTempDir("recording-merge")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		entries := make([]string, 0, len(manifest)*2)
		signatures := make([]videoFrameSignature, 0, len(manifest))
		intervals := make([]concatMediaInterval, 0, len(manifest))
		cumulative := 0.0
		for i, entry := range manifest {
			var chunk db.RecordingChunk
			if err := w.DB.WithContext(ctx).Where("id = ? AND run_id = ? AND source_generation = ?", entry.ChunkID, run.ID, entry.SourceGeneration).First(&chunk).Error; err != nil {
				return err
			}
			var choices []db.ChunkMediaPreparation
			if err := w.DB.WithContext(ctx).Where("run_id = ? AND chunk_id = ? AND source_generation = ? AND recipe_version = ? AND state = ? AND profile_key IN ?", run.ID, chunk.ID, entry.SourceGeneration, recordingConcatRecipe, "READY", []string{"native", key}).Find(&choices).Error; err != nil {
				return err
			}
			var selected *db.ChunkMediaPreparation
			for j := range choices {
				var actual recordingConcatProfile
				if json.Unmarshal(choices[j].ResolvedProfile, &actual) == nil && actual == target {
					selected = &choices[j]
					break
				}
			}
			if selected == nil {
				return errors.New("prepared recording chunk is unavailable")
			}
			signature, err := decodeFrameSignature(selected.Signature)
			if err != nil {
				return err
			}
			path := filepath.Join(tmp, fmt.Sprintf("chunk_%05d.mp4", i))
			if err := w.downloadPinnedMedia(ctx, "gs://"+w.BucketName+"/"+selected.OutputObject, selected.OutputGeneration, path); err != nil {
				return err
			}
			duration := signature.End - signature.Start
			if duration <= 0 {
				return errors.New("invalid prepared chunk duration")
			}
			entries = append(entries, fmt.Sprintf("file '%s'", strings.ReplaceAll(path, "'", "'\\''")), fmt.Sprintf("duration %.9f", duration))
			signatures = append(signatures, signature)
			intervals = append(intervals, concatMediaInterval{FilePath: chunk.SourceURI, Start: cumulative, End: cumulative + duration})
			cumulative += duration
		}
		list := filepath.Join(tmp, "concat.txt")
		if err := os.WriteFile(list, []byte(strings.Join(entries, "\n")), 0o600); err != nil {
			return err
		}
		merged := filepath.Join(tmp, "merged.mp4")
		if err := runFFmpegStreamCopyConcat(ctx, w.logger, list, merged, cumulative, target.IncludeAudio); err != nil {
			return err
		}
		if err := verifyVideoFrameSequence(ctx, merged, signatures); err != nil {
			return err
		}
		if err := verifyMergedDurationMatchesIntervals(ctx, merged, intervals); err != nil {
			return err
		}
		object := fmt.Sprintf("videos/%d/%s/derived/merge/%d/%s.mp4", run.ProfileID, run.SessionID, run.ID, run.LeaseToken)
		defer w.cleanupRecordingAttemptObject(ctx, object, "recording_media_runs", run.ID, "merged_staging_object")
		if _, err := w.StorageClient.UploadFromFile(ctx, merged, object); err != nil {
			return err
		}
		attrs, err := w.StorageClient.ObjectAttrs(ctx, object)
		if err != nil {
			return err
		}
		if attrs.Generation <= 0 || attrs.Size <= 0 {
			return errors.New("merged staging metadata unavailable")
		}
		run.MergedStagingObject, run.MergedStagingGeneration = object, attrs.Generation
		run.Intervals = recordingJSON(intervals)
		if err := w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
			result := tx.Model(&db.RecordingMediaRun{}).Where("id = ? AND state = ? AND lease_token = ?", run.ID, "MERGING", run.LeaseToken).Updates(map[string]any{"merged_staging_object": object, "merged_staging_generation": attrs.Generation, "intervals": run.Intervals})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errRecordingMediaStale
			}
			return nil
		}); err != nil {
			return err
		}
	}
	attrs, err := w.publishRecordingMerged(ctx, run, run.MergedStagingObject, run.MergedStagingGeneration)
	if err != nil {
		return err
	}
	return w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		result := tx.Model(&db.RecordingMediaRun{}).Where("id = ? AND state = ? AND lease_token = ?", run.ID, "MERGING", run.LeaseToken).Updates(map[string]any{"state": "MERGED", "merged_object": fmt.Sprintf("videos/%d/%s/merged.mp4", run.ProfileID, run.SessionID), "merged_generation": attrs.Generation, "merged_bytes": attrs.Size, "lease_token": "", "lease_expires_at": nil, "next_attempt_at": nil, "safe_error": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errRecordingMediaStale
		}
		return nil
	})
}

func (w *Worker) recordingChunkAITerminal(ctx context.Context, run db.RecordingMediaRun, payload VideoAnalysisPayload) (bool, error) {
	manifest, err := recordingManifest(run)
	if err != nil {
		return false, err
	}
	uris := make([]string, 0, len(manifest))
	for _, entry := range manifest {
		var chunk db.RecordingChunk
		if err := w.DB.WithContext(ctx).Where("id = ? AND run_id = ?", entry.ChunkID, run.ID).First(&chunk).Error; err != nil {
			return false, err
		}
		uris = append(uris, chunk.SourceURI)
	}
	var terminal []db.ChunkAnalysisResult
	if err := w.DB.WithContext(ctx).Where("profile_id = ? AND session_id = ? AND file_path IN ? AND status IN ?", run.ProfileID, run.SessionID, uris, []string{"COMPLETED", "FAILED"}).Find(&terminal).Error; err != nil {
		return false, err
	}
	seen := map[string]bool{}
	for _, row := range terminal {
		seen[row.FilePath] = true
	}
	var pending []string
	for _, uri := range uris {
		if !seen[uri] {
			pending = append(pending, uri)
		}
	}
	if len(pending) == 0 {
		return true, nil
	}
	if payload.MergeRequestedAt.IsZero() {
		payload.MergeRequestedAt = run.CreatedAt
	}
	if err := w.resolvePendingChunks(ctx, payload, pending); err != nil {
		if strings.HasPrefix(err.Error(), "waiting for chunk analysis to complete:") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (w *Worker) coordinateRecordingAnalysis(ctx context.Context, run db.RecordingMediaRun) error {
	var payload VideoAnalysisPayload
	if err := json.Unmarshal(run.Payload, &payload); err != nil {
		return err
	}
	payload.SessionID, payload.ProfileID, payload.MediaRunID = run.SessionID, run.ProfileID, run.ID
	var intervals []concatMediaInterval
	if err := json.Unmarshal(run.Intervals, &intervals); err != nil {
		return err
	}
	if len(intervals) == 0 {
		return errors.New("merged recording intervals unavailable")
	}
	uri := "gs://" + w.BucketName + "/" + run.MergedObject
	if intervals[len(intervals)-1].End > maxAnalysisVideoDuration.Seconds() {
		// Record the duration fence before inspecting AI rows: late chunk results
		// must not schedule a paid activity review for this merged recording.
		if run.State != "FAILED" || run.FailedStage != "duration" {
			if err := w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
				return tx.Model(&db.RecordingMediaRun{}).Where("id = ?", run.ID).Updates(map[string]any{"state": "FAILED", "failed_stage": "duration", "safe_error": videoTooLongUserMessage, "activity_dispatched": false}).Error
			}); err != nil {
				return err
			}
		}
		if err := w.persistRecordingMediaFailure(ctx, run.ID); err != nil {
			return err
		}
		terminal, err := w.recordingChunkAITerminal(ctx, run, payload)
		if err != nil {
			return err
		}
		if !terminal || run.ActivityDispatched {
			return nil
		}
		if err := w.persistConcatMediaIntervals(ctx, run.SessionID, run.ProfileID, intervals); err != nil {
			return err
		}
		if err := w.skipOverlongActivityReview(ctx, payload, uri); err != nil {
			return err
		}
		return w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
			return tx.Model(&db.RecordingMediaRun{}).Where("id = ? AND state = ? AND failed_stage = ?", run.ID, "FAILED", "duration").Update("activity_dispatched", true).Error
		})
	}
	terminal, err := w.recordingChunkAITerminal(ctx, run, payload)
	if err != nil {
		return err
	}
	if terminal && !run.ActivityDispatched {
		if err := w.persistConcatMediaIntervals(ctx, run.SessionID, run.ProfileID, intervals); err != nil {
			return err
		}
		if err := w.enqueueActivityReview(ctx, payload, uri); err != nil {
			return err
		}
		if err := w.DB.WithContext(ctx).Model(&db.RecordingMediaRun{}).Where("id = ?", run.ID).Update("activity_dispatched", true).Error; err != nil {
			return err
		}
	}
	if run.State == "MERGED" {
		return w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
			var current db.RecordingMediaRun
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", run.ID).First(&current).Error; err != nil {
				return err
			}
			if current.State != "MERGED" {
				return nil
			}
			now := time.Now().UTC()
			prep := db.AnalysisVideoPreparation{RunID: run.ID, SourceObject: run.MergedObject, SourceGeneration: run.MergedGeneration, PresetVersion: recordingAnalysisPreset, State: "PENDING", NextAttemptAt: &now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&prep).Error; err != nil {
				return err
			}
			return tx.Model(&current).Update("state", "PREPARING_ANALYSIS").Error
		})
	}
	if run.State != "READY_FOR_ANALYSIS" || !terminal || run.AnalysisDispatched {
		return nil
	}
	var prep db.AnalysisVideoPreparation
	if err := w.DB.WithContext(ctx).Where("run_id = ? AND source_generation = ? AND preset_version = ? AND state = ?", run.ID, run.MergedGeneration, recordingAnalysisPreset, "READY").First(&prep).Error; err != nil {
		return err
	}
	payload.FilePath = "gs://" + w.BucketName + "/" + prep.OutputObject
	payload.SourceGeneration = prep.OutputGeneration
	payload.AnalysisPreparationID = prep.ID
	payload.MediaDispatchVersion = run.DispatchVersion
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		_, err := recordingBaseQueue(w.QueueClient).Enqueue(asynq.NewTask(TypeVideoAnalysis, data, asynq.Timeout(videoAnalysisTaskTimeout), asynq.TaskID(recordingAnalysisTaskID(run.ID, prep.ID, run.DispatchVersion)), asynq.Retention(24*time.Hour)))
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
			return err
		}
		return tx.Model(&db.RecordingMediaRun{}).Where("id = ? AND state = ?", run.ID, "READY_FOR_ANALYSIS").Update("analysis_dispatched", true).Error
	})
}

func (w *Worker) HandlePrepareAnalysisVideoTask(ctx context.Context, task *asynq.Task) error {
	p, run, err := w.loadRecordingMediaTask(ctx, task)
	if err != nil {
		if recordingMediaIgnored(err) {
			return nil
		}
		return err
	}
	var prep db.AnalysisVideoPreparation
	if err := w.DB.WithContext(ctx).Where("id = ? AND run_id = ?", p.PreparationID, run.ID).First(&prep).Error; err != nil {
		return err
	}
	token, err := w.claimRecordingMedia(ctx, run, "analysis_video_preparations", prep.ID, p.DispatchVersion, "PENDING")
	if err != nil {
		if recordingMediaIgnored(err) {
			return nil
		}
		return err
	}
	workCtx, done, err := w.recordingMediaContext(ctx, run, "analysis_video_preparations", prep.ID, token)
	if err != nil {
		return w.failRecordingMedia(ctx, run, "analysis_video_preparations", prep.ID, token, "analysis_preparation", err)
	}
	defer done()
	started := time.Now()
	if err := w.scopedSessionWorker(workCtx).prepareRecordingAnalysisVideo(workCtx, run, prep, token); err != nil {
		return w.failRecordingMedia(workCtx, run, "analysis_video_preparations", prep.ID, token, "analysis_preparation", err)
	}
	var completed db.AnalysisVideoPreparation
	var uploadedBytes int64
	if w.DB.WithContext(ctx).First(&completed, prep.ID).Error == nil && completed.OutputObject != prep.SourceObject {
		uploadedBytes = completed.OutputBytes
	}
	w.recordStageMetrics(run.SessionID, run.ProfileID, "media:prepare_analysis", prep.PresetVersion, 0, 0, uploadedBytes, time.Since(started))
	w.logger.Info("Recording analysis preparation completed", zap.Uint("run_id", run.ID), zap.Uint("preparation_id", prep.ID), zap.Bool("reused_merged", completed.OutputObject == prep.SourceObject), zap.Duration("elapsed", time.Since(started)))
	return DispatchRecordingMedia(workCtx, w.DB, w.QueueClient, run.ID)
}

func (w *Worker) prepareRecordingAnalysisVideo(ctx context.Context, run db.RecordingMediaRun, prep db.AnalysisVideoPreparation, token string) error {
	if prep.SourceObject != run.MergedObject || prep.SourceGeneration != run.MergedGeneration || prep.PresetVersion != recordingAnalysisPreset {
		return errRecordingMediaStale
	}
	object, generation, size := prep.SourceObject, prep.SourceGeneration, run.MergedBytes
	if size > maxAnalysisVideoSizeBytes {
		tmp, err := createTempDir("recording-analysis")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		input, output := filepath.Join(tmp, "input.mp4"), filepath.Join(tmp, "analysis.mp4")
		if err := w.downloadPinnedMedia(ctx, "gs://"+w.BucketName+"/"+prep.SourceObject, prep.SourceGeneration, input); err != nil {
			return err
		}
		encodeErr := runFFmpegAnalysisEncode(ctx, w.logger, input, output)
		if err := ctx.Err(); err != nil {
			return err
		}
		if encodeErr != nil {
			if size > maxAnalysisFileSizeBytes {
				return encodeErr
			}
		} else {
			info, err := os.Stat(output)
			if err != nil {
				return err
			}
			if info.Size() <= 0 || info.Size() > maxAnalysisFileSizeBytes {
				return errors.New("analysis video exceeds hard size limit")
			}
			var intervals []concatMediaInterval
			if err := json.Unmarshal(run.Intervals, &intervals); err != nil {
				return err
			}
			if len(intervals) == 0 {
				return errors.New("merged duration unavailable")
			}
			if err := verifyMediaEndpoints(ctx, output, intervals[len(intervals)-1].End, 2.0/30+1.0/30000); err != nil {
				return err
			}
			object = fmt.Sprintf("videos/%d/%s/derived/analysis/%d/%s.mp4", run.ProfileID, run.SessionID, prep.ID, token)
			defer w.cleanupRecordingAttemptObject(ctx, object, "analysis_video_preparations", prep.ID, "output_object")
			if _, err := w.StorageClient.UploadFromFile(ctx, output, object); err != nil {
				return err
			}
			attrs, err := w.StorageClient.ObjectAttrs(ctx, object)
			if err != nil {
				return err
			}
			generation, size = attrs.Generation, attrs.Size
		}
	}
	if generation <= 0 || size <= 0 || size > maxAnalysisFileSizeBytes {
		return errors.New("analysis input metadata is invalid")
	}
	return w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		var current db.RecordingMediaRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", run.ID).First(&current).Error; err != nil {
			return err
		}
		if current.State != "PREPARING_ANALYSIS" || current.MergedGeneration != prep.SourceGeneration {
			return errRecordingMediaStale
		}
		result := tx.Model(&db.AnalysisVideoPreparation{}).Where("id = ? AND state = ? AND lease_token = ?", prep.ID, "RUNNING", token).Updates(map[string]any{"state": "READY", "output_object": object, "output_generation": generation, "output_bytes": size, "lease_token": "", "lease_expires_at": nil, "next_attempt_at": nil, "safe_error": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errRecordingMediaStale
		}
		return tx.Model(&current).Update("state", "READY_FOR_ANALYSIS").Error
	})
}

// RunRecordingMediaRecovery only visits explicitly admitted version-1 runs.
func (w *Worker) RunRecordingMediaRecovery(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		w.recoverRecordingMedia(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) recoverRecordingMedia(ctx context.Context) {
	var runs []db.RecordingMediaRun
	if err := w.DB.WithContext(ctx).Where(`version = ? AND (
		state NOT IN ?
		OR (state = 'RECORDING' AND (
			EXISTS (SELECT 1 FROM recording_chunks c WHERE c.run_id = recording_media_runs.id AND c.ai_dispatched = FALSE)
			OR EXISTS (SELECT 1 FROM chunk_media_preparations p WHERE p.run_id = recording_media_runs.id AND p.state IN ('PENDING','RUNNING'))
		))
		OR (state = 'FAILED' AND merged_generation > 0 AND activity_dispatched = FALSE)
		OR (state = 'FAILED' AND jsonb_array_length(manifest) > 0 AND NOT EXISTS (
			SELECT 1 FROM analysis_results a WHERE a.profile_id = recording_media_runs.profile_id AND a.session_id = recording_media_runs.session_id AND a.status IN ('FAILED','COMPLETED')
		))
	)`, db.RecordingMediaVersion, []string{"LEGACY", "RECORDING", "COMPLETED", "CANCELLED", "FAILED"}).Order("id ASC").Find(&runs).Error; err != nil {
		w.logger.Warn("Recording media recovery query failed", zap.Error(err))
		return
	}
	for _, run := range runs {
		if ctx.Err() != nil {
			return
		}
		if err := db.SessionAutomaticWorkAllowed(ctx, w.DB, run.ProfileID, run.SessionID); err != nil {
			if errors.Is(err, db.ErrSessionWorkCancelled) {
				_ = w.DB.WithContext(ctx).Model(&db.RecordingMediaRun{}).Where("id = ? AND version = ?", run.ID, db.RecordingMediaVersion).Updates(map[string]any{"state": "CANCELLED", "lease_token": "", "lease_expires_at": nil}).Error
			}
			continue
		}
		w.recoverRecordingPendingDeliveries(ctx, run)
		if run.State == "FAILED" {
			if err := w.persistRecordingMediaFailure(ctx, run.ID); err != nil {
				w.logger.Warn("Recording failure history publication deferred", zap.Uint("run_id", run.ID), zap.Error(err))
			}
		}
		for _, table := range []string{"chunk_media_preparations", "analysis_video_preparations", "recording_media_runs"} {
			var expired []struct {
				ID         uint
				LeaseToken string
			}
			query := w.DB.WithContext(ctx).Table(table).Select("id, lease_token").Where("lease_token <> '' AND lease_expires_at <= ?", time.Now().UTC())
			if table == "recording_media_runs" {
				query = query.Where("id = ? AND state = ?", run.ID, "MERGING")
			} else {
				query = query.Where("run_id = ? AND state = ?", run.ID, "RUNNING")
			}
			if err := query.Find(&expired).Error; err != nil {
				continue
			}
			for _, row := range expired {
				stage := "chunk_preparation"
				if table == "analysis_video_preparations" {
					stage = "analysis_preparation"
				} else if table == "recording_media_runs" {
					stage = "merge"
				}
				_ = w.failRecordingMedia(ctx, run, table, row.ID, row.LeaseToken, stage, errors.New("media attempt lease expired"))
			}
		}
		if run.State == "ANALYZING" || (run.State == "READY_FOR_ANALYSIS" && run.AnalysisDispatched) {
			if err := w.recoverRecordingVideoAnalysis(ctx, run); err != nil {
				w.logger.Warn("Recording analysis recovery failed", zap.Uint("run_id", run.ID), zap.Error(err))
			}
			continue
		}
		if err := DispatchRecordingMedia(ctx, w.DB, w.QueueClient, run.ID); err != nil && !recordingMediaIgnored(err) {
			w.logger.Warn("Recording media dispatch deferred", zap.Uint("run_id", run.ID), zap.Error(err))
		}
	}
}

// A delivery can die before claiming its row (for example, during a database
// outage). Its archived TaskID must not strand a still-pending durable outbox.
func (w *Worker) recoverRecordingPendingDeliveries(ctx context.Context, run db.RecordingMediaRun) {
	if w.QueueInspector == nil || run.State == "FAILED" {
		return
	}
	for _, spec := range []struct{ table, kind, queue string }{
		{"chunk_media_preparations", TypePrepareRecordingChunk, RecordingChunkQueueName},
		{"analysis_video_preparations", TypePrepareAnalysisVideo, RecordingSessionQueueName},
		{"recording_media_runs", TypeMergeRecordingMedia, RecordingSessionQueueName},
	} {
		var rows []struct {
			ID              uint
			DispatchVersion int64
		}
		query := w.DB.WithContext(ctx).Table(spec.table).Select("id, dispatch_version").Where("lease_token = ''")
		if spec.table == "recording_media_runs" {
			query = query.Where("id = ? AND state = ?", run.ID, "MERGING")
		} else {
			query = query.Where("run_id = ? AND state = ?", run.ID, "PENDING")
		}
		if err := query.Find(&rows).Error; err != nil {
			continue
		}
		for _, row := range rows {
			payload := recordingMediaTask{RunID: run.ID, PreparationID: row.ID, DispatchVersion: row.DispatchVersion}
			if spec.table == "recording_media_runs" {
				payload.PreparationID = 0
			}
			info, err := w.QueueInspector.GetTaskInfo(spec.queue, recordingMediaTaskID(spec.kind, payload))
			if err != nil || (info.State != asynq.TaskStateArchived && info.State != asynq.TaskStateCompleted) {
				continue
			}
			_ = w.DB.WithContext(ctx).Table(spec.table).Where("id = ? AND dispatch_version = ? AND lease_token = ''", row.ID, row.DispatchVersion).Updates(map[string]any{"dispatch_version": gorm.Expr("dispatch_version + 1"), "next_attempt_at": time.Now().UTC()}).Error
		}
	}
}
