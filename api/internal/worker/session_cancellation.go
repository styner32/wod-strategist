package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	gcs "cloud.google.com/go/storage"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Archive is terminal for original recording jobs. A restore does not clear the
// durable fence; separately identified manual runs can still be requested.
const sessionCancellationPollInterval = 250 * time.Millisecond

type sessionWorkContextKey struct{}
type sessionWorkGuard struct {
	database     *gorm.DB
	sessionID    string
	profileID    uint
	automatic    bool
	stillCurrent func(context.Context) (bool, error)
}

func (g *sessionWorkGuard) allowed(ctx context.Context) error {
	if g.automatic {
		if err := db.SessionAutomaticWorkAllowed(ctx, g.database, g.profileID, g.sessionID); err != nil {
			return err
		}
	} else if g.database != nil {
		var row db.AnalysisResult
		err := g.database.WithContext(ctx).Select("id, profile_id, archived_at").Where("session_id = ?", g.sessionID).First(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && (row.ArchivedAt != nil || (g.profileID != 0 && row.ProfileID != g.profileID)) {
			return db.ErrSessionWorkCancelled
		}
	}
	if g.stillCurrent != nil {
		ok, err := g.stillCurrent(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return db.ErrSessionWorkCancelled
		}
	}
	return nil
}

// The guard is also retained by WithoutCancel contexts: cancellation may allow
// accounting/cleanup, but cannot authorize a late result or a new paid request.
func checkSessionWork(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if guard, ok := ctx.Value(sessionWorkContextKey{}).(*sessionWorkGuard); ok {
		return guard.allowed(ctx)
	}
	return nil
}

func (w *Worker) withSessionWorkContext(ctx context.Context, sessionID string, profileID uint, allowCancelledOriginal bool, current func(context.Context) (bool, error)) (context.Context, context.CancelFunc, error) {
	guard := &sessionWorkGuard{w.DB, sessionID, profileID, !allowCancelledOriginal, current}
	if guard.automatic && w.DB != nil {
		// Redis can deliver an ingress task before its metadata transaction
		// commits. Wait on the same fence before any worker reads source data.
		if err := db.WithSessionAutomaticWork(ctx, w.DB, profileID, sessionID, func(*gorm.DB) error { return nil }); err != nil {
			return ctx, func() {}, err
		}
	}
	if err := guard.allowed(ctx); err != nil {
		return ctx, func() {}, err
	}
	workCtx, cancel := context.WithCancelCause(context.WithValue(ctx, sessionWorkContextKey{}, guard))
	go func() {
		ticker := time.NewTicker(sessionCancellationPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-ticker.C:
				probe, done := context.WithTimeout(context.WithoutCancel(workCtx), sessionCancellationPollInterval)
				err := guard.allowed(probe)
				done()
				if err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return workCtx, func() { cancel(context.Canceled) }, nil
}

func sessionWorkResult(ctx context.Context, err error) error {
	if errors.Is(err, db.ErrSessionWorkCancelled) || errors.Is(context.Cause(ctx), db.ErrSessionWorkCancelled) {
		return nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return err
}

func (w *Worker) scopedSessionWorker(ctx context.Context) *Worker {
	scoped := *w
	if w.DB != nil {
		scoped.DB = w.DB.WithContext(ctx)
	}
	if w.QueueClient != nil {
		scoped.QueueClient = &sessionWorkQueue{w.QueueClient, w.DB, ctx}
	}
	if w.StorageClient != nil {
		scoped.StorageClient = &sessionWorkStorage{w.StorageClient}
	}
	if w.GeminiClient != nil {
		scoped.GeminiClient = &sessionWorkGemini{w.GeminiClient}
	}
	return &scoped
}

type sessionWorkQueue struct {
	QueueClient
	database *gorm.DB
	ctx      context.Context
}

func (q *sessionWorkQueue) Enqueue(task *asynq.Task, opts ...asynq.Option) (info *asynq.TaskInfo, err error) {
	guard, ok := q.ctx.Value(sessionWorkContextKey{}).(*sessionWorkGuard)
	if !ok {
		return q.QueueClient.Enqueue(task, opts...)
	}
	if err = checkSessionWork(q.ctx); err != nil {
		return nil, err
	}
	if guard.automatic {
		err = db.WithSessionAutomaticWork(q.ctx, q.database, guard.profileID, guard.sessionID, func(tx *gorm.DB) error {
			var enqueueErr error
			info, enqueueErr = q.QueueClient.Enqueue(task, opts...)
			return enqueueErr
		})
		return info, err
	}
	return q.QueueClient.Enqueue(task, opts...)
}

type sessionWorkStorage struct{ StorageClient }

func (s *sessionWorkStorage) DownloadFile(ctx context.Context, uri, path string) error {
	if err := checkSessionWork(ctx); err != nil {
		return err
	}
	return s.StorageClient.DownloadFile(ctx, uri, path)
}
func (s *sessionWorkStorage) UploadFromFile(ctx context.Context, path, object string) (string, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", err
	}
	return s.StorageClient.UploadFromFile(ctx, path, object)
}
func (s *sessionWorkStorage) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	if err := checkSessionWork(ctx); err != nil {
		return nil, err
	}
	return s.StorageClient.ListObjects(ctx, prefix)
}
func (s *sessionWorkStorage) ObjectAttrs(ctx context.Context, object string) (*gcs.ObjectAttrs, error) {
	if err := checkSessionWork(ctx); err != nil {
		return nil, err
	}
	return s.StorageClient.ObjectAttrs(ctx, object)
}
func (s *sessionWorkStorage) NewReaderWithGeneration(ctx context.Context, object string, gen int64) (io.ReadCloser, error) {
	if err := checkSessionWork(ctx); err != nil {
		return nil, err
	}
	reader, err := s.StorageClient.NewReaderWithGeneration(ctx, object, gen)
	if err != nil {
		return nil, err
	}
	return &sessionWorkReader{ReadCloser: reader, ctx: ctx}, nil
}

type sessionWorkReader struct {
	io.ReadCloser
	ctx context.Context
}

func (r *sessionWorkReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.ReadCloser.Read(p)
}

type sessionWorkGemini struct{ GeminiClient }

func (g *sessionWorkGemini) Model() string {
	if p, ok := g.GeminiClient.(interface{ Model() string }); ok {
		return p.Model()
	}
	return ""
}

// Cleanup is allowed after archive and always gets its own bounded lifetime.
func (g *sessionWorkGemini) DeleteFile(ctx context.Context, name string) error {
	cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer done()
	return g.GeminiClient.DeleteFile(cleanup, name)
}
func (g *sessionWorkGemini) AnalyzeVideo(ctx context.Context, filePath, prompt string) (string, string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", "", nil, err
	}
	return g.GeminiClient.AnalyzeVideo(ctx, filePath, prompt)
}
func (g *sessionWorkGemini) AnalyzeVideoWithModel(ctx context.Context, filePath, prompt, model string) (string, string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", "", nil, err
	}
	return g.GeminiClient.AnalyzeVideoWithModel(ctx, filePath, prompt, model)
}
func (g *sessionWorkGemini) AnalyzeChunkVideo(ctx context.Context, filePath, prompt, model string) (string, string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", "", nil, err
	}
	return g.GeminiClient.AnalyzeChunkVideo(ctx, filePath, prompt, model)
}
func (g *sessionWorkGemini) AnalyzeChunkVideoWithImages(ctx context.Context, filePath, prompt, model string, images [][]byte) (string, string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", "", nil, err
	}
	return g.GeminiClient.AnalyzeChunkVideoWithImages(ctx, filePath, prompt, model, images)
}
func (g *sessionWorkGemini) FileExists(ctx context.Context, name string) (bool, error) {
	if err := checkSessionWork(ctx); err != nil {
		return false, err
	}
	return g.GeminiClient.FileExists(ctx, name)
}
func (g *sessionWorkGemini) FileVideoDuration(ctx context.Context, name string) (time.Duration, bool, error) {
	if err := checkSessionWork(ctx); err != nil {
		return 0, false, err
	}
	return g.GeminiClient.FileVideoDuration(ctx, name)
}
func (g *sessionWorkGemini) GenerateWorkoutMusic(ctx context.Context, model, prompt, outputPath string) (*gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return nil, err
	}
	return g.GeminiClient.GenerateWorkoutMusic(ctx, model, prompt, outputPath)
}
func (g *sessionWorkGemini) UploadVideo(ctx context.Context, filePath string) (*gemini.UploadResult, error) {
	if err := checkSessionWork(ctx); err != nil {
		return nil, err
	}
	return g.GeminiClient.UploadVideo(ctx, filePath)
}
func (g *sessionWorkGemini) UploadVideoWithObserver(ctx context.Context, filePath string, onUploaded func(*gemini.UploadResult) error) (*gemini.UploadResult, error) {
	if err := checkSessionWork(ctx); err != nil {
		return nil, err
	}
	return g.GeminiClient.UploadVideoWithObserver(ctx, filePath, onUploaded)
}
func (g *sessionWorkGemini) IndexVideo(ctx context.Context, fileURI, mimeType, prompt string) (string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", nil, err
	}
	return g.GeminiClient.IndexVideo(ctx, fileURI, mimeType, prompt)
}
func (g *sessionWorkGemini) AnalyzeSegment(ctx context.Context, fileURI, mimeType string, start, end time.Duration, prompt string) (string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", nil, err
	}
	return g.GeminiClient.AnalyzeSegment(ctx, fileURI, mimeType, start, end, prompt)
}
func (g *sessionWorkGemini) AnalyzeSegmentWithModel(ctx context.Context, fileURI, mimeType string, start, end time.Duration, prompt, model string) (string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", nil, err
	}
	return g.GeminiClient.AnalyzeSegmentWithModel(ctx, fileURI, mimeType, start, end, prompt, model)
}
func (g *sessionWorkGemini) AnalyzeSegmentWithModelAndImages(ctx context.Context, fileURI, mimeType string, start, end time.Duration, prompt, model string, images [][]byte) (string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", nil, err
	}
	return g.GeminiClient.AnalyzeSegmentWithModelAndImages(ctx, fileURI, mimeType, start, end, prompt, model, images)
}
func (g *sessionWorkGemini) AnalyzeFrameGridsWithModel(ctx context.Context, images [][]byte, mimeType, prompt, model string) (string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", nil, err
	}
	return g.GeminiClient.AnalyzeFrameGridsWithModel(ctx, images, mimeType, prompt, model)
}
func (g *sessionWorkGemini) QueryVideoFlash(ctx context.Context, fileURI, mimeType, prompt string) (string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", nil, err
	}
	return g.GeminiClient.QueryVideoFlash(ctx, fileURI, mimeType, prompt)
}
func (g *sessionWorkGemini) ParseText(ctx context.Context, prompt string) (string, *gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return "", nil, err
	}
	return g.GeminiClient.ParseText(ctx, prompt)
}
func (g *sessionWorkGemini) GenerateSpeech(ctx context.Context, text, voiceName, outputPath string) (*gemini.TokenUsage, error) {
	if err := checkSessionWork(ctx); err != nil {
		return nil, err
	}
	return g.GeminiClient.GenerateSpeech(ctx, text, voiceName, outputPath)
}
func (g *sessionWorkGemini) AnalyzeHighlightAgentic(ctx context.Context, fileURI, mimeType, prompt string) gemini.StreamComparisonResult {
	if err := checkSessionWork(ctx); err != nil {
		return gemini.StreamComparisonResult{VideoModeResult: gemini.VideoModeResult{Error: err.Error()}}
	}
	return g.GeminiClient.AnalyzeHighlightAgentic(ctx, fileURI, mimeType, prompt)
}

func (w *Worker) HandleVideoAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, p.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleVideoAnalysisTask(workCtx, t))
}

func (w *Worker) HandleChunkAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, p.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleChunkAnalysisTask(workCtx, t))
}

func (w *Worker) HandleChunkAnalysisWithSessionTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisWithSessionPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, p.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleChunkAnalysisWithSessionTask(workCtx, t))
}

func (w *Worker) HandleMergeChunksTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, p.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleMergeChunksTask(workCtx, t))
}

func (w *Worker) HandleActivityReviewTask(ctx context.Context, t *asynq.Task) error {
	var p activityReviewPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, p.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleActivityReviewTask(workCtx, t))
}

func (w *Worker) HandleInjuryAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p InjuryAnalysisPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, p.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleInjuryAnalysisTask(workCtx, t))
}

func (w *Worker) HandleGenerateHighlightTask(ctx context.Context, t *asynq.Task) error {
	var p HighlightPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	// Legacy highlight jobs can carry a stale profile. The existing handler
	// resolves the owner from the completed analysis; fence that same owner.
	profileID := p.ProfileID
	if w.DB != nil {
		var parent db.AnalysisResult
		err := w.DB.WithContext(ctx).Select("profile_id").Where("session_id = ? AND analysis_type = ? AND status = ?", p.SessionID, db.AnalysisTypeWOD, "COMPLETED").Order("created_at DESC").First(&parent).Error
		if err == nil {
			profileID = parent.ProfileID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, profileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleGenerateHighlightTask(workCtx, t))
}

func (w *Worker) HandleVerifyHighlightsTask(ctx context.Context, t *asynq.Task) error {
	var p VerifyHighlightsPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, uint(0), false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleVerifyHighlightsTask(workCtx, t))
}

func (w *Worker) HandleGenerateHardSubTask(ctx context.Context, t *asynq.Task) error {
	var p HardSubPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, p.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleGenerateHardSubTask(workCtx, t))
}

func (w *Worker) HandleSensorTelemetryTask(ctx context.Context, t *asynq.Task) error {
	var p SensorTelemetryPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}
	var parent db.AnalysisResult
	if w.DB == nil {
		return w.handleSensorTelemetryTask(ctx, t)
	}
	if err := w.DB.WithContext(ctx).Select("session_id, profile_id").First(&parent, p.AnalysisResultID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	workCtx, done, err := w.withSessionWorkContext(ctx, parent.SessionID, parent.ProfileID, false, nil)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleSensorTelemetryTask(workCtx, t))
}

func (w *Worker) HandleChunkDebugReanalysisTask(ctx context.Context, t *asynq.Task) error {
	var payload struct {
		RunID uint `json:"run_id"`
	}
	if t == nil || json.Unmarshal(t.Payload(), &payload) != nil || payload.RunID == 0 {
		return asynq.SkipRetry
	}
	if w.DB == nil {
		return w.handleChunkDebugReanalysisTask(ctx, t)
	}
	var run db.ChunkReanalysisRun
	if err := w.DB.WithContext(ctx).First(&run, payload.RunID).Error; err != nil {
		return err
	}
	if run.Status == "CANCELLED" {
		return nil
	}
	current := func(probe context.Context) (bool, error) {
		var status string
		err := w.DB.WithContext(probe).Model(&db.ChunkReanalysisRun{}).Select("status").Where("id = ?", payload.RunID).Scan(&status).Error
		return status != "" && status != "CANCELLED", err
	}
	workCtx, done, err := w.withSessionWorkContext(ctx, run.SessionID, run.ProfileID, true, current)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleChunkDebugReanalysisTask(workCtx, t))
}

func (w *Worker) HandleSessionDebugReanalysisTask(ctx context.Context, t *asynq.Task) error {
	var payload struct {
		RunID uint `json:"run_id"`
	}
	if t == nil || json.Unmarshal(t.Payload(), &payload) != nil || payload.RunID == 0 {
		return asynq.SkipRetry
	}
	if w.DB == nil {
		return w.handleSessionDebugReanalysisTask(ctx, t)
	}
	var run db.SessionReanalysisRun
	if err := w.DB.WithContext(ctx).First(&run, payload.RunID).Error; err != nil {
		return err
	}
	if run.Status == "CANCELLED" {
		return nil
	}
	current := func(probe context.Context) (bool, error) {
		var status string
		err := w.DB.WithContext(probe).Model(&db.SessionReanalysisRun{}).Select("status").Where("id = ?", payload.RunID).Scan(&status).Error
		return status != "" && status != "CANCELLED", err
	}
	workCtx, done, err := w.withSessionWorkContext(ctx, run.SessionID, run.ProfileID, true, current)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleSessionDebugReanalysisTask(workCtx, t))
}

func (w *Worker) HandleRecognitionReviewTask(ctx context.Context, t *asynq.Task) error {
	var payload struct {
		RunID uint `json:"run_id"`
	}
	if t == nil || json.Unmarshal(t.Payload(), &payload) != nil || payload.RunID == 0 {
		return asynq.SkipRetry
	}
	if w.DB == nil {
		return w.handleRecognitionReviewTask(ctx, t)
	}
	var run db.RecognitionReviewRun
	if err := w.DB.WithContext(ctx).First(&run, payload.RunID).Error; err != nil {
		return err
	}
	if run.Status == "CANCELLED" {
		return nil
	}
	current := func(probe context.Context) (bool, error) {
		var status string
		err := w.DB.WithContext(probe).Model(&db.RecognitionReviewRun{}).Select("status").Where("id = ?", payload.RunID).Scan(&status).Error
		return status != "" && status != "CANCELLED", err
	}
	workCtx, done, err := w.withSessionWorkContext(ctx, run.SessionID, run.ProfileID, true, current)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleRecognitionReviewTask(workCtx, t))
}

// Publish under the same parent lock as archive. Manual runs deliberately ignore
// the permanent original-job fence, but still require their own current run.
func (w *Worker) withSessionPublication(ctx context.Context, profileID uint, sessionID string, fn func(*gorm.DB) error) error {
	guard, manual := ctx.Value(sessionWorkContextKey{}).(*sessionWorkGuard)
	if !manual || guard.automatic {
		return db.WithSessionAutomaticWork(ctx, w.DB, profileID, sessionID, fn)
	}
	return w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent db.AnalysisResult
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id = ?", sessionID).First(&parent).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && (parent.ArchivedAt != nil || (profileID != 0 && parent.ProfileID != profileID)) {
			return db.ErrSessionWorkCancelled
		}
		if err = guard.allowed(ctx); err != nil {
			return err
		}
		return fn(tx)
	})
}

func (w *Worker) HandleAnalysisEnrichmentTask(ctx context.Context, t *asynq.Task) error {
	var p enrichmentTask
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil {
		return asynq.SkipRetry
	}
	if p.Phase == "cleanup" {
		return w.handleAnalysisEnrichmentTask(ctx, t)
	}
	if w.DB == nil {
		return w.handleAnalysisEnrichmentTask(ctx, t)
	}
	var a db.AnalysisResult
	if err := w.DB.WithContext(ctx).Where("session_id = ?", p.SessionID).First(&a).Error; err != nil {
		return err
	}
	current := func(probe context.Context) (bool, error) {
		var row db.AnalysisResult
		if err := w.DB.WithContext(probe).Select("analysis_summary, agentic_highlight_analysis").Where("id = ?", a.ID).First(&row).Error; err != nil {
			return false, err
		}
		s, b := decodeEnrichment(row)
		if p.Phase == "summary" {
			return s.RunID == p.RunID && activeEnrichment(s.Status), nil
		}
		if b.RunID != p.RunID || !activeEnrichment(b.Status) {
			return false, nil
		}
		if p.Phase == "highlight" && (p.Index < 0 || p.Index >= len(b.Items) || (b.Items[p.Index].Status != "pending" && b.Items[p.Index].Status != "running")) {
			return false, nil
		}
		return true, nil
	}
	workCtx, done, err := w.withSessionWorkContext(ctx, p.SessionID, a.ProfileID, true, current)
	if err != nil {
		return sessionWorkResult(workCtx, err)
	}
	defer done()
	return sessionWorkResult(workCtx, w.scopedSessionWorker(workCtx).handleAnalysisEnrichmentTask(workCtx, t))
}
