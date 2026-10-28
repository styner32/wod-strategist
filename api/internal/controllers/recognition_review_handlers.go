package controllers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/recognitionreview"
	"github.com/wod-strategist/api/internal/worker"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errRecognitionSourceChanged   = errors.New("원본 분석 또는 확정 기록이 변경되었습니다. 평가를 새로 불러오세요")
	errRecognitionActive          = errors.New("이 영상의 오류 진단이 이미 진행 중입니다")
	errRecognitionRequestConflict = errors.New("같은 요청 ID가 다른 진단에 사용되었습니다")
)

// loadRecognitionSnapshot deliberately bypasses the response enrichment and
// original_candidate: both can contain a manually corrected movement.
func loadRecognitionSnapshot(ctx context.Context, tx *gorm.DB, sid string, pid uint, lock bool) (recognitionreview.Snapshot, error) {
	out := recognitionreview.Snapshot{Chunks: []recognitionreview.ChunkEvidence{}}
	var analysis db.AnalysisResult
	q := tx.WithContext(ctx).Where("session_id = ? AND profile_id = ? AND archived_at IS NULL", sid, pid)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.First(&analysis).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, errRecognitionSourceChanged
		}
		return out, err
	}
	progress, err := workoutprogress.Decode(analysis.WorkoutProgress)
	if err != nil || analysis.Status != "COMPLETED" {
		return out, errRecognitionSourceChanged
	}
	out.Progress = progress
	out.AnalysisOutput = analysis.Output
	var rows []db.PerformanceRecord
	if err = tx.WithContext(ctx).Where("session_id = ? AND profile_id = ?", sid, pid).Order("start_secs ASC,id ASC").Find(&rows).Error; err != nil {
		return out, err
	}
	records := []workoutprogress.Record{}
	for _, row := range rows {
		// Exclude derived rest comparisons to keep this path independent of the
		// confirmation candidate even when it is malformed or synthesized.
		r := workoutprogress.Record{ID: row.ID, SessionID: row.SessionID, IntervalID: row.IntervalID, SourceVersion: row.SourceVersion, Status: row.Status, Movement: row.Movement, Start: row.StartSecs, End: row.EndSecs, ElapsedSeconds: row.ElapsedSeconds, Reps: row.Reps, LoadKG: row.LoadKG, Measurement: row.Measurement, Technique: row.Technique, WorkoutPrescription: row.WorkoutPrescription, RepsPerMinute: row.RepsPerMinute, RateBasis: row.RateBasis, MedianCycleSeconds: row.MedianCycleSeconds, PerformedAt: row.PerformedAt, PerformedAtSource: row.PerformedAtSource, ConfirmedAt: row.ConfirmedAt}
		if row.LoadValue != nil && row.LoadUnit != nil && row.LoadBasis != nil {
			r.Load = &workoutprogress.Load{Value: *row.LoadValue, Unit: *row.LoadUnit, Basis: *row.LoadBasis, Status: "confirmed"}
		}
		records = append(records, r)
	}
	var chunks []db.ChunkAnalysisResult
	if err = tx.WithContext(ctx).Where("session_id = ? AND profile_id = ?", sid, pid).Order("id ASC").Find(&chunks).Error; err != nil {
		return out, err
	}
	for _, ch := range chunks {
		var observations json.RawMessage
		if len(ch.MovementObservations) > 0 && json.Valid(ch.MovementObservations) {
			observations = json.RawMessage(ch.MovementObservations)
		}
		out.Chunks = append(out.Chunks, recognitionreview.ChunkEvidence{ID: ch.ID, MediaStartSecs: ch.MediaStartSecs, MediaEndSecs: ch.MediaEndSecs, ExerciseType: ch.ExerciseType, Status: ch.Status, Output: ch.Output, MovementObservations: observations, TargetConfidence: ch.TargetConfidence, TargetCues: ch.TargetCues, SkipReason: ch.SkipReason, MotionScore: ch.MotionScore, UpdatedAt: ch.UpdatedAt})
	}
	out.Evaluation = recognitionreview.BuildEvaluation(progress, records, out.Chunks, analysis.Output)
	return out, nil
}

// GetRecognitionEvaluation compares current confirmations with raw observations without AI.
// @Summary Compare confirmed performance with original video recognition
// @Tags analysis
// @Produce json
// @Param session_id path string true "Owned session ID"
// @Success 200 {object} recognitionreview.Evaluation
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /sessions/{session_id}/recognition-evaluation [get]
func (ctl *Controller) GetRecognitionEvaluation(c *gin.Context) {
	sid, pid, ok := ctl.progressSession(c)
	if !ok {
		return
	}
	var snapshot recognitionreview.Snapshot
	err := ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		snapshot, err = loadRecognitionSnapshot(c.Request.Context(), tx, sid, pid, false)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		recognitionError(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot.Evaluation)
}

func decodeRecognitionRequest(c *gin.Context) (CreateRecognitionReviewRequest, error) {
	var req CreateRecognitionReviewRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		return req, errors.New("invalid diagnosis request")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return req, errors.New("invalid diagnosis request")
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	if req.ClientRequestID == "" || len(req.ClientRequestID) > 128 || req.SourceVersion == "" || req.EvaluationFingerprint == "" || len(req.RecordIDs) == 0 || len(req.RecordIDs) > 100 || math.IsNaN(req.StartSecs) || math.IsInf(req.StartSecs, 0) || math.IsNaN(req.EndSecs) || math.IsInf(req.EndSecs, 0) || req.StartSecs < 0 || req.EndSecs <= req.StartSecs {
		return req, errors.New("select confirmed records and a valid video range")
	}
	sort.Slice(req.RecordIDs, func(i, j int) bool { return req.RecordIDs[i] < req.RecordIDs[j] })
	for i, id := range req.RecordIDs {
		if id == 0 || (i > 0 && id == req.RecordIDs[i-1]) {
			return req, errors.New("record IDs must be unique")
		}
	}
	return req, nil
}

func recognitionRequestHash(req CreateRecognitionReviewRequest) string {
	req.ClientRequestID = ""
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CreateRecognitionReview snapshots confirmed truth and queues a separate diagnostic.
// @Summary Manually diagnose selected video recognition errors
// @Tags analysis
// @Accept json
// @Produce json
// @Param session_id path string true "Owned session ID"
// @Param request body CreateRecognitionReviewRequest true "Confirmed records and review range"
// @Success 202 {object} CreateRecognitionReviewResponse
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /sessions/{session_id}/recognition-reviews [post]
func (ctl *Controller) CreateRecognitionReview(c *gin.Context) {
	sid, pid, ok := ctl.progressSession(c)
	if !ok {
		return
	}
	req, err := decodeRecognitionRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if ctl.queueClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "오류 진단 작업을 시작할 수 없습니다"})
		return
	}
	hash := recognitionRequestHash(req)
	var run db.RecognitionReviewRun
	err = ctl.db.WithContext(c.Request.Context()).Where("profile_id = ? AND client_request_id = ?", pid, req.ClientRequestID).First(&run).Error
	if err == nil {
		if run.SessionID != sid || run.RequestHash != hash {
			recognitionError(c, errRecognitionRequestConflict)
			return
		}
		if err = ctl.ensureRecognitionReviewTask(c.Request.Context(), &run); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "진단 작업 대기열에 연결하지 못했습니다. 같은 요청으로 다시 시도하세요"})
			return
		}
		c.JSON(http.StatusAccepted, CreateRecognitionReviewResponse{RunID: run.ID, Status: run.Status})
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		recognitionError(c, err)
		return
	}
	err = ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		snapshot, err := loadRecognitionSnapshot(c.Request.Context(), tx, sid, pid, true)
		if err != nil {
			return err
		}
		var previous db.RecognitionReviewRun
		if e := tx.Where("profile_id = ? AND client_request_id = ?", pid, req.ClientRequestID).First(&previous).Error; e == nil {
			if previous.SessionID != sid || previous.RequestHash != hash {
				return errRecognitionRequestConflict
			}
			run = previous
			return nil
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if snapshot.Progress.SourceVersion != req.SourceVersion || snapshot.Evaluation.Fingerprint != req.EvaluationFingerprint {
			return errRecognitionSourceChanged
		}
		var active int64
		if err = tx.Model(&db.RecognitionReviewRun{}).Where("profile_id = ? AND session_id = ? AND status IN ?", pid, sid, []string{db.RecognitionReviewStatusQueued, db.RecognitionReviewStatusRunning}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return errRecognitionActive
		}
		batches, err := recognitionreview.BuildBatches(snapshot.Evaluation.Records, req.StartSecs, req.EndSecs, req.RecordIDs)
		if err != nil {
			return fmt.Errorf("invalid selection: %w", err)
		}
		object, err := ctl.resolveTargetVideoObject(c.Request.Context(), pid, sid, "merged")
		if err != nil {
			return fmt.Errorf("video unavailable: %w", err)
		}
		attrs, err := ctl.storageClient.ObjectAttrs(c.Request.Context(), object)
		if err != nil {
			return fmt.Errorf("video unavailable: %w", err)
		}
		if attrs.Generation <= 0 {
			return errors.New("video unavailable: missing immutable generation")
		}
		snapshotJSON, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		batchJSON, _ := json.Marshal(batches)
		idsJSON, _ := json.Marshal(req.RecordIDs)
		model := strings.TrimSpace(ctl.recognitionReviewModel)
		if model == "" {
			model = gemini.ModelArgon4
		}
		now := time.Now().UTC()
		run = db.RecognitionReviewRun{SessionID: sid, ProfileID: pid, ClientRequestID: req.ClientRequestID, RequestHash: hash, TaskID: uuid.NewString(), Status: db.RecognitionReviewStatusQueued, SourceVersion: req.SourceVersion, EvaluationFingerprint: req.EvaluationFingerprint, SourceGCSURI: fmt.Sprintf("gs://%s/%s", ctl.bucketName, object), SourceObject: object, SourceGeneration: attrs.Generation, RecordIDs: db.JSONDocument(idsJSON), StartSecs: req.StartSecs, EndSecs: req.EndSecs, Snapshot: db.JSONDocument(snapshotJSON), Batches: db.JSONDocument(batchJSON), Model: model, PromptVersion: recognitionreview.PromptVersion, SchemaVersion: recognitionreview.SchemaVersion, CreatedAt: now, UpdatedAt: now}
		return tx.Create(&run).Error
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		// A concurrent identical request in another session can race on the
		// profile-scoped unique key. Recover it without issuing another job.
		var prior db.RecognitionReviewRun
		if e := ctl.db.WithContext(c.Request.Context()).Where("profile_id = ? AND client_request_id = ?", pid, req.ClientRequestID).First(&prior).Error; e == nil {
			if prior.SessionID != sid || prior.RequestHash != hash {
				recognitionError(c, errRecognitionRequestConflict)
				return
			}
			run = prior
		} else {
			recognitionError(c, err)
			return
		}
	}
	if err = ctl.ensureRecognitionReviewTask(c.Request.Context(), &run); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "진단 작업 대기열에 연결하지 못했습니다. 같은 요청으로 다시 시도하세요"})
		return
	}
	c.JSON(http.StatusAccepted, CreateRecognitionReviewResponse{RunID: run.ID, Status: run.Status})
}

func (ctl *Controller) ensureRecognitionReviewTask(ctx context.Context, run *db.RecognitionReviewRun) error {
	if run.Status != db.RecognitionReviewStatusQueued {
		return nil
	}
	task, err := worker.NewRecognitionReviewTask(run.ID)
	if err != nil {
		return err
	}
	_, err = ctl.queueClient.Enqueue(task, asynq.TaskID(run.TaskID), asynq.MaxRetry(2), asynq.Timeout(60*time.Minute))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

func recognitionError(c *gin.Context, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "23505") {
		c.JSON(http.StatusConflict, gin.H{"error": "평가 기준 또는 실행 상태가 변경되었습니다. 새로 불러오세요"})
		return
	}
	switch {
	case errors.Is(err, errRecognitionSourceChanged), errors.Is(err, errRecognitionActive), errors.Is(err, errRecognitionRequestConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case strings.HasPrefix(err.Error(), "invalid selection:"):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case strings.HasPrefix(err.Error(), "video unavailable:"):
		c.JSON(http.StatusConflict, gin.H{"error": "원본 영상을 사용할 수 없어 진단을 시작할 수 없습니다"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "영상 분석 평가를 불러오지 못했습니다"})
	}
}

func recognitionRunResponse(row db.RecognitionReviewRun, currentFingerprint string, detail bool) RecognitionReviewRunResponse {
	out := RecognitionReviewRunResponse{ID: row.ID, Status: row.Status, SourceVersion: row.SourceVersion, EvaluationFingerprint: row.EvaluationFingerprint, RecordIDs: []uint{}, StartSecs: row.StartSecs, EndSecs: row.EndSecs, Model: row.Model, PromptVersion: row.PromptVersion, SchemaVersion: row.SchemaVersion, Batches: []RecognitionReviewBatchResponse{}, Outdated: currentFingerprint != row.EvaluationFingerprint, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens, DurationMs: row.DurationMs, Error: row.SafeError, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	_ = json.Unmarshal(row.RecordIDs, &out.RecordIDs)
	var batches []recognitionreview.Batch
	_ = json.Unmarshal(row.Batches, &batches)
	for _, b := range batches {
		findings := []recognitionreview.Finding{}
		if detail && b.Findings != nil {
			findings = b.Findings
		}
		out.Batches = append(out.Batches, RecognitionReviewBatchResponse{ID: b.ID, RecordID: b.RecordID, StartSecs: b.StartSecs, EndSecs: b.EndSecs, CoreStartSecs: b.CoreStartSecs, CoreEndSecs: b.CoreEndSecs, Status: b.Status, Findings: findings, Error: b.Error, InputTokens: b.InputTokens, OutputTokens: b.OutputTokens, TotalTokens: b.TotalTokens})
	}
	if detail {
		var snapshot recognitionreview.Snapshot
		if json.Unmarshal(row.Snapshot, &snapshot) == nil {
			out.Evaluation = &snapshot.Evaluation
		}
	}
	return out
}

func (ctl *Controller) recognitionCurrentFingerprint(ctx context.Context, sid string, pid uint) string {
	var snapshot recognitionreview.Snapshot
	err := ctl.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		snapshot, err = loadRecognitionSnapshot(ctx, tx, sid, pid, false)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ""
	}
	return snapshot.Evaluation.Fingerprint
}

// ListRecognitionReviews returns bounded diagnostic history, without running AI.
// @Summary List recognition diagnoses
// @Tags analysis
// @Produce json
// @Param session_id path string true "Owned session ID"
// @Success 200 {object} ListRecognitionReviewsResponse
// @Router /sessions/{session_id}/recognition-reviews [get]
func (ctl *Controller) ListRecognitionReviews(c *gin.Context) {
	sid, pid, ok := ctl.progressSession(c)
	if !ok {
		return
	}
	var rows []db.RecognitionReviewRun
	if err := ctl.db.WithContext(c.Request.Context()).Omit("snapshot").Where("session_id = ? AND profile_id = ?", sid, pid).Order("created_at DESC,id DESC").Limit(20).Find(&rows).Error; err != nil {
		recognitionError(c, err)
		return
	}
	fingerprint := ctl.recognitionCurrentFingerprint(c.Request.Context(), sid, pid)
	out := ListRecognitionReviewsResponse{Runs: []RecognitionReviewRunResponse{}}
	// Resolve the currently selected merged source once. A newly created
	// canonical object can supersede a legacy source that still exists.
	currentObject := ""
	var currentGeneration int64
	if len(rows) > 0 {
		if object, e := ctl.resolveTargetVideoObject(c.Request.Context(), pid, sid, "merged"); e == nil {
			currentObject = object
			if attrs, e := ctl.storageClient.ObjectAttrs(c.Request.Context(), object); e == nil && attrs != nil {
				currentGeneration = attrs.Generation
			}
		}
	}
	for _, row := range rows {
		response := recognitionRunResponse(row, fingerprint, false)
		if currentObject != row.SourceObject || currentGeneration == 0 || currentGeneration != row.SourceGeneration {
			response.Outdated = true
		}
		out.Runs = append(out.Runs, response)
		if row.Status == db.RecognitionReviewStatusQueued || row.Status == db.RecognitionReviewStatusRunning {
			id := row.ID
			out.ActiveRunID = &id
		}
	}
	c.JSON(http.StatusOK, out)
}

// GetRecognitionReview returns a stored diagnosis and its historical comparison.
// @Summary Get a recognition diagnosis
// @Tags analysis
// @Produce json
// @Param session_id path string true "Owned session ID"
// @Param run_id path int true "Diagnosis ID"
// @Success 200 {object} RecognitionReviewRunResponse
// @Failure 404 {object} map[string]string
// @Router /sessions/{session_id}/recognition-reviews/{run_id} [get]
func (ctl *Controller) GetRecognitionReview(c *gin.Context) {
	sid, pid, ok := ctl.progressSession(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("run_id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid diagnosis ID"})
		return
	}
	var row db.RecognitionReviewRun
	if err = ctl.db.WithContext(c.Request.Context()).Where("id = ? AND session_id = ? AND profile_id = ?", id, sid, pid).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "diagnosis not found"})
		} else {
			recognitionError(c, err)
		}
		return
	}
	out := recognitionRunResponse(row, ctl.recognitionCurrentFingerprint(c.Request.Context(), sid, pid), true)
	// A replaced/deleted merged object also invalidates an otherwise identical
	// analysis/confirmation snapshot. No video content is read by this check.
	object, e := ctl.resolveTargetVideoObject(c.Request.Context(), pid, sid, "merged")
	if e != nil || object != row.SourceObject {
		out.Outdated = true
	} else if attrs, e := ctl.storageClient.ObjectAttrs(c.Request.Context(), object); e != nil || attrs == nil || attrs.Generation != row.SourceGeneration {
		out.Outdated = true
	}
	c.JSON(http.StatusOK, out)
}
