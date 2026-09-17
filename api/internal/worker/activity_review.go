package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const TypeActivityReview = "activity:review"

type activityReviewPayload struct {
	SessionID     string `json:"session_id"`
	ProfileID     uint   `json:"profile_id"`
	SourceVersion string `json:"source_version"`
	Object        string `json:"object"`
	Generation    int64  `json:"generation"`
}

func (w *Worker) activityRows(ctx context.Context, profileID uint, sessionID string) ([]db.ChunkAnalysisResult, error) {
	var rows []db.ChunkAnalysisResult
	err := w.DB.WithContext(ctx).Where("profile_id = ? AND session_id = ?", profileID, sessionID).Find(&rows).Error
	return activity.CanonicalChunks(rows), err
}

// Save under the session lock, compare the original source snapshot, then rebuild
// totals. A late job cannot replace a summary for different source observations.
func (w *Worker) saveActivityReview(ctx context.Context, p activityReviewPayload, summary *activity.Summary) error {
	return w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session db.Session
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).First(&session).Error; err != nil {
			return err
		}
		var rows []db.ChunkAnalysisResult
		if err := tx.Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).Find(&rows).Error; err != nil {
			return err
		}
		if activity.SourceVersion(rows) != p.SourceVersion {
			return errors.New("activity source changed")
		}
		var current activity.Summary
		_ = json.Unmarshal(session.ActivitySummary, &current)
		currentGeneration, _ := strconv.ParseInt(current.MediaGeneration, 10, 64)
		if current.SourceVersion == p.SourceVersion && currentGeneration > p.Generation {
			return errors.New("activity media superseded")
		}
		summary.MediaGeneration = strconv.FormatInt(p.Generation, 10)
		result := activity.Build(rows, summary)
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&session).Update("activity_summary", db.JSONDocument(encoded)).Error
	})
}

func (w *Worker) enqueueActivityReview(ctx context.Context, p VideoAnalysisPayload, uri string) error {
	if !w.ActivityCountingEnabled {
		return nil
	}
	rows, err := w.activityRows(ctx, p.ProfileID, p.SessionID)
	if err != nil {
		return err
	}
	summary := activity.Build(rows, nil)
	if !summary.Available {
		return nil
	} // old/uploaded recordings are not opted in
	object := fmt.Sprintf("videos/%d/%s/merged.mp4", p.ProfileID, p.SessionID)
	if uri != "gs://"+w.BucketName+"/"+object {
		return errors.New("unexpected activity media source")
	}
	attrs, err := w.StorageClient.ObjectAttrs(ctx, object)
	if err != nil {
		return err
	}
	payload := activityReviewPayload{p.SessionID, p.ProfileID, summary.SourceVersion, object, attrs.Generation}
	if payload.Generation <= 0 {
		return errors.New("activity media generation unavailable")
	}
	// Retain a completed or resumable review when merge delivery repeats.
	var session db.Session
	if err = w.DB.WithContext(ctx).Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).First(&session).Error; err != nil {
		return err
	}
	var previous activity.Summary
	_ = json.Unmarshal(session.ActivitySummary, &previous)
	if previous.SourceVersion == summary.SourceVersion && previous.MediaGeneration == strconv.FormatInt(payload.Generation, 10) {
		if previous.ReviewState == "completed" || previous.ReviewState == "partial" || previous.ReviewState == "disabled" {
			return nil
		}
		summary = activity.Build(rows, &previous)
	}
	summary.ReviewState = "queued"
	if err = w.saveActivityReview(ctx, payload, &summary); err != nil {
		return err
	}
	data, _ := json.Marshal(payload)
	task := asynq.NewTask(TypeActivityReview, data, asynq.MaxRetry(2), asynq.Timeout(20*time.Minute), asynq.Retention(24*time.Hour))
	_, err = w.QueueClient.Enqueue(task, asynq.TaskID(fmt.Sprintf("activity:%d:%s:%s:%d", p.ProfileID, p.SessionID, payload.SourceVersion, payload.Generation)))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		if previous.SourceVersion == summary.SourceVersion && previous.MediaGeneration == strconv.FormatInt(payload.Generation, 10) && previous.ReviewState == "failed" {
			summary.ReviewState = "failed"
			return w.saveActivityReview(ctx, payload, &summary)
		}
		return nil
	}
	if err != nil {
		summary.ReviewState = "failed"
		_ = w.saveActivityReview(ctx, payload, &summary)
	}
	return err
}

func reviewBounds(rows []db.ChunkAnalysisResult, i int) (float64, float64, bool) {
	row := rows[i]
	if !activity.Timed(row) || !activity.MediaTimed(row) {
		return 0, 0, false
	}
	start, end := *row.MediaStartSecs, *row.MediaEndSecs
	// Distinct files with overlapping source/media clocks are not trustworthy.
	for j, other := range rows {
		if i == j {
			continue
		}
		if activity.Timed(other) && *row.StartSecs < *other.EndSecs && *row.EndSecs > *other.StartSecs {
			return 0, 0, false
		}
		if activity.MediaTimed(other) && start < *other.MediaEndSecs && end > *other.MediaStartSecs {
			return 0, 0, false
		}
	}
	if i > 0 && activity.Adjacent(rows[i-1], row) {
		start = *rows[i-1].MediaStartSecs
	}
	if i+1 < len(rows) && activity.Adjacent(row, rows[i+1]) {
		end = *rows[i+1].MediaEndSecs
	}
	return start, end, true
}

func (w *Worker) HandleActivityReviewTask(ctx context.Context, t *asynq.Task) (taskErr error) {

	var p activityReviewPayload
	if json.Unmarshal(t.Payload(), &p) != nil || p.ProfileID == 0 || validateSessionID(p.SessionID) != nil || p.Generation <= 0 || p.Object != fmt.Sprintf("videos/%d/%s/merged.mp4", p.ProfileID, p.SessionID) {
		return fmt.Errorf("invalid activity review payload: %w", asynq.SkipRetry)
	}
	rows, err := w.activityRows(ctx, p.ProfileID, p.SessionID)
	if err != nil {
		return err
	}
	if activity.SourceVersion(rows) != p.SourceVersion {
		return nil
	} // superseded
	var session db.Session
	if err = w.DB.WithContext(ctx).Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).First(&session).Error; err != nil {
		return err
	}
	var stored activity.Summary
	_ = json.Unmarshal(session.ActivitySummary, &stored)
	currentGeneration, _ := strconv.ParseInt(stored.MediaGeneration, 10, 64)
	if stored.SourceVersion == p.SourceVersion && currentGeneration > p.Generation {
		return nil
	}
	if currentGeneration != p.Generation {
		stored = activity.Summary{}
	}
	summary := activity.Build(rows, &stored)
	if summary.ReviewState == "completed" {
		return nil
	}
	if !w.ActivityCountingEnabled {
		summary.ReviewState = "disabled"
		return w.saveActivityReview(ctx, p, &summary)
	}
	summary.ReviewState = "running"
	if err = w.saveActivityReview(ctx, p, &summary); err != nil {
		return err
	}
	defer func() {
		if taskErr != nil {
			summary.ReviewState = "queued"
			if getRetryCount(ctx) >= 2 {
				summary.ReviewState = "failed"
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := w.saveActivityReview(cleanup, p, &summary); err != nil {
				w.logger.Warn("activity review state not saved", zap.Error(err))
			}
		}
	}()
	// Pin the exact merged generation selected when scheduling the review.
	reader, err := w.StorageClient.NewReaderWithGeneration(ctx, p.Object, p.Generation)
	if err != nil {
		return err
	}
	defer reader.Close()
	local, err := os.CreateTemp("", "activity-review-*.mp4")
	if err != nil {
		return err
	}
	defer os.Remove(local.Name())
	_, copyErr := io.Copy(local, reader)
	closeErr := local.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	uploaded, err := w.GeminiClient.UploadVideo(ctx, local.Name())
	if uploaded != nil && uploaded.FileName != "" {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = w.GeminiClient.DeleteFile(cleanup, uploaded.FileName)
		}()
	}
	if err != nil {
		return err
	}
	duration := uploaded.VideoDuration.Seconds()
	if duration <= 0 {
		duration = probeVideoDuration(ctx, local.Name())
	}
	if duration <= 0 {
		return errors.New("activity review media duration unavailable")
	}
	previous := map[uint]activity.ReviewedChunk{}
	for _, r := range summary.Reviews {
		previous[r.ChunkID] = r
	}
	replaceReview := func(review activity.ReviewedChunk) {
		for i, old := range summary.Reviews {
			if old.ChunkID == review.ChunkID {
				summary.Reviews[i] = review
				return
			}
		}
		summary.Reviews = append(summary.Reviews, review)
	}
	failed := false
	target := w.buildTargetPersonContext(p.ProfileID, p.SessionID)
	for i, row := range rows {
		if saved, ok := previous[row.ID]; ok && saved.State == "completed" {
			replaceReview(saved)
			continue
		}
		start, end, valid := reviewBounds(rows, i)
		r := activity.ReviewedChunk{ChunkID: row.ID, State: "failed"}
		if valid && end <= duration+0.001 {
			prompt := movementObservationPrompt + target + fmt.Sprintf("\n검토 영상의 절대 media 초를 사용하세요. 문맥 구간 %.6f~%.6f초. 현재 집계 소유 구간은 (%.6f, %.6f]초입니다. 문맥에서 사이클 시작과 끝을 확인하고 기록하되, 이전 합계나 목표 횟수로 추측하지 마세요. 대상이 불명확하면 unknown입니다.", start, end, *row.MediaStartSecs, *row.MediaEndSecs)
			started := time.Now()
			callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			raw, usage, callErr := w.GeminiClient.AnalyzeSegmentWithModel(callCtx, uploaded.FileURI, uploaded.MIMEType, time.Duration(start*float64(time.Second)), time.Duration(end*float64(time.Second)), prompt, gemini.ModelFlash38)
			cancel()
			w.saveTokenUsage(p.SessionID, p.ProfileID, "activity:review", usage)
			w.logger.Info("activity review window", zap.Uint("chunk_id", row.ID), zap.Duration("duration", time.Since(started)), zap.Bool("failed", callErr != nil))
			if callErr == nil {
				document := strings.TrimSpace(raw)
				for _, m := range liveBlocks.FindAllStringSubmatch(raw, -1) {
					if strings.EqualFold(m[1], "movement_observations") {
						document = m[2]
						break
					}
				}
				doc, parseErr := activity.Decode([]byte(stripJSONFence(document)), start, end)
				if parseErr == nil {
					r.State = "completed"
					r.Observations = activity.Own(doc, *row.MediaStartSecs, *row.MediaEndSecs)
				}
			}
		}
		if r.State != "completed" {
			failed = true
		}
		replaceReview(r)
		if err = w.saveActivityReview(ctx, p, &summary); err != nil {
			return err
		}
	}
	summary.ReviewState = "completed"
	built := activity.Build(rows, &summary)
	if failed || len(built.Unassessed) > 0 {
		summary.ReviewState = "partial"
	}
	if err = w.saveActivityReview(ctx, p, &summary); err != nil {
		return err
	}
	if failed && getRetryCount(ctx) < 2 {
		return errors.New("some activity review windows failed")
	}
	return nil
}
