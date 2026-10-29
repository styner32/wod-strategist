package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"gorm.io/gorm"
)

var _ = Describe("Session cancellation context", func() {
	It("cancels active work within one second and fences WithoutCancel follow-ups", func() {
		var active atomic.Bool
		active.Store(true)
		w := NewWorker(nil, nil, "", nil, nil, nil)
		ctx, done, err := w.withSessionWorkContext(context.Background(), "sess-cancel", 1, true, func(context.Context) (bool, error) { return active.Load(), nil })
		Expect(err).NotTo(HaveOccurred())
		defer done()
		active.Store(false)
		Eventually(ctx.Done(), time.Second).Should(BeClosed())
		Expect(errors.Is(context.Cause(ctx), db.ErrSessionWorkCancelled)).To(BeTrue())
		Expect(checkSessionWork(context.WithoutCancel(ctx))).To(MatchError(db.ErrSessionWorkCancelled))
		Expect(sessionWorkResult(ctx, context.Canceled)).To(Succeed())
	})
	It("cancels an in-flight provider request and blocks a detached follow-up request", func() {
		started := make(chan struct{})
		cancelled := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if calls.Add(1) == 1 {
				close(started)
			}
			_, _ = io.Copy(io.Discard, request.Body)
			select {
			case <-request.Context().Done():
				close(cancelled)
			case <-release:
			}

		}))
		defer server.Close()
		defer close(release)
		client, err := gemini.NewClientWithOptions(context.Background(), nil, gemini.Options{APIKey: "test", BaseURL: server.URL, HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())
		var active atomic.Bool
		active.Store(true)
		w := NewWorker(nil, nil, "", client, nil, nil)
		ctx, done, err := w.withSessionWorkContext(context.Background(), "sess-request-cancel", 1, true, func(context.Context) (bool, error) { return active.Load(), nil })
		Expect(err).NotTo(HaveOccurred())
		defer done()
		scoped := w.scopedSessionWorker(ctx)
		results := make(chan error, 1)
		go func() {
			_, _, callErr := scoped.GeminiClient.ParseText(ctx, "observe current recording")
			results <- callErr
		}()
		Eventually(started, time.Second).Should(BeClosed())
		active.Store(false)
		Eventually(results, time.Second).Should(Receive(HaveOccurred()))
		Eventually(cancelled, time.Second).Should(BeClosed())
		_, _, err = scoped.GeminiClient.ParseText(context.WithoutCancel(ctx), "late follow-up")
		Expect(err).To(MatchError(db.ErrSessionWorkCancelled))
		Expect(calls.Load()).To(Equal(int32(1)))
	})
	It("fails closed on a failed state refresh and keeps that failure retryable", func() {
		var fail atomic.Bool
		outage := errors.New("state lookup unavailable")
		w := NewWorker(nil, nil, "", nil, nil, nil)
		ctx, done, err := w.withSessionWorkContext(context.Background(), "sess-cancel", 1, true, func(context.Context) (bool, error) {
			if fail.Load() {
				return false, outage
			}
			return true, nil
		})
		Expect(err).NotTo(HaveOccurred())
		defer done()
		fail.Store(true)
		Eventually(ctx.Done(), time.Second).Should(BeClosed())
		Expect(sessionWorkResult(ctx, context.Canceled)).To(MatchError(outage))
	})
})

var _ = Describe("Archived session processing fence", Ordered, func() {
	var conn *gorm.DB
	var w *Worker
	var profile db.Profile
	var analysis db.AnalysisResult
	const sid = "WOD-20261029-01JARCHIVECANCELLATION0000"
	BeforeAll(func() {
		var err error
		conn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		sql, err := conn.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sql.Close)
	})
	BeforeEach(func() {
		testhelpers.CleanupDB(conn)
		profile = testhelpers.CreateProfile(conn, &db.Profile{})
		analysis = testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "PENDING", Output: "original"})
		w = NewWorker(conn, nil, "test-bucket", nil, nil, nil)
	})
	stop := func() {
		now := time.Now().UTC()
		Expect(conn.Model(&analysis).Updates(map[string]any{"automatic_processing_cancelled_at": now, "archived_at": now, "status": "CANCELLED"}).Error).To(Succeed())
	}
	restore := func() { Expect(conn.Model(&analysis).Update("archived_at", nil).Error).To(Succeed()) }
	It("waits for ingress source metadata to commit before starting original work", func() {
		session := testhelpers.CreateSession(conn, &db.Session{SessionID: sid, ProfileID: profile.ID, WorkoutType: "wod", WODDescription: "original plan"})
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		published := make(chan error, 1)
		go func() {
			published <- db.WithSessionAutomaticWork(context.Background(), conn, profile.ID, sid, func(tx *gorm.DB) error {
				if err := tx.Model(&session).Update("wod_description", "committed ingress plan").Error; err != nil {
					return err
				}
				close(entered)
				<-release
				return nil
			})
		}()
		Eventually(entered, time.Second).Should(BeClosed())
		type startedWork struct {
			plan string
			err  error
		}
		started := make(chan startedWork, 1)
		go func() {
			ctx, done, err := w.withSessionWorkContext(context.Background(), sid, profile.ID, false, nil)
			defer done()
			if err != nil {
				started <- startedWork{err: err}
				return
			}
			var source db.Session
			err = conn.WithContext(ctx).First(&source, session.ID).Error
			started <- startedWork{plan: source.WODDescription, err: err}
		}()
		Consistently(started, 100*time.Millisecond).ShouldNot(Receive())
		unblock()
		Eventually(published, time.Second).Should(Receive(Succeed()))
		Eventually(started, time.Second).Should(Receive(Equal(startedWork{plan: "committed ingress plan"})))
	})
	It("rejects original work when archive commits before its entry fence is acquired", func() {
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		archived := make(chan error, 1)
		go func() {
			archived <- db.WithSessionAutomaticWork(context.Background(), conn, profile.ID, sid, func(tx *gorm.DB) error {
				now := time.Now().UTC()
				if err := tx.Model(&analysis).Updates(map[string]any{"automatic_processing_cancelled_at": now, "archived_at": now, "status": "CANCELLED"}).Error; err != nil {
					return err
				}
				close(entered)
				<-release
				return nil
			})
		}()
		Eventually(entered, time.Second).Should(BeClosed())
		started := make(chan error, 1)
		go func() {
			_, done, err := w.withSessionWorkContext(context.Background(), sid, profile.ID, false, nil)
			defer done()
			started <- err
		}()
		Consistently(started, 100*time.Millisecond).ShouldNot(Receive())
		unblock()
		Eventually(archived, time.Second).Should(Receive(Succeed()))
		Eventually(started, time.Second).Should(Receive(MatchError(db.ErrSessionWorkCancelled)))
	})
	It("drops every queued original task after immediate restoration without needing external clients", func() {
		stop()
		restore()
		generic, _ := json.Marshal(VideoAnalysisPayload{SessionID: sid, ProfileID: profile.ID, FilePath: "gs://test-bucket/videos/1/" + sid + "/chunk.mp4"})
		hardSub, _ := json.Marshal(HardSubPayload{SessionID: sid, ProfileID: profile.ID})
		activityRaw, _ := json.Marshal(activityReviewPayload{SessionID: sid, ProfileID: profile.ID})
		sensor, _ := json.Marshal(SensorTelemetryPayload{AnalysisResultID: analysis.ID, ProfileID: profile.ID})
		cases := []struct {
			kind   string
			body   []byte
			handle func(context.Context, *asynq.Task) error
		}{
			{TypeVideoAnalysis, generic, w.HandleVideoAnalysisTask},
			{TypeChunkAnalysis, generic, w.HandleChunkAnalysisTask},
			{TypeChunkAnalysisWithSession, generic, w.HandleChunkAnalysisWithSessionTask},
			{TypeMergeChunks, generic, w.HandleMergeChunksTask},
			{TypeInjuryAnalysis, generic, w.HandleInjuryAnalysisTask},
			{TypeGenerateHighlight, generic, w.HandleGenerateHighlightTask},
			{TypeVerifyHighlights, generic, w.HandleVerifyHighlightsTask},
			{TypeGenerateHardSub, hardSub, w.HandleGenerateHardSubTask},
			{TypeActivityReview, activityRaw, w.HandleActivityReviewTask},
			{TypeSensorTelemetry, sensor, w.HandleSensorTelemetryTask},
		}
		for _, tc := range cases {
			Expect(tc.handle(context.Background(), asynq.NewTask(tc.kind, tc.body))).To(Succeed(), tc.kind)
		}
		var current db.AnalysisResult
		Expect(conn.First(&current, analysis.ID).Error).To(Succeed())
		Expect(current.Status).To(Equal("CANCELLED"))
		Expect(current.Output).To(Equal("original"))
	})
	It("keeps repaired legacy manual and enrichment tasks stopped after their first restoration", func() {
		stop()
		chunk := testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{SessionID: sid, ProfileID: profile.ID, FilePath: "legacy.mp4", Status: "COMPLETED"})
		chunkRun := testhelpers.CreateChunkReanalysisRun(conn, &db.ChunkReanalysisRun{SessionID: sid, ProfileID: profile.ID, ChunkAnalysisResultID: chunk.ID, Status: "CANCELLED"})
		sessionRun := testhelpers.CreateSessionReanalysisRun(conn, &db.SessionReanalysisRun{SessionID: sid, ProfileID: profile.ID, Status: "CANCELLED"})
		recognitionRun := testhelpers.CreateRecognitionReviewRun(conn, &db.RecognitionReviewRun{SessionID: sid, ProfileID: profile.ID, Status: "CANCELLED"})
		Expect(conn.Model(&analysis).Updates(map[string]any{"analysis_summary": enrichmentJSON(AnalysisSummary{RunID: "legacy-summary", Status: "interrupted"}), "agentic_highlight_analysis": enrichmentJSON(AgenticHighlights{RunID: "legacy-agentic", Status: "interrupted"})}).Error).To(Succeed())
		restore()
		cases := []struct {
			kind    string
			id      uint
			handler func(context.Context, *asynq.Task) error
		}{
			{TypeChunkDebugReanalysis, chunkRun.ID, w.HandleChunkDebugReanalysisTask},
			{TypeSessionDebugReanalysis, sessionRun.ID, w.HandleSessionDebugReanalysisTask},
			{TypeRecognitionReview, recognitionRun.ID, w.HandleRecognitionReviewTask},
		}
		for _, tc := range cases {
			body, _ := json.Marshal(map[string]uint{"run_id": tc.id})
			Expect(tc.handler(context.Background(), asynq.NewTask(tc.kind, body))).To(Succeed())
		}
		body, _ := json.Marshal(enrichmentTask{SessionID: sid, RunID: "legacy-summary", Phase: "summary"})
		Expect(w.HandleAnalysisEnrichmentTask(context.Background(), asynq.NewTask(TypeAnalysisEnrichment, body))).To(Succeed())
		body, _ = json.Marshal(enrichmentTask{SessionID: sid, RunID: "legacy-agentic", Phase: "prepare"})
		Expect(w.HandleAnalysisEnrichmentTask(context.Background(), asynq.NewTask(TypeAnalysisEnrichment, body))).To(Succeed())
	})
	It("rejects late terminal publication even when its timeout persistence detached the cancelled context", func() {
		ctx, done, err := w.withSessionWorkContext(context.Background(), sid, profile.ID, false, nil)
		Expect(err).NotTo(HaveOccurred())
		defer done()
		stop()
		restore()
		Eventually(ctx.Done(), time.Second).Should(BeClosed())
		payload := VideoAnalysisPayload{SessionID: sid, ProfileID: profile.ID}
		persistCtx := context.WithoutCancel(ctx)
		Expect(w.persistVideoAnalysisFailed(persistCtx, payload, &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "FAILED", Output: "late failure"})).To(MatchError(db.ErrSessionWorkCancelled))
		Expect(w.persistChunkAnalysisResult(persistCtx, &db.ChunkAnalysisResult{SessionID: sid, ProfileID: profile.ID, FilePath: "late.mp4", Status: "COMPLETED"})).To(MatchError(db.ErrSessionWorkCancelled))
		var current db.AnalysisResult
		Expect(conn.First(&current, analysis.ID).Error).To(Succeed())
		Expect(current.Output).To(Equal("original"))
	})
	It("drops a stale history-based recommendation while preserving current visual analysis", func() {
		priorRevision := int64(0)
		w.stretchHistoryRevision = &priorRevision
		Expect(conn.Exec("UPDATE profiles SET workout_history_revision = workout_history_revision + 1 WHERE id = ?", profile.ID).Error).To(Succeed())
		result := &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "COMPLETED", AnalysisType: db.AnalysisTypeWOD, Output: "current independent visual observations", SessionScore: "{}", StretchRecommendations: `[{"stretch":"old source advice"}]`}
		Expect(w.persistVideoAnalysisCompleted(context.Background(), VideoAnalysisPayload{SessionID: sid, ProfileID: profile.ID}, result)).To(Succeed())
		var current db.AnalysisResult
		Expect(conn.First(&current, analysis.ID).Error).To(Succeed())
		Expect(current.Output).To(Equal("current independent visual observations"))
		Expect(current.StretchRecommendations).To(Equal("[]"))
	})
	It("retains billed token usage after cancellation", func() {
		ctx, done, err := w.withSessionWorkContext(context.Background(), sid, profile.ID, false, nil)
		Expect(err).NotTo(HaveOccurred())
		defer done()
		stop()
		Eventually(ctx.Done(), time.Second).Should(BeClosed())
		scoped := w.scopedSessionWorker(ctx)
		scoped.saveTokenUsage(sid, profile.ID, "chunk:analysis", &gemini.TokenUsage{Model: gemini.ModelFlash38, PromptTokens: 3, CandidateTokens: 2, TotalTokens: 5})
		var count int64
		Expect(conn.Table("token_usages").Where("session_id = ?", sid).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
	})
	It("permits a new manual run after restore but never overwrites a cancelled old run", func() {
		stop()
		restore()
		run := testhelpers.CreateSessionReanalysisRun(conn, &db.SessionReanalysisRun{SessionID: sid, ProfileID: profile.ID, Status: "QUEUED"})
		current := func(ctx context.Context) (bool, error) {
			var row db.SessionReanalysisRun
			err := conn.WithContext(ctx).First(&row, run.ID).Error
			return row.Status != "CANCELLED", err
		}
		ctx, done, err := w.withSessionWorkContext(context.Background(), sid, profile.ID, true, current)
		Expect(err).NotTo(HaveOccurred())
		defer done()
		Expect(conn.Model(&run).Update("status", "CANCELLED").Error).To(Succeed())
		Eventually(ctx.Done(), time.Second).Should(BeClosed())
		Expect(w.completeSessionDebugRun(context.WithoutCancel(ctx), run.ID, "COMPLETED", time.Now(), map[string]any{"output": "late result"})).To(Succeed())
		var stored db.SessionReanalysisRun
		Expect(conn.First(&stored, run.ID).Error).To(Succeed())
		Expect(stored.Status).To(Equal("CANCELLED"))
		Expect(stored.Output).NotTo(Equal("late result"))
	})
	It("does not revive interrupted enrichment on source changes and allows a fresh explicit summary", func() {
		stop()
		restore()
		oldSummary := AnalysisSummary{RunID: "old-summary", Status: "interrupted", Fingerprint: "old"}
		oldAgentic := AgenticHighlights{RunID: "old-agentic", Status: "interrupted", Fingerprint: "old"}
		Expect(conn.Model(&analysis).Updates(map[string]any{"status": "COMPLETED", "analysis_summary": enrichmentJSON(oldSummary), "agentic_highlight_analysis": enrichmentJSON(oldAgentic)}).Error).To(Succeed())
		var current db.AnalysisResult
		Expect(conn.First(&current, analysis.ID).Error).To(Succeed())
		late := oldAgentic
		late.Status = "running"
		Expect(w.saveAgenticState(context.Background(), current, late, true)).To(BeFalse())
		Expect(w.reconcileEnrichmentOnce(context.Background(), sid)).To(BeFalse())
		Expect(conn.First(&current, analysis.ID).Error).To(Succeed())
		s, b := decodeEnrichment(current)
		Expect(s.Status).To(Equal("interrupted"))
		Expect(b.Status).To(Equal("interrupted"))
		fresh, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, false, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh).NotTo(Equal(oldSummary.RunID))
		Expect(conn.First(&current, analysis.ID).Error).To(Succeed())
		s, _ = decodeEnrichment(current)
		Expect(s.Status).To(Equal("pending"))
	})
})
