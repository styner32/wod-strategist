package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/sensor"
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

const (
	activityReviewMaxBatchChunks     = 3
	activityReviewMaxBatchCoreSecs   = 35.0
	activityReviewMaxContextPadSecs  = 15.0
	activityReviewWindowTimeout      = 180 * time.Second
	activityReviewMotionVetoScore    = 0.045
	activityReviewMotionConfirmScore = 0.012
	activityReviewAccVetoMaxG        = 0.12
	activityReviewAccVetoMeanG       = 0.08
	activityReviewAccConfirmMaxG     = 0.08
	activityReviewAccMinCoverage     = 0.50
)

func activityReviewFrameGridOptions() FrameGridOptions {
	return FrameGridOptions{
		FormatSecondsBadge: true,
		ThinkingLevel:      "MEDIUM",
	}
}

func rowClockValid(rows []db.ChunkAnalysisResult, i int) bool {
	if i < 0 || i >= len(rows) {
		return false
	}
	row := rows[i]
	if !activity.Timed(row) || !activity.MediaTimed(row) {
		return false
	}
	start, end := *row.MediaStartSecs, *row.MediaEndSecs
	// Distinct files with overlapping source/media clocks are not trustworthy.
	for j, other := range rows {
		if i == j {
			continue
		}
		if activity.Timed(other) && *row.StartSecs < *other.EndSecs && *row.EndSecs > *other.StartSecs {
			return false
		}
		if activity.MediaTimed(other) && start < *other.MediaEndSecs && end > *other.MediaStartSecs {
			return false
		}
	}
	return true
}

func reviewBounds(rows []db.ChunkAnalysisResult, i int) (float64, float64, bool) {
	if !rowClockValid(rows, i) {
		return 0, 0, false
	}
	row := rows[i]
	start, end := *row.MediaStartSecs, *row.MediaEndSecs
	if i > 0 && activity.Adjacent(rows[i-1], row) {
		start = *rows[i-1].MediaStartSecs
	}
	if i+1 < len(rows) && activity.Adjacent(row, rows[i+1]) {
		end = *rows[i+1].MediaEndSecs
	}
	return start, end, true
}

// loadSessionSensorTimeline returns the optional completed Polar H10 sensor timeline
// when available and valid. Missing, failed, or stale sensor data returns nil and
// never blocks or penalizes activity review.
func (w *Worker) loadSessionSensorTimeline(ctx context.Context, profileID uint, sessionID string) *sensor.SensorTimelineData {
	if w.DB == nil {
		return nil
	}
	var a db.AnalysisResult
	if w.DB.WithContext(ctx).Where("session_id = ? AND profile_id = ?", sessionID, profileID).First(&a).Error != nil || a.SensorState != db.SensorStateCompleted {
		return nil
	}
	if len(a.SensorTimeline) == 0 || string(a.SensorTimeline) == "{}" || string(a.SensorTimeline) == "null" {
		return nil
	}
	var failureCheck struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(a.SensorTimeline, &failureCheck) == nil && failureCheck.Status == "failed" {
		return nil
	}
	var timeline sensor.SensorTimelineData
	if err := json.Unmarshal(a.SensorTimeline, &timeline); err != nil || len(timeline.Points) == 0 {
		return nil
	}
	if timeline.Clock != "" && timeline.Clock != "capture_clock" {
		return nil
	}
	if timeline.Source.SensorVersion != "" && timeline.Source.SensorVersion != fmt.Sprint(a.SensorVersion) {
		return nil
	}
	var processing SensorProcessingRecord
	if len(a.SensorProcessing) > 0 && json.Unmarshal(a.SensorProcessing, &processing) == nil {
		if processing.RequestID != "" && timeline.Source.RequestID != "" && timeline.Source.RequestID != processing.RequestID {
			return nil
		}
		if processing.TargetGeneration != "" && timeline.Source.SourceGeneration != "" && timeline.Source.SourceGeneration != processing.TargetGeneration {
			return nil
		}
	}
	if !sort.SliceIsSorted(timeline.Points, func(i, j int) bool { return timeline.Points[i].StartMs < timeline.Points[j].StartMs }) {
		sort.Slice(timeline.Points, func(i, j int) bool { return timeline.Points[i].StartMs < timeline.Points[j].StartMs })
	}
	return &timeline
}

type accChunkStats struct {
	hasValid bool
	maxG     float64
	meanG    float64
	coverage float64
}

func chunkAccStats(timeline *sensor.SensorTimelineData, captureStartSecs, captureEndSecs float64) accChunkStats {
	if timeline == nil || len(timeline.Points) == 0 || !activity.ValidInterval(captureStartSecs, captureEndSecs) {
		return accChunkStats{}
	}
	startMs := int64(math.Round(captureStartSecs * 1000.0))
	endMs := int64(math.Round(captureEndSecs * 1000.0))
	if endMs <= startMs {
		return accChunkStats{}
	}
	sorted := sort.SliceIsSorted(timeline.Points, func(i, j int) bool {
		return timeline.Points[i].StartMs < timeline.Points[j].StartMs
	})
	first := 0
	if sorted {
		first = sort.Search(len(timeline.Points), func(i int) bool {
			return timeline.Points[i].EndMs > startMs
		})
	}
	var (
		hasValid    bool
		maxG        float64
		weightedSum float64
		validMs     float64
	)
	for i := first; i < len(timeline.Points); i++ {
		pt := timeline.Points[i]
		if pt.StartMs >= endMs {
			if sorted {
				break
			}
			continue
		}
		ovStart := pt.StartMs
		if startMs > ovStart {
			ovStart = startMs
		}
		ovEnd := pt.EndMs
		if endMs < ovEnd {
			ovEnd = endMs
		}
		if ovEnd <= ovStart {
			continue
		}
		if pt.AccMagnitudeStdG.Status != sensor.StatusValid || pt.AccMagnitudeStdG.Value == nil {
			continue
		}
		v := *pt.AccMagnitudeStdG.Value
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		durMs := float64(ovEnd - ovStart)
		validMs += durMs
		weightedSum += v * durMs
		if !hasValid || v > maxG {
			maxG = v
		}
		hasValid = true
	}
	if !hasValid || validMs <= 0 {
		return accChunkStats{}
	}
	return accChunkStats{
		hasValid: true,
		maxG:     maxG,
		meanG:    weightedSum / validMs,
		coverage: validMs / float64(endMs-startMs),
	}
}

func isExplicitRestExerciseType(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "none") {
		return true
	}
	return isExplicitNonExerciseActivity(trimmed)
}

func isPass1ExplicitRest(rows []db.ChunkAnalysisResult, idx int) bool {
	if !rowClockValid(rows, idx) {
		return false
	}
	row := rows[idx]
	if row.Status != "COMPLETED" || !isExplicitRestExerciseType(row.ExerciseType) {
		return false
	}
	if rawSig := strings.TrimSpace(row.ObservedSignals); rawSig != "" && rawSig != "{}" {
		var sig struct {
			Movement      string `json:"movement"`
			ActivityState string `json:"activity_state"`
			RepCount      int    `json:"rep_count"`
		}
		if json.Unmarshal([]byte(rawSig), &sig) != nil {
			return false
		}
		if strings.EqualFold(strings.TrimSpace(sig.ActivityState), "exercise") || sig.RepCount > 0 || !isExplicitRestExerciseType(sig.Movement) {
			return false
		}
	}
	if len(row.MovementObservations) == 0 {
		return false
	}
	var local activity.Observations
	_ = json.Unmarshal(row.MovementObservations, &local)
	dur := local.DurationSecs
	if !activity.ValidInterval(0, dur) {
		dur = *row.EndSecs - *row.StartSecs
	}
	doc, err := activity.Decode(row.MovementObservations, 0, dur)
	if err != nil {
		return false
	}
	return doc.TargetState == "identified" && doc.ActivityState == "rest" && len(doc.Events) == 0 && len(doc.Unassessed) == 0
}

func isRestRunInterior(rows []db.ChunkAnalysisResult, idx int) bool {
	if len(rows) < 2 || !isPass1ExplicitRest(rows, idx) {
		return false
	}
	leftRest := idx == 0 || (activity.Adjacent(rows[idx-1], rows[idx]) && isPass1ExplicitRest(rows, idx-1))
	rightRest := idx == len(rows)-1 || (activity.Adjacent(rows[idx], rows[idx+1]) && isPass1ExplicitRest(rows, idx+1))
	return leftRest && rightRest
}

func isPass1ExplicitNotVisible(rows []db.ChunkAnalysisResult, idx int) bool {
	if !rowClockValid(rows, idx) {
		return false
	}
	row := rows[idx]
	if row.Status != "COMPLETED" || !isExplicitRestExerciseType(row.ExerciseType) {
		return false
	}
	if row.TargetConfidence >= trackingMinConfidence {
		return false
	}
	if tp, ok := parseStoredTargetPerson(row.TargetCues); ok && ((!tp.Ambiguous && len(tp.Boxes) > 0) || tp.ReferenceImage != "") {
		return false
	}
	if rawSig := strings.TrimSpace(row.ObservedSignals); rawSig != "" && rawSig != "{}" {
		var sig struct {
			Movement      string `json:"movement"`
			ActivityState string `json:"activity_state"`
			RepCount      int    `json:"rep_count"`
		}
		if json.Unmarshal([]byte(rawSig), &sig) != nil {
			return false
		}
		if strings.EqualFold(strings.TrimSpace(sig.ActivityState), "exercise") || sig.RepCount > 0 || !isExplicitRestExerciseType(sig.Movement) {
			return false
		}
	}
	if len(row.MovementObservations) == 0 {
		return false
	}
	var local activity.Observations
	_ = json.Unmarshal(row.MovementObservations, &local)
	dur := local.DurationSecs
	if !activity.ValidInterval(0, dur) {
		dur = *row.EndSecs - *row.StartSecs
	}
	doc, err := activity.Decode(row.MovementObservations, 0, dur)
	if err != nil {
		return false
	}
	return doc.TargetState == "not_visible" && len(doc.Events) == 0
}

// isNotVisibleRunInterior returns true for interior chunks of a contiguous Pass-1
// not_visible run (not_visible -> not_visible -> not_visible). Transition boundary
// chunks adjacent to non-not_visible chunks return false so departure/return boundary
// repetitions are still reviewed on merged media.
func isNotVisibleRunInterior(rows []db.ChunkAnalysisResult, idx int) bool {
	if len(rows) < 2 || !isPass1ExplicitNotVisible(rows, idx) {
		return false
	}
	leftNotVisible := idx == 0 || (activity.Adjacent(rows[idx-1], rows[idx]) && isPass1ExplicitNotVisible(rows, idx-1))
	rightNotVisible := idx == len(rows)-1 || (activity.Adjacent(rows[idx], rows[idx+1]) && isPass1ExplicitNotVisible(rows, idx+1))
	return leftNotVisible && rightNotVisible
}

func (w *Worker) cachedPass2TrackingInputs(ctx context.Context, refs []trackingReferenceRef, cache map[string][]byte, start, end float64) (string, [][]byte) {
	if len(refs) == 0 {
		return "", nil
	}
	load := func(object string) []byte {
		if object == "" {
			return nil
		}
		if img, seen := cache[object]; seen {
			return img
		}
		img := w.downloadTrackingReference(ctx, object)
		cache[object] = img
		return img
	}
	near := ""
	for _, ref := range refs {
		if ref.End > start && ref.Start < end {
			near = ref.Object
			break
		}
	}
	anchor := refs[0].Object
	var images [][]byte
	if img := load(near); img != nil {
		images = append(images, img)
	}
	if anchor != near {
		if img := load(anchor); img != nil {
			images = append(images, img)
		}
	}
	if len(images) == 0 {
		return "", nil
	}
	return pass2TrackingPrompt, images
}

// shouldSkipActivityReviewRestChunk decides whether a Pass-1 rest chunk is sufficiently
// confirmed to skip independent Pass-2 media review without risking rep loss.
// Note: MoveNet workout_confidence is intentionally excluded; Polar H10 sensor_timeline
// is strictly optional (ignored when absent).
func shouldSkipActivityReviewRestChunk(rows []db.ChunkAnalysisResult, idx int, workoutType string, timeline *sensor.SensorTimelineData) bool {
	if !isPass1ExplicitRest(rows, idx) {
		return false
	}
	// Recovery sessions (warmup/cooldown) ignore motion and H10 thresholds and skip
	// directly when Pass 1 forms a contiguous rest run (rest -> rest -> rest).
	if IsRecoveryWorkoutType(workoutType) {
		return isRestRunInterior(rows, idx)
	}
	// Never skip a rest chunk immediately following an active chunk: a rep starting near
	// the end of the active chunk and finishing across the boundary belongs to this chunk's
	// (MediaStartSecs, MediaEndSecs] ownership window.
	noPrecedingActive := idx == 0 || (activity.Adjacent(rows[idx-1], rows[idx]) && isPass1ExplicitRest(rows, idx-1))
	if !noPrecedingActive {
		return false
	}
	row := rows[idx]
	if row.MotionScore != nil && *row.MotionScore >= activityReviewMotionVetoScore {
		return false
	}
	acc := chunkAccStats(timeline, *row.StartSecs, *row.EndSecs)
	if acc.hasValid && (acc.maxG >= activityReviewAccVetoMaxG || acc.meanG >= activityReviewAccVetoMeanG) {
		return false
	}
	if isRestRunInterior(rows, idx) {
		return true
	}
	if acc.hasValid && acc.coverage >= activityReviewAccMinCoverage && acc.maxG < activityReviewAccConfirmMaxG {
		return true
	}
	if row.MotionScore != nil && *row.MotionScore < activityReviewMotionConfirmScore {
		return true
	}
	return false
}

type activityReviewBatch struct {
	Indices   []int
	CoreStart float64
	CoreEnd   float64
	WinStart  float64
	WinEnd    float64
	Valid     bool
}

func buildActivityReviewBatches(rows []db.ChunkAnalysisResult, completedIDs map[uint]bool) []activityReviewBatch {
	batches := []activityReviewBatch{}
	for i := 0; i < len(rows); {
		if completedIDs[rows[i].ID] {
			i++
			continue
		}
		if !rowClockValid(rows, i) {
			batches = append(batches, activityReviewBatch{Indices: []int{i}, Valid: false})
			i++
			continue
		}
		indices := []int{i}
		coreStart := *rows[i].MediaStartSecs
		coreEnd := *rows[i].MediaEndSecs
		j := i + 1
		for j < len(rows) && len(indices) < activityReviewMaxBatchChunks {
			if completedIDs[rows[j].ID] || !rowClockValid(rows, j) || !activity.Adjacent(rows[j-1], rows[j]) {
				break
			}
			if *rows[j].MediaEndSecs-coreStart > activityReviewMaxBatchCoreSecs+0.001 {
				break
			}
			indices = append(indices, j)
			coreEnd = *rows[j].MediaEndSecs
			j++
		}
		firstIdx := indices[0]
		lastIdx := indices[len(indices)-1]
		winStart := coreStart
		if firstIdx > 0 && rowClockValid(rows, firstIdx-1) && activity.Adjacent(rows[firstIdx-1], rows[firstIdx]) {
			cand := *rows[firstIdx-1].MediaStartSecs
			if coreStart-cand > activityReviewMaxContextPadSecs {
				cand = coreStart - activityReviewMaxContextPadSecs
			}
			winStart = cand
		}
		winEnd := coreEnd
		if lastIdx+1 < len(rows) && rowClockValid(rows, lastIdx+1) && activity.Adjacent(rows[lastIdx], rows[lastIdx+1]) {
			cand := *rows[lastIdx+1].MediaEndSecs
			if cand-coreEnd > activityReviewMaxContextPadSecs {
				cand = coreEnd + activityReviewMaxContextPadSecs
			}
			winEnd = cand
		}
		batches = append(batches, activityReviewBatch{
			Indices:   indices,
			CoreStart: coreStart,
			CoreEnd:   coreEnd,
			WinStart:  winStart,
			WinEnd:    winEnd,
			Valid:     true,
		})
		i = j
	}
	return batches
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

	timeline := w.loadSessionSensorTimeline(ctx, p.ProfileID, p.SessionID)
	// Freeze the original skip candidates before protecting completion-owning
	// successors. Using the adjusted decisions would propagate review through
	// the rest of a rest run, and using completedIDs would drop protection on retry.
	restSkipCandidates := make([]bool, len(rows))
	for i := range rows {
		restSkipCandidates[i] = shouldSkipActivityReviewRestChunk(rows, i, session.WorkoutType, timeline)
	}
	completedIDs := make(map[uint]bool, len(rows))
	skippedRestChunks := 0
	skippedNotVisibleChunks := 0
	for i, row := range rows {
		if saved, ok := previous[row.ID]; ok && saved.State == "completed" {
			replaceReview(saved)
			completedIDs[row.ID] = true
			continue
		}
		if isNotVisibleRunInterior(rows, i) {
			acc := chunkAccStats(timeline, *row.StartSecs, *row.EndSecs)
			sensorActive := acc.hasValid && (acc.maxG >= activityReviewAccVetoMaxG || acc.meanG >= activityReviewAccVetoMeanG)
			replaceReview(activity.ReviewedChunk{
				ChunkID:      row.ID,
				State:        "completed",
				Observations: activity.ConfirmedNotVisible(*row.MediaStartSecs, *row.MediaEndSecs, sensorActive),
			})
			completedIDs[row.ID] = true
			skippedNotVisibleChunks++
			continue
		}
		skipRest := restSkipCandidates[i]
		if !IsRecoveryWorkoutType(session.WorkoutType) && i > 0 &&
			activity.Adjacent(rows[i-1], row) && !restSkipCandidates[i-1] {
			skipRest = false
		}
		if skipRest {
			dur := *row.MediaEndSecs - *row.MediaStartSecs
			replaceReview(activity.ReviewedChunk{
				ChunkID:      row.ID,
				State:        "completed",
				Observations: activity.ConfirmedRest(dur),
			})
			completedIDs[row.ID] = true
			skippedRestChunks++
			continue
		}
		if saved, ok := previous[row.ID]; ok {
			replaceReview(saved)
		} else {
			replaceReview(activity.ReviewedChunk{ChunkID: row.ID, State: "pending"})
		}
	}

	batches := buildActivityReviewBatches(rows, completedIDs)
	w.logger.Info("activity review plan",
		zap.String("session_id", p.SessionID),
		zap.Int("total_chunks", len(rows)),
		zap.Int("skipped_rest_chunks", skippedRestChunks),
		zap.Int("skipped_not_visible_chunks", skippedNotVisibleChunks),
		zap.Int("batch_count", len(batches)),
		zap.Bool("has_sensor_timeline", timeline != nil),
	)
	if len(batches) == 0 {
		summary.ReviewState = "completed"
		built := activity.Build(rows, &summary)
		if len(built.Unassessed) > 0 {
			summary.ReviewState = "partial"
		}
		return w.saveActivityReview(ctx, p, &summary)
	}

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
	var (
		uploadMu sync.Mutex
		uploaded *gemini.UploadResult
	)
	defer func() {
		if uploaded != nil && uploaded.FileName != "" {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = w.GeminiClient.DeleteFile(cleanup, uploaded.FileName)
		}
	}()
	ensureUploaded := func(callCtx context.Context) (*gemini.UploadResult, error) {
		uploadMu.Lock()
		defer uploadMu.Unlock()
		if uploaded != nil && uploaded.FileURI != "" {
			return uploaded, nil
		}
		res, upErr := w.GeminiClient.UploadVideo(callCtx, local.Name())
		if res != nil && res.FileName != "" {
			uploaded = res
		}
		if upErr != nil {
			return nil, upErr
		}
		return uploaded, nil
	}

	duration := probeVideoDuration(ctx, local.Name())
	localProbed := duration > 0
	if !localProbed {
		up, upErr := ensureUploaded(ctx)
		if upErr != nil {
			return upErr
		}
		duration = up.VideoDuration.Seconds()
	}
	if duration <= 0 {
		return errors.New("activity review media duration unavailable")
	}

	failed := false
	target := w.buildTargetPersonContext(p.ProfileID, p.SessionID)
	trackingRefs := w.loadTrackingReferences(ctx, p.SessionID, p.ProfileID)
	trackingCache := make(map[string][]byte, len(trackingRefs))
	if skippedRestChunks > 0 || skippedNotVisibleChunks > 0 {
		if err = w.saveActivityReview(ctx, p, &summary); err != nil {
			return err
		}
	}

	var mu sync.Mutex
	var saveErr error
	sem := make(chan struct{}, maxConcurrentSegmentAnalyses)
	var wg sync.WaitGroup

	for _, b := range batches {
		var trackingPrompt string
		var trackingImages [][]byte
		if b.Valid && b.WinEnd <= duration+0.001 {
			trackingPrompt, trackingImages = w.cachedPass2TrackingInputs(ctx, trackingRefs, trackingCache, b.WinStart, b.WinEnd)
		}
		wg.Add(1)
		go func(batch activityReviewBatch, batchTrackingPrompt string, batchTrackingImages [][]byte) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			chunkReviews := make([]activity.ReviewedChunk, 0, len(batch.Indices))
			if batch.Valid && batch.WinEnd <= duration+0.001 {
				prompt := movementObservationPrompt + target + batchTrackingPrompt + fmt.Sprintf(
					"\n검토 영상의 절대 media 초를 사용하세요. 문맥 구간 %.6f~%.6f초. 현재 집계 소유 구간은 (%.6f, %.6f]초입니다. 문맥에서 사이클 시작과 끝을 확인하고 기록하되, 이전 합계나 목표 횟수로 추측하지 마세요. 구간 중 일부에서만 대상이나 동작이 확인되고 다른 하위 구간만 가림·이탈·판독불가인 경우, 확인된 구간이 있으면 target_state는 identified로 유지하고 판독불가 하위 구간만 unassessed 배열에 기록하세요. 전체 구간 내내 대상을 식별할 수 없을 때만 unknown입니다.",
					batch.WinStart, batch.WinEnd, batch.CoreStart, batch.CoreEnd,
				)
				started := time.Now()
				callCtx, cancel := context.WithTimeout(ctx, activityReviewWindowTimeout)
				segStart := time.Duration(batch.WinStart * float64(time.Second))
				segEnd := time.Duration(batch.WinEnd * float64(time.Second))
				gridOpts := activityReviewFrameGridOptions()
				gridOpts.ReferenceImages = batchTrackingImages
				var (
					raw     string
					sheets  []FrameGridSheet
					usage   *gemini.TokenUsage
					callErr error
				)
				firstChunkID := rows[batch.Indices[0]].ID
				useFrameGrid := localProbed && frameGridEligible(segEnd-segStart, gridOpts)
				if useFrameGrid {
					raw, sheets, usage, callErr = w.AnalyzeVideoIntervalWithFrameGrids(
						callCtx, local.Name(), segStart, segEnd, prompt, gemini.ModelFlash38, gridOpts,
					)
				}
				if !useFrameGrid || (callErr != nil && len(sheets) == 0) {
					if callErr != nil {
						w.logger.Warn("frame grid extraction failed for activity review window; falling back to video segment analysis",
							zap.Uint("chunk_id", firstChunkID),
							zap.Duration("start", segStart),
							zap.Duration("end", segEnd),
							zap.Error(callErr))
					}
					up, upErr := ensureUploaded(callCtx)
					if upErr != nil {
						callErr = upErr
					} else {
						segCtx := gemini.WithThinkingLevel(callCtx, "MEDIUM")
						if len(batchTrackingImages) > 0 {
							raw, usage, callErr = w.GeminiClient.AnalyzeSegmentWithModelAndImages(
								segCtx, up.FileURI, up.MIMEType, segStart, segEnd, prompt, gemini.ModelFlash38, batchTrackingImages,
							)
						} else {
							raw, usage, callErr = w.GeminiClient.AnalyzeSegmentWithModel(
								segCtx, up.FileURI, up.MIMEType, segStart, segEnd, prompt, gemini.ModelFlash38,
							)
						}
					}
				}
				cancel()
				w.saveTokenUsage(p.SessionID, p.ProfileID, "activity:review", usage)
				w.logger.Info("activity review window",
					zap.Uint("chunk_id", firstChunkID),
					zap.Int("batch_chunks", len(batch.Indices)),
					zap.Duration("duration", time.Since(started)),
					zap.Bool("failed", callErr != nil),
				)
				if callErr == nil {
					document := strings.TrimSpace(raw)
					for _, m := range liveBlocks.FindAllStringSubmatch(raw, -1) {
						if strings.EqualFold(m[1], "movement_observations") {
							document = m[2]
							break
						}
					}
					doc, parseErr := activity.Decode([]byte(stripJSONFence(document)), batch.WinStart, batch.WinEnd)
					if parseErr == nil {
						for _, idx := range batch.Indices {
							chunkRow := rows[idx]
							chunkReviews = append(chunkReviews, activity.ReviewedChunk{
								ChunkID:      chunkRow.ID,
								State:        "completed",
								Observations: activity.Own(doc, *chunkRow.MediaStartSecs, *chunkRow.MediaEndSecs),
							})
						}
					}
				}
			}
			if len(chunkReviews) == 0 {
				for _, idx := range batch.Indices {
					chunkReviews = append(chunkReviews, activity.ReviewedChunk{
						ChunkID: rows[idx].ID,
						State:   "failed",
					})
				}
			}

			mu.Lock()
			defer mu.Unlock()
			for _, r := range chunkReviews {
				if r.State != "completed" {
					failed = true
				}
				replaceReview(r)
			}
			if saveErr == nil {
				if err := w.saveActivityReview(ctx, p, &summary); err != nil {
					saveErr = err
				}
			}
		}(b, trackingPrompt, trackingImages)
	}
	wg.Wait()
	if saveErr != nil {
		return saveErr
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
