package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wod-strategist/api/internal/cost"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ = Describe("Analysis enrichment lifecycle", func() {
	var conn *gorm.DB
	var w *Worker
	var transport *testhelpers.MockTransport
	var queue *asynq.Client
	var inspector *asynq.Inspector
	var a db.AnalysisResult
	const base = "https://generativelanguage.googleapis.com"
	const sid = "WOD-20260928-01JENRICHMENTTEST000000000"
	load := func() (AnalysisSummary, AgenticHighlights) {
		var row db.AnalysisResult
		Expect(conn.First(&row, a.ID).Error).To(Succeed())
		return decodeEnrichment(row)
	}
	task := func(run, phase string, index int) {
		raw, _ := json.Marshal(enrichmentTask{SessionID: sid, RunID: run, Phase: phase, Index: index})
		Expect(w.HandleAnalysisEnrichmentTask(context.Background(), asynq.NewTask(TypeAnalysisEnrichment, raw))).To(Succeed())
	}
	summary := func(text string) {
		transport.New(base).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(200).JSON(map[string]any{"usageMetadata": map[string]int{"promptTokenCount": 20, "candidatesTokenCount": 5, "thoughtsTokenCount": 10, "totalTokenCount": 35}, "candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]string{"text": text}}}}}})
	}
	stream := func(text string) {
		body, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"toolCall": map[string]string{"toolType": "MEDIA_PROCESSING"}}, map[string]any{"toolResponse": map[string]string{"toolType": "MEDIA_PROCESSING"}}, map[string]string{"text": text}}}}}, "usageMetadata": map[string]int{"promptTokenCount": 10, "candidatesTokenCount": 5, "thoughtsTokenCount": 20, "toolUsePromptTokenCount": 65, "totalTokenCount": 100}})
		transport.New(base).Post("/v1beta/models/"+gemini.ModelFlash38+":streamGenerateContent?alt=sse").MatchBodyContains(`"thinkingLevel":"HIGH"`).MatchBodyContains(`"mediaResolution":"MEDIA_RESOLUTION_LOW"`).MatchBodyContains(`"fileUri":"https://example.test/shared"`).Reply(200).Header("Content-Type", "text/event-stream").Body([]byte("data: " + string(body) + "\n\n"))
	}
	BeforeAll(func() {
		var err error
		conn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		queue = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
	})
	AfterAll(func() { _ = queue.Close(); _ = inspector.Close(); sql, _ := conn.DB(); _ = sql.Close() })
	BeforeEach(func() {
		testhelpers.CleanupDB(conn)
		testhelpers.CleanupQueue(inspector)
		transport = testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}, ThinkingLevel: "LOW"})
		Expect(err).NotTo(HaveOccurred())
		storage, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		w = NewWorker(conn, storage, "test-bucket", client, queue, zap.NewNop())
		w.AgenticHighlightsEnabled = true
		p := testhelpers.CreateProfile(conn, &db.Profile{})
		testhelpers.CreateSession(conn, &db.Session{SessionID: sid, ProfileID: p.ID, WorkoutType: "wod"})
		expiry := time.Now().Add(time.Hour)
		a = testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{SessionID: sid, ProfileID: p.ID, Status: "COMPLETED", Output: "original evidence", HighlightSegments: `[{"start":"00:10","end":"00:15","type":"best_form","movement":"DoNotInject","reason":"DoNotInject","observations":[{"start":"00:11","end":"00:12","type":"positive_form","reason":"DoNotInject"}]},{"start":"00:20","end":"00:25","type":"worst_form"}]`, GeminiFileURI: "https://example.test/shared", GeminiFileName: "files/shared", GeminiMIMEType: "video/mp4", GeminiFileExpiresAt: &expiry})
	})
	AfterEach(func() { Expect(transport.Verify()).To(Succeed()) })
	It("shares one file, independently streams each highlight, preserves success through failure, and enriches the base summary once", func() {
		id, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		again, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(id))
		s, b := load()
		Expect(b.Items).To(HaveLen(2))
		Expect(agenticHighlightPrompt(b, b.Items[0])).NotTo(ContainSubstring("DoNotInject"))
		summary(`{"overview":"기존 요약","strengths":["강점"],"improvements":[],"limitations":[]}`)
		task(s.RunID, "summary", 0)
		transport.New(base).Get("/v1beta/files/shared").Reply(200).JSON(map[string]any{"name": "files/shared", "state": "ACTIVE", "videoMetadata": map[string]string{"videoDuration": "60s"}})
		task(id, "prepare", 0)
		stream(`{"target_status":"confirmed","activity":"exercise","movement":"Snatch","direct_observation":"바벨 캐치","evidence":[{"start":11,"end":12,"observation":"안정적"}],"continuity":"continuous","noteworthy":["좋은 자세"],"limitations":[]}`)
		task(id, "highlight", 0)
		task(id, "highlight", 0) // duplicate delivery must not generate
		stream(`{"target_status":"confirmed","activity":"exercise","movement":"Snatch","direct_observation":"wrong clock","evidence":[{"start":80,"end":90,"observation":"invalid"}],"continuity":"continuous"}`)
		task(id, "highlight", 1)
		s, b = load()
		Expect(b.Status).To(Equal("partial"))
		Expect(b.Items[0].LastSuccess).NotTo(BeNil())
		Expect(b.Items[1].Status).To(Equal("failed"))
		Expect(s.Stage).To(Equal("agentic"))
		Expect(s.Result.Overview).To(Equal("기존 요약"))
		Expect(s.Failed).To(Equal(1))
		summary(`{"overview":"추가 관찰 반영","strengths":[],"improvements":[],"limitations":["일부 구간 실패"]}`)
		task(s.RunID, "summary", 0)
		var row db.AnalysisResult
		Expect(conn.First(&row, a.ID).Error).To(Succeed())
		Expect(row.Output).To(Equal(a.Output))
		Expect(row.HighlightSegments).To(Equal(a.HighlightSegments))
		Expect(row.Verified).To(BeNil())
		var usages []db.TokenUsage
		Expect(conn.Where("session_id = ?", sid).Order("id").Find(&usages).Error).To(Succeed())
		Expect(usages).To(HaveLen(4)) // two summaries, success + invalid highlight, no duplicate
		totals := cost.CalculateSessionCost(sid, usages)
		Expect(totals.TotalTokens).To(Equal(int64(270)))
		Expect(totals.ThinkingTokens).To(Equal(int64(60)))
		Expect(totals.ToolUseTokens).To(Equal(int64(130)))
		Expect(totals.UnmeasuredCalls).To(BeZero())

		next, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal(id))
		next, err = w.ScheduleAnalysisEnrichment(context.Background(), sid, true, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(next).NotTo(Equal(id))
		_, b = load()
		Expect(b.Items[0].LastSuccess).NotTo(BeNil())
		// A real rerun is a new billable request even with the same source/key.
		b.FileURI = "https://example.test/shared"
		b.MIMEType = "video/mp4"
		stream(`{}`)
		w.generateAgenticHighlight(context.Background(), a, b, 0)
		Expect(conn.Where("session_id = ?", sid).Find(&usages).Error).To(Succeed())
		Expect(usages).To(HaveLen(5))
	})
	It("records usage even when a completed response is stale and preserves missing metrics", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		_, b := load()
		Expect(conn.Model(&a).UpdateColumn("highlight_segments", `[]`).Error).To(Succeed())
		b.FileURI = "https://example.test/shared"
		b.MIMEType = "video/mp4"
		stream(`{}`)
		w.generateAgenticHighlight(context.Background(), a, b, 0)
		var usages []db.TokenUsage
		Expect(conn.Where("session_id = ?", sid).Find(&usages).Error).To(Succeed())
		Expect(usages).To(HaveLen(1))
		Expect(usages[0].TotalTokens).To(Equal(int64(100)))
		// Retrying the persistence step must not double-count a generated request.
		w.saveTokenUsageForRequest(sid, a.ProfileID, "highlight:agentic", *usages[0].RequestKey, gemini.UsageFromComparison(nil, gemini.ModelFlash38))
		Expect(conn.Where("session_id = ?", sid).Find(&usages).Error).To(Succeed())
		Expect(usages).To(HaveLen(1))
	})
	It("backfills only retained Agentic metrics without duplicating prior records", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		_, b := load()
		total := int64(5000000000)
		b.Items[0].Metrics = &gemini.VideoModeResult{StartedAt: time.Now(), Usage: &gemini.ComparisonUsage{Total: &total}}
		Expect(conn.Model(&a).UpdateColumn("agentic_highlight_analysis", enrichmentJSON(b)).Error).To(Succeed())
		sql, err := os.ReadFile("../../scripts/backfill-agentic-token-usages.sql")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Exec(string(sql)).Error).To(Succeed())
		Expect(conn.Exec(string(sql)).Error).To(Succeed())
		var usages []db.TokenUsage
		Expect(conn.Where("session_id = ?", sid).Find(&usages).Error).To(Succeed())
		Expect(usages).To(HaveLen(1))
		Expect(usages[0].TotalTokens).To(Equal(total))
		Expect(cost.CalculateSessionCost(sid, usages).UnmeasuredCalls).To(Equal(int64(1)))
	})

	It("journals a newly uploaded file before polling and cleans only that upload on preparation failure", func() {
		Expect(conn.Model(&a).Updates(map[string]any{"gemini_file_uri": "", "gemini_file_name": ""}).Error).To(Succeed())
		id, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		object := fmt.Sprintf("videos/%d/%s/merged.mp4", a.ProfileID, sid)
		testhelpers.MockGCSListObjects(transport, "test-bucket", object, []string{object})
		testhelpers.MockGCSDownload(transport, "gs://test-bucket/"+object)
		transport.New(base).Post("/upload/v1beta/files").Reply(200).Header("X-Goog-Upload-Url", base+"/upload-session").JSON(map[string]any{})
		transport.New(base).Post("/upload-session").Reply(200).Header("X-Goog-Upload-Status", "final").JSON(map[string]any{"file": map[string]any{"name": "files/owned", "uri": "https://example.test/owned", "mimeType": "video/mp4"}})
		transport.New(base).Get("/v1beta/files/owned").Reply(200).JSON(map[string]any{"name": "files/owned", "state": "FAILED"})
		task(id, "prepare", 0)
		s, b := load()
		Expect(b.Status).To(Equal("failed"))
		Expect(b.OwnedUpload).To(BeTrue())
		Expect(b.CleanupPending).To(BeTrue())
		Expect(s.Failed).To(Equal(2))
		transport.New(base).Delete("/v1beta/files/owned").Reply(200).JSON(map[string]any{})
		task(id, "cleanup", 0)
		_, b = load()
		Expect(b.CleanupPending).To(BeFalse())
		Expect(b.OwnedUpload).To(BeFalse())
	})
	It("keeps all failed highlights terminal without queuing an enriched summary", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		s, b := load()
		s.Status = "completed"
		s.Result = &SummaryContent{Overview: "base"}
		s.LastSuccess = s.Result
		b.Status = "running"
		for i := range b.Items {
			b.Items[i].Status = "failed"
		}
		Expect(conn.Model(&a).Updates(map[string]any{"analysis_summary": enrichmentJSON(s), "agentic_highlight_analysis": enrichmentJSON(b)}).Error).To(Succeed())
		w.reconcileEnrichment(context.Background(), sid)
		s, b = load()
		Expect(b.Status).To(Equal("failed"))
		Expect(s.Stage).To(Equal("base"))
		Expect(s.Status).To(Equal("completed"))
		Expect(s.Result.Overview).To(Equal("base"))
		Expect(s.Failed).To(Equal(2))
	})
	It("recovers a claimed interrupted generation without calling it again and proceeds with the next window", func() {
		id, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		s, b := load()
		old := time.Now().Add(-14 * time.Minute)
		b.Status = "running"
		b.Items[0].Status = "running"
		b.Items[0].StartedAt = &old
		s.Status = "completed"
		Expect(conn.Model(&a).Updates(map[string]any{"agentic_highlight_analysis": enrichmentJSON(b), "analysis_summary": enrichmentJSON(s)}).Error).To(Succeed())
		w.reconcileEnrichment(context.Background(), sid)
		_, b = load()
		Expect(b.Items[0].Status).To(Equal("interrupted"))
		Expect(b.Items[1].Status).To(Equal("pending"))
		task(id, "highlight", 0)
	})
	It("rejects stale output and does not link previous successes to new highlights", func() {
		id, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		_, b := load()
		b.Items[0].LastSuccess = &AgenticObservation{Movement: "old"}
		Expect(conn.Model(&a).Update("highlight_segments", `[{"start":"00:30","end":"00:35","type":"best_form"}]`).Error).To(Succeed())
		Expect(w.saveAgenticState(context.Background(), a, b, true)).To(BeFalse())
		w.reconcileEnrichment(context.Background(), sid)
		_, b = load()
		Expect(b.RunID).NotTo(Equal(id))
		Expect(b.Items).To(HaveLen(1))
		Expect(b.Items[0].LastSuccess).To(BeNil())
		task(id, "highlight", 0)
	})
	It("preserves the last successful summary when text generation fails", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, false, false)
		Expect(err).NotTo(HaveOccurred())
		s, _ := load()
		s.Result = &SummaryContent{Overview: "keep"}
		s.LastSuccess = s.Result
		Expect(conn.Model(&a).Update("analysis_summary", enrichmentJSON(s)).Error).To(Succeed())
		summary("not json")
		task(s.RunID, "summary", 0)
		s, _ = load()
		Expect(s.Status).To(Equal("failed"))
		Expect(s.Result.Overview).To(Equal("keep"))
		var usages []db.TokenUsage
		Expect(conn.Where("task_type = ?", "analysis:summary").Find(&usages).Error).To(Succeed())
		Expect(usages).To(HaveLen(1))
		Expect(usages[0].TotalTokens).To(Equal(int64(35)))

	})
	It("rolls back enrichment state with its parent write and recovers committed unpublished work", func() {
		err := conn.Transaction(func(tx *gorm.DB) error {
			w.PrepareEnrichmentOutbox(context.Background(), tx, sid)
			return fmt.Errorf("rollback")
		})
		Expect(err).To(HaveOccurred())
		s, b := load()
		Expect(s.RunID).To(BeEmpty())
		Expect(b.RunID).To(BeEmpty())
		Expect(conn.Transaction(func(tx *gorm.DB) error {
			w.PrepareEnrichmentOutbox(context.Background(), tx, sid)
			return nil
		})).To(Succeed())
		s, b = load()
		Expect(s.Status).To(Equal("pending"))
		Expect(b.Status).To(Equal("pending"))
		w.recoverEnrichments(context.Background())
		w.recoverEnrichments(context.Background()) // repeated passes must not duplicate queued work
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(2))
	})
	It("never rolls back the parent write when the optional outbox fails", func() {
		Expect(conn.Model(&a).UpdateColumn("status", "PENDING").Error).To(Succeed())
		Expect(conn.Transaction(func(tx *gorm.DB) error {
			w.PrepareEnrichmentOutbox(context.Background(), tx, sid) // no COMPLETED row: schedule fails
			return tx.Model(&db.AnalysisResult{}).Where("id = ?", a.ID).Update("status", "COMPLETED").Error
		})).To(Succeed())
		var row db.AnalysisResult
		Expect(conn.First(&row, a.ID).Error).To(Succeed())
		Expect(row.Status).To(Equal("COMPLETED"))
	})
	It("restarts stale state whose source reverted instead of recursing", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		s, b := load()
		oldSummary, oldAgentic := s.RunID, b.RunID
		// Marked stale by a source change that was later reverted: fingerprints match again.
		s.Status, b.Status = "stale", "stale"
		Expect(conn.Model(&a).UpdateColumns(map[string]any{"analysis_summary": enrichmentJSON(s), "agentic_highlight_analysis": enrichmentJSON(b)}).Error).To(Succeed())
		w.reconcileEnrichment(context.Background(), sid)
		s, b = load()
		Expect(s.Status).To(Equal("pending"))
		Expect(s.RunID).NotTo(Equal(oldSummary))
		Expect(b.Status).To(Equal("pending"))
		Expect(b.RunID).NotTo(Equal(oldAgentic))
	})
	It("marks a preparation failure stale when the source changed, so the new source restarts", func() {
		id, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		_, b := load()
		b.Status = "preparing"
		Expect(conn.Model(&a).UpdateColumn("agentic_highlight_analysis", enrichmentJSON(b)).Error).To(Succeed())
		Expect(conn.Model(&a).UpdateColumn("highlight_segments", `[{"start":"00:30","end":"00:35","type":"best_form"}]`).Error).To(Succeed())
		transport.New(base).Get("/v1beta/files/shared").Reply(400).JSON(map[string]any{"error": map[string]any{"code": 400, "message": "bad", "status": "INVALID_ARGUMENT"}})
		w.prepareAgenticHighlights(context.Background(), a, b)
		_, b = load()
		Expect(b.Status).To(Equal("stale"))
		w.reconcileEnrichment(context.Background(), sid)
		_, b = load()
		Expect(b.RunID).NotTo(Equal(id))
		Expect(b.Status).To(Equal("pending"))
		Expect(b.Items).To(HaveLen(1))
	})
	It("keeps past results current when only the profile appearance changes", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		before := w.buildTargetPersonContext(a.ProfileID, sid)
		Expect(conn.Model(&db.Profile{}).Where("id = ?", a.ProfileID).UpdateColumn("appearance", db.JSONDocument(`{"appearance":"red shirt"}`)).Error).To(Succeed())
		Expect(w.buildTargetPersonContext(a.ProfileID, sid)).NotTo(Equal(before))
		var row db.AnalysisResult
		Expect(conn.First(&row, a.ID).Error).To(Succeed())
		Expect(w.AgenticSourceCurrent(row)).To(BeTrue())
	})
	It("does not regenerate the summary when only highlights change", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		s, b := load()
		s.Status = "completed"
		s.Result = &SummaryContent{Overview: "base"}
		Expect(conn.Model(&a).UpdateColumn("analysis_summary", enrichmentJSON(s)).Error).To(Succeed())
		Expect(conn.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&db.AnalysisResult{}).Where("id = ?", a.ID).Update("highlight_segments", `[{"start":"00:30","end":"00:35","type":"best_form"}]`).Error; err != nil {
				return err
			}
			w.PrepareEnrichmentOutbox(context.Background(), tx, sid)
			return nil
		})).To(Succeed())
		w.PublishEnrichmentOutbox(context.Background(), sid)
		after, agentic := load()
		Expect(after.RunID).To(Equal(s.RunID))
		Expect(after.Status).To(Equal("completed"))
		Expect(agentic.RunID).NotTo(Equal(b.RunID))
		Expect(agentic.Status).To(Equal("pending"))
	})
	It("accepts fenced JSON from text generation", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, false, false)
		Expect(err).NotTo(HaveOccurred())
		s, _ := load()
		summary("```json\n{\"overview\":\"fenced\",\"strengths\":[],\"improvements\":[],\"limitations\":[]}\n```")
		task(s.RunID, "summary", 0)
		s, _ = load()
		Expect(s.Status).To(Equal("completed"))
		Expect(s.Result.Overview).To(Equal("fenced"))
	})
	It("treats a missing file as cleaned up and rejects forced reruns until then", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		_, b := load()
		b.Status = "failed"
		b.FileName = "files/owned"
		b.OwnedUpload = true
		b.CleanupPending = true
		Expect(conn.Model(&a).UpdateColumn("agentic_highlight_analysis", enrichmentJSON(b)).Error).To(Succeed())
		_, err = w.ScheduleAnalysisEnrichment(context.Background(), sid, true, true)
		Expect(err).To(MatchError(ErrEnrichmentCleanupPending))
		transport.New(base).Delete("/v1beta/files/owned").Reply(403).JSON(map[string]any{"error": map[string]any{"code": 403, "message": "You do not have permission to access the File owned or it may not exist.", "status": "PERMISSION_DENIED"}})
		task(b.RunID, "cleanup", 0)
		_, b = load()
		Expect(b.CleanupPending).To(BeFalse())
		next, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(next).NotTo(Equal(b.RunID))
	})
	It("retries a failed cleanup with backoff and abandons it after the attempt limit", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		_, b := load()
		b.Status = "failed"
		b.FileName = "files/owned"
		b.OwnedUpload = true
		b.CleanupPending = true
		Expect(conn.Model(&a).UpdateColumn("agentic_highlight_analysis", enrichmentJSON(b)).Error).To(Succeed())
		denied := map[string]any{"error": map[string]any{"code": 400, "message": "bad", "status": "FAILED_PRECONDITION"}}
		transport.New(base).Delete("/v1beta/files/owned").Reply(400).JSON(denied)
		task(b.RunID, "cleanup", 0)
		task(b.RunID, "cleanup", 0) // the same attempt is claimed once
		_, b = load()
		Expect(b.CleanupPending).To(BeTrue())
		Expect(b.CleanupAttempts).To(Equal(1))
		scheduled, err := inspector.ListScheduledTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(scheduled).To(HaveLen(1))
		b.CleanupAttempts = maxEnrichmentCleanupAttempts - 1
		Expect(conn.Model(&a).UpdateColumn("agentic_highlight_analysis", enrichmentJSON(b)).Error).To(Succeed())
		transport.New(base).Delete("/v1beta/files/owned").Reply(400).JSON(denied)
		task(b.RunID, "cleanup", maxEnrichmentCleanupAttempts-1)
		_, b = load()
		Expect(b.CleanupPending).To(BeFalse())
	})
	It("leaves stale state that cannot restart untouched during recovery", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupQueue(inspector)
		s, b := load()
		s.Status = "stale"
		b.Status = "stale"
		Expect(conn.Model(&a).UpdateColumns(map[string]any{"status": "FAILED", "analysis_summary": enrichmentJSON(s), "agentic_highlight_analysis": enrichmentJSON(b)}).Error).To(Succeed())
		var before db.AnalysisResult
		Expect(conn.First(&before, a.ID).Error).To(Succeed())
		w.recoverEnrichments(context.Background())
		var after db.AnalysisResult
		Expect(conn.First(&after, a.ID).Error).To(Succeed())
		Expect(string(after.AnalysisSummary)).To(Equal(string(before.AnalysisSummary)))
		Expect(string(after.AgenticHighlightAnalysis)).To(Equal(string(before.AgenticHighlightAnalysis)))
		Expect(after.UpdatedAt).To(Equal(before.UpdatedAt))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})
	It("terminates pending Agentic work when disabled without generating", func() {
		_, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		w.AgenticHighlightsEnabled = false
		w.reconcileEnrichment(context.Background(), sid)
		_, b := load()
		Expect(b.Status).To(Equal("interrupted"))
		Expect(b.Items[0].Status).To(Equal("interrupted"))
	})
	It("does not expose raw responses or thought text in persisted metrics", func() {
		b := AgenticHighlights{Items: []AgenticHighlight{{Metrics: &gemini.VideoModeResult{Text: "secret thought", Request: []byte("api-key"), Response: []byte("raw SSE")}}}}
		raw := string(enrichmentJSON(b))
		Expect(strings.Contains(raw, "secret thought")).To(BeFalse())
		Expect(raw).NotTo(ContainSubstring("api-key"))
		Expect(raw).NotTo(ContainSubstring("raw SSE"))
	})
}, Ordered)
