package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
		transport.New(base).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]string{"text": text}}}}}})
	}
	stream := func(text string) {
		body, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"toolCall": map[string]string{"toolType": "MEDIA_PROCESSING"}}, map[string]any{"toolResponse": map[string]string{"toolType": "MEDIA_PROCESSING"}}, map[string]string{"text": text}}}}}, "usageMetadata": map[string]int{"totalTokenCount": 100}})
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
		next, err := w.ScheduleAnalysisEnrichment(context.Background(), sid, true, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal(id))
		next, err = w.ScheduleAnalysisEnrichment(context.Background(), sid, true, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(next).NotTo(Equal(id))
		_, b = load()
		Expect(b.Items[0].LastSuccess).NotTo(BeNil())
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
	})
	It("rolls back enrichment state with its parent write and recovers committed unpublished work", func() {
		err := conn.Transaction(func(tx *gorm.DB) error {
			Expect(w.PrepareEnrichmentOutbox(context.Background(), tx, sid)).To(Succeed())
			return fmt.Errorf("rollback")
		})
		Expect(err).To(HaveOccurred())
		s, b := load()
		Expect(s.RunID).To(BeEmpty())
		Expect(b.RunID).To(BeEmpty())
		Expect(conn.Transaction(func(tx *gorm.DB) error { return w.PrepareEnrichmentOutbox(context.Background(), tx, sid) })).To(Succeed())
		s, b = load()
		Expect(s.Status).To(Equal("pending"))
		Expect(b.Status).To(Equal("pending"))
		w.recoverEnrichments(context.Background())
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(2))
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
