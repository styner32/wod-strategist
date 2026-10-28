package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/recognitionreview"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func recognitionTestFinding(start, end float64) recognitionreview.Finding {
	return recognitionreview.Finding{StartSecs: start, EndSecs: end, Category: "occlusion", Stage: "deep_observation", Difference: "원본과 확정 횟수 차이", OriginalEvidence: "원본 반복 근거", VisualEvidence: "몸이 기구에 가려지는 구간", PossibleReason: "가림 때문에 동작 전환을 확인하지 못했을 가능성", Limitations: "확정값은 총횟수이므로 특정 반복 누락을 단정할 수 없음", EvidenceLevel: "video_supported_hypothesis"}
}

var _ = Describe("recognition review helpers", func() {
	It("queues only an authoritative run ID", func() {
		task, err := NewRecognitionReviewTask(42)
		Expect(err).NotTo(HaveOccurred())
		Expect(task.Type()).To(Equal(TypeRecognitionReview))
		Expect(string(task.Payload())).To(MatchJSON(`{"run_id":42}`))
		_, err = NewRecognitionReviewTask(0)
		Expect(err).To(HaveOccurred())
	})
	It("bounds raw nested events and explanations to the requested interval", func() {
		p := workoutprogress.Progress{Intervals: []workoutprogress.Interval{{ID: "wide", Start: 0, End: 300, Activity: "exercise", Movement: "Clean", RepEvents: []activity.Observation{{Start: 1, End: 2, Unit: "reps", Complete: true, Evidence: "outside-first"}, {Start: 11, End: 12, Unit: "reps", Complete: true, Evidence: "inside"}, {Start: 299, End: 300, Unit: "reps", Complete: true, Evidence: "outside-last"}}, FormObservations: []workoutprogress.Observation{{Start: 1, End: 2, Evidence: "outside form"}, {Start: 9, End: 11, Evidence: "inside form"}}}}, Unassessed: []workoutprogress.Gap{{Start: 0, End: 15, Clock: "media", Reason: "not_analyzed"}, {Start: 250, End: 280, Clock: "media", Reason: "outside gap"}}}
		snapshot := recognitionreview.Snapshot{Progress: p, Evaluation: recognitionreview.Evaluation{Records: []recognitionreview.RecordEvaluation{{Record: workoutprogress.Record{ID: 1, Start: 0, End: 300, Reps: 30}}}}, AnalysisOutput: "## 세그먼트 1: Clean (0:00 ~ 0:20)\ninside original\n## 최종 평가\noutside final assessment\n## 세그먼트 2: Clean (4:00 ~ 5:00)\noutside section"}
		batch := recognitionreview.Batch{RecordID: 1, StartSecs: 10, EndSecs: 20, CoreStartSecs: 10, CoreEndSecs: 20}
		input, ok := recognitionBatchInput(snapshot, batch)
		Expect(ok).To(BeTrue())
		raw, err := json.Marshal(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring("inside"))
		Expect(string(raw)).NotTo(ContainSubstring("outside"))
		Expect(input.Intervals[0].Start).To(Equal(10.0))
		Expect(input.Intervals[0].End).To(Equal(20.0))
		Expect(input.Gaps[0].Start).To(Equal(10.0))
		Expect(input.Gaps[0].End).To(Equal(15.0))
		prompt := buildRecognitionReviewPrompt(input, batch)
		Expect(prompt).To(ContainSubstring("RECORD_TOTAL"))
		Expect(prompt).To(ContainSubstring("독립 재판독"))
		Expect(prompt).To(ContainSubstring("오류를 꾸며내지"))
	})
	It("maps clip-relative evidence into owned media and rejects out-of-range claims", func() {
		batch := recognitionreview.Batch{StartSecs: 10, EndSecs: 30, CoreStartSecs: 15, CoreEndSecs: 25}
		raw, _ := json.Marshal(map[string]any{"findings": []recognitionreview.Finding{recognitionTestFinding(2, 18)}})
		findings, err := parseRecognitionFindings(string(raw), batch, 20, recognitionReviewInput{})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings[0].StartSecs).To(Equal(15.0))
		Expect(findings[0].EndSecs).To(Equal(25.0))
		raw, _ = json.Marshal(map[string]any{"findings": []recognitionreview.Finding{recognitionTestFinding(0, 21)}})
		_, err = parseRecognitionFindings(string(raw), batch, 20, recognitionReviewInput{})
		Expect(err).To(MatchError("diagnostic finding is outside its clip"))
		_, err = parseRecognitionFindings(`{"findings":[],"new_reps":5}`, batch, 20, recognitionReviewInput{})
		Expect(err).To(HaveOccurred())
		findings, err = parseRecognitionFindings(`{"findings":[]}`, batch, 20, recognitionReviewInput{})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(BeEmpty())
		_, err = parseRecognitionFindings(`{}`, batch, 20, recognitionReviewInput{})
		Expect(err).To(HaveOccurred())
		_, err = parseRecognitionFindings(`{"findings":null}`, batch, 20, recognitionReviewInput{})
		Expect(err).To(HaveOccurred())
	})
	It("maps and filters original chunk-local observation times without changing its snapshot", func() {
		doc := activity.Observations{Version: 1, DurationSecs: 10, TargetState: "identified", ActivityState: "exercise", Events: []activity.Observation{{Movement: "Clean", Unit: "reps", Start: 1, End: 2, Complete: true, Evidence: "outside-local"}, {Movement: "Clean", Unit: "reps", Start: 3, End: 4, Complete: true, Evidence: "inside-local"}}, Unassessed: []activity.Interval{}}
		raw, _ := json.Marshal(doc)
		chunk := recognitionreview.ChunkEvidence{MediaStartSecs: ptr(100.0), MediaEndSecs: ptr(120.0), MovementObservations: raw}
		bounded := boundedRecognitionChunk(chunk, recognitionreview.Batch{StartSecs: 105, EndSecs: 110})
		var mapped struct {
			Clock string `json:"clock"`
			activity.Observations
		}
		Expect(json.Unmarshal(bounded.MovementObservations, &mapped)).To(Succeed())
		Expect(mapped.Clock).To(Equal("media"))
		Expect(mapped.Events).To(HaveLen(1))
		Expect(mapped.Events[0].Start).To(Equal(106.0))
		Expect(mapped.Events[0].End).To(Equal(108.0))
		Expect(string(bounded.MovementObservations)).NotTo(ContainSubstring("outside-local"))
		Expect(string(chunk.MovementObservations)).To(ContainSubstring("outside-local"))
		// Missing local duration cannot establish a capture-to-media scale.
		doc.DurationSecs = 0
		missingDuration, err := json.Marshal(doc)
		Expect(err).NotTo(HaveOccurred())
		chunk.MovementObservations = missingDuration
		unmapped := boundedRecognitionChunk(chunk, recognitionreview.Batch{StartSecs: 105, EndSecs: 110})
		Expect(string(unmapped.MovementObservations)).To(ContainSubstring(`"diagnostic_mapping_unavailable":true`))
		Expect(string(unmapped.MovementObservations)).NotTo(ContainSubstring("inside-local"))
	})
	It("does not call a user correction an original stored fact", func() {
		finding := recognitionTestFinding(0, 1)
		finding.EvidenceLevel = "stored_fact"
		finding.OriginalEvidence = "UserCorrectedMovement"
		input := recognitionReviewInput{RecordTotal: recognitionreview.RecordEvaluation{Record: workoutprogress.Record{Movement: "UserCorrectedMovement"}}}
		raw, _ := json.Marshal(map[string]any{"findings": []recognitionreview.Finding{finding}})
		findings, err := parseRecognitionFindings(string(raw), recognitionreview.Batch{CoreEndSecs: 1}, 1, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(findings[0].EvidenceLevel).To(Equal("undetermined"))
	})
	It("keeps missing token fields unknown across attempts and batches", func() {
		batch := recognitionreview.Batch{Attempts: 1}
		addRecognitionUsage(&batch, nil)
		batch.Attempts++
		addRecognitionUsage(&batch, &gemini.TokenUsage{PromptTokens: 10, CandidateTokens: 3, TotalTokens: 13})
		Expect(batch.TotalTokens).To(BeNil())
		known := recognitionreview.Batch{Attempts: 1}
		addRecognitionUsage(&known, &gemini.TokenUsage{PromptTokens: 10, CandidateTokens: 3, TotalTokens: 13})
		input, output, total := aggregateRecognitionUsage([]recognitionreview.Batch{known, batch})
		Expect(input).To(BeNil())
		Expect(output).To(BeNil())
		Expect(total).To(BeNil())
		input, _, total = aggregateRecognitionUsage([]recognitionreview.Batch{known, {Status: "QUEUED"}})
		Expect(*input).To(Equal(int64(10)))
		Expect(*total).To(Equal(int64(13)))
	})
	It("re-encodes a precise sub-second clip instead of retaining a full keyframe group", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg unavailable")
		}
		source := createTinyMP4(GinkgoT())
		target := filepath.Join(GinkgoT().TempDir(), "bounded.mp4")
		duration, err := encodeRecognitionClip(context.Background(), source, target, 0.25, 0.75)
		Expect(err).NotTo(HaveOccurred())
		Expect(duration).To(BeNumerically("~", 0.5, 0.04))
		Expect(probeVideoDuration(context.Background(), target)).To(BeNumerically("<", 0.6))
		cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=start_time", "-of", "default=noprint_wrappers=1:nokey=1", target)
		out, err := cmd.Output()
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(Equal("0.000000"))
	})
})

var _ = Describe("HandleRecognitionReviewTask", func() {
	const base = "https://recognition.test"
	var conn *gorm.DB
	var once sync.Once
	var w *Worker
	var geminiTransport, storageTransport *testhelpers.MockTransport
	var profile db.Profile
	var session db.Session
	var original db.AnalysisResult
	var queue *asynq.Client
	var inspector *asynq.Inspector
	var snapshot recognitionreview.Snapshot

	BeforeEach(func() {
		once.Do(func() { var err error; conn, err = testhelpers.InitDB(); Expect(err).NotTo(HaveOccurred()) })
		testhelpers.CleanupDB(conn)
		geminiTransport = testhelpers.NewMockTransport()
		storageTransport = testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(err).NotTo(HaveOccurred())
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test-key", BaseURL: base, HTTPClient: &http.Client{Transport: geminiTransport}, PollInterval: time.Millisecond, Sleep: func(time.Duration) {}})
		Expect(err).NotTo(HaveOccurred())
		queue = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		testhelpers.CleanupQueue(inspector)
		w = NewWorker(conn, storageClient, "test-bucket", client, queue, zap.NewNop())
		profile = testhelpers.CreateProfile(conn, &db.Profile{})
		session = testhelpers.CreateSession(conn, &db.Session{ProfileID: profile.ID, SessionID: "WOD-20261028-01JRECOGNITIONREVIEW00", WorkoutType: "wod"})
		p := workoutprogress.Progress{Version: 1, Clock: "media", SourceVersion: "test-source", Intervals: []workoutprogress.Interval{{ID: "one", Start: 0.2, End: 0.8, Activity: "exercise", Movement: "Deadlift", ObservationRefs: []string{"segment-1:interval-0"}, RepEvents: []activity.Observation{{Movement: "Deadlift", Unit: "reps", Start: 0.2, End: 0.4, Complete: true, Evidence: "original visible cycle"}}}}, Unassessed: []workoutprogress.Gap{}}
		raw, _ := json.Marshal(p)
		original = testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", Output: "original immutable analysis", WorkoutProgress: raw})
		record := testhelpers.CreatePerformanceRecord(conn, &db.PerformanceRecord{AnalysisID: original.ID, ProfileID: profile.ID, SessionID: session.SessionID, SourceVersion: "test-source", IntervalID: "one", Movement: "Power Clean", StartSecs: 0.2, EndSecs: 0.8, Reps: 2, OriginalCandidate: db.JSONDocument(`{"movement":"Power Clean"}`)})
		snapshot = recognitionreview.Snapshot{Progress: p, Evaluation: recognitionreview.Evaluation{SourceVersion: "test-source", Records: []recognitionreview.RecordEvaluation{{Record: workoutprogress.Record{ID: record.ID, SessionID: session.SessionID, SourceVersion: "test-source", Status: "confirmed", Movement: record.Movement, Start: record.StartSecs, End: record.EndSecs, Reps: record.Reps}, Status: "mismatch"}}}}
	})
	AfterEach(func() {
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
		Expect(inspector.Close()).To(Succeed())
		Expect(queue.Close()).To(Succeed())
	})
	seedRun := func(batches []recognitionreview.Batch) db.RecognitionReviewRun {
		snapshotRaw, _ := json.Marshal(snapshot)
		batchesRaw, _ := json.Marshal(batches)
		return testhelpers.CreateRecognitionReviewRun(conn, &db.RecognitionReviewRun{SessionID: session.SessionID, ProfileID: profile.ID, SourceVersion: "test-source", SourceGeneration: 7, StartSecs: 0.2, EndSecs: 0.8, Model: gemini.ModelArgon4, Snapshot: snapshotRaw, Batches: batchesRaw})
	}
	seedVideoAndCall := func(run db.RecognitionReviewRun, text string, deleteStatus int) {
		if !hasFfmpeg() {
			Skip("ffmpeg unavailable")
		}
		body, err := os.ReadFile(createTinyMP4(GinkgoT()))
		Expect(err).NotTo(HaveOccurred())
		storageTransport.New(testhelpers.GCSBaseURL).Get("/test-bucket/" + run.SourceObject + "?generation=7").Reply(http.StatusOK).Body(body)
		geminiTransport.New(base).Post("/upload/v1beta/files").Reply(http.StatusOK).Header("X-Goog-Upload-Url", base+"/upload-session").JSON(map[string]any{})
		geminiTransport.New(base).Post("/upload-session").Reply(http.StatusOK).Header("X-Goog-Upload-Status", "final").JSON(map[string]any{"file": map[string]any{"name": "files/diagnostic", "uri": base + "/files/diagnostic"}})
		geminiTransport.New(base).Get("/v1beta/files/diagnostic").Reply(http.StatusOK).JSON(map[string]any{"name": "files/diagnostic", "uri": base + "/files/diagnostic", "state": "ACTIVE", "videoMetadata": map[string]any{"videoDuration": "0.6s"}})
		geminiTransport.New(base).Post("/v1beta/models/" + gemini.ModelArgon4 + ":generateContent").MatchBodyContains(`"fileUri":"` + base + `/files/diagnostic"`).Reply(http.StatusOK).JSON(map[string]any{"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{{"text": text}}}}}, "usageMetadata": map[string]any{"promptTokenCount": 11, "candidatesTokenCount": 7, "totalTokenCount": 18}})
		geminiTransport.New(base).Delete("/v1beta/files/diagnostic").Reply(deleteStatus).JSON(map[string]any{})
	}
	newBatches := func() []recognitionreview.Batch {
		return []recognitionreview.Batch{{ID: 1, RecordID: snapshot.Evaluation.Records[0].Record.ID, StartSecs: 0.2, EndSecs: 0.8, CoreStartSecs: 0.2, CoreEndSecs: 0.8, Status: "QUEUED", Findings: []recognitionreview.Finding{}}}
	}
	readRun := func(id uint) db.RecognitionReviewRun {
		var run db.RecognitionReviewRun
		Expect(conn.First(&run, id).Error).To(Succeed())
		return run
	}

	It("uploads only the exact clip, pins generation, translates evidence and preserves production rows", func() {
		run := seedRun(newBatches())
		reply, _ := json.Marshal(map[string]any{"findings": []recognitionreview.Finding{recognitionTestFinding(0.05, 0.3)}})
		seedVideoAndCall(run, string(reply), http.StatusOK)
		task, err := NewRecognitionReviewTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleRecognitionReviewTask(context.Background(), task)).To(Succeed())
		saved := readRun(run.ID)
		Expect(saved.Status).To(Equal("COMPLETED"))
		Expect(saved.TotalTokens).NotTo(BeNil())
		Expect(*saved.TotalTokens).To(Equal(int64(18)))
		var batches []recognitionreview.Batch
		Expect(json.Unmarshal(saved.Batches, &batches)).To(Succeed())
		Expect(batches[0].Findings[0].StartSecs).To(BeNumerically("~", 0.25))
		Expect(batches[0].GeminiFileName).To(BeEmpty())
		for _, request := range geminiTransport.Requests() {
			if strings.Contains(request.URL, ":generateContent") {
				var body map[string]any
				Expect(json.Unmarshal(request.Body, &body)).To(Succeed())
				start, end := segmentOffsetsFromRequest(body)
				Expect(start).To(Equal("0s"))
				Expect(end).To(Equal("0.600s"))
				Expect(string(request.Body)).To(ContainSubstring(`"fps":5`))
				Expect(string(request.Body)).To(ContainSubstring("RECORD_TOTAL"))
			}
			if strings.Contains(request.URL, "/upload-session") {
				clip := filepath.Join(GinkgoT().TempDir(), "uploaded.mp4")
				Expect(os.WriteFile(clip, request.Body, 0600)).To(Succeed())
				Expect(probeVideoDuration(context.Background(), clip)).To(BeNumerically("<", 0.7))
			}
		}
		var unchanged db.AnalysisResult
		Expect(conn.First(&unchanged, original.ID).Error).To(Succeed())
		Expect(unchanged.Output).To(Equal(original.Output))
		Expect(string(unchanged.WorkoutProgress)).To(MatchJSON(string(original.WorkoutProgress)))
		var confirmed db.PerformanceRecord
		Expect(conn.First(&confirmed, snapshot.Evaluation.Records[0].Record.ID).Error).To(Succeed())
		Expect(confirmed.Reps).To(Equal(2))
		Expect(confirmed.Movement).To(Equal("Power Clean"))
		var usage db.TokenUsage
		Expect(conn.Where("session_id = ? AND task_type = ?", session.SessionID, TypeRecognitionReview).First(&usage).Error).To(Succeed())
		Expect(*usage.RequestKey).To(ContainSubstring(fmt.Sprintf("recognition-review:%d:batch:1:attempt:1", run.ID)))
		Expect(w.HandleRecognitionReviewTask(context.Background(), task)).To(Succeed())
	})
	It("cleans an interrupted upload and reuses completed findings without any new video request", func() {
		batches := newBatches()
		batches[0].Status = "COMPLETED"
		batches[0].Findings = []recognitionreview.Finding{recognitionTestFinding(0.2, 0.4)}
		batches[0].GeminiFileName = "files/interrupted"
		run := seedRun(batches)
		Expect(conn.Model(&run).Update("status", "RUNNING").Error).To(Succeed())
		geminiTransport.New(base).Delete("/v1beta/files/interrupted").Reply(http.StatusNotFound).JSON(map[string]any{"error": map[string]any{"code": 404, "message": "not found", "status": "NOT_FOUND"}})
		task, _ := NewRecognitionReviewTask(run.ID)
		Expect(w.HandleRecognitionReviewTask(context.Background(), task)).To(Succeed())
		saved := readRun(run.ID)
		Expect(saved.Status).To(Equal("COMPLETED"))
		Expect(storageTransport.Requests()).To(BeEmpty())
		Expect(string(saved.Batches)).NotTo(ContainSubstring("gemini_file_name"))
	})
	It("retries failed cleanup without repeating a completed paid diagnostic call", func() {
		run := seedRun(newBatches())
		reply, _ := json.Marshal(map[string]any{"findings": []recognitionreview.Finding{recognitionTestFinding(0.05, 0.3)}})
		seedVideoAndCall(run, string(reply), http.StatusServiceUnavailable)
		task, _ := NewRecognitionReviewTask(run.ID)
		Expect(w.HandleRecognitionReviewTask(context.Background(), task)).NotTo(Succeed())
		saved := readRun(run.ID)
		Expect(saved.Status).To(Equal("QUEUED"))
		var batches []recognitionreview.Batch
		Expect(json.Unmarshal(saved.Batches, &batches)).To(Succeed())
		Expect(batches[0].Status).To(Equal("COMPLETED"))
		Expect(batches[0].Attempts).To(Equal(1))
		Expect(batches[0].GeminiFileName).To(Equal("files/diagnostic"))
		geminiTransport.New(base).Delete("/v1beta/files/diagnostic").Reply(http.StatusOK).JSON(map[string]any{})
		Expect(w.HandleRecognitionReviewTask(WithRetryCount(context.Background(), 1), task)).To(Succeed())
		saved = readRun(run.ID)
		Expect(saved.Status).To(Equal("COMPLETED"))
		Expect(*saved.TotalTokens).To(Equal(int64(18)))
	})
	It("preserves completed batches and marks an invalid remaining diagnosis as partial after final retry", func() {
		batches := newBatches()
		done := batches[0]
		done.ID = 2
		done.Status = "COMPLETED"
		done.Findings = []recognitionreview.Finding{recognitionTestFinding(0.2, 0.4)}
		batches = append([]recognitionreview.Batch{done}, batches...)
		run := seedRun(batches)
		reply, _ := json.Marshal(map[string]any{"findings": []recognitionreview.Finding{recognitionTestFinding(0, 99)}})
		seedVideoAndCall(run, string(reply), http.StatusOK)
		task, _ := NewRecognitionReviewTask(run.ID)
		Expect(w.HandleRecognitionReviewTask(WithRetryCount(context.Background(), 2), task)).To(Succeed())
		saved := readRun(run.ID)
		Expect(saved.Status).To(Equal("PARTIAL"))
		Expect(json.Unmarshal(saved.Batches, &batches)).To(Succeed())
		Expect(batches[0].Status).To(Equal("COMPLETED"))
		Expect(batches[1].Status).To(Equal("FAILED"))
		Expect(batches[1].GeminiFileName).To(BeEmpty())
	})
	It("does not execute a duplicate delivery while another worker holds its run lock", func() {
		run := seedRun(newBatches())
		task, _ := NewRecognitionReviewTask(run.ID)
		Expect(conn.Connection(func(lockConn *gorm.DB) error {
			key := fmt.Sprintf("recognition-review:%d", run.ID)
			if err := lockConn.Exec("SELECT pg_advisory_lock(hashtextextended(?,0))", key).Error; err != nil {
				return err
			}
			defer lockConn.Exec("SELECT pg_advisory_unlock(hashtextextended(?,0))", key)
			return w.HandleRecognitionReviewTask(context.Background(), task)
		})).To(Succeed())
		Expect(readRun(run.ID).Status).To(Equal("QUEUED"))
		Expect(geminiTransport.Requests()).To(BeEmpty())
		Expect(storageTransport.Requests()).To(BeEmpty())
	})
	It("rejects unowned preserved source identities before any download", func() {
		run := seedRun(newBatches())
		run.SourceGCSURI = "gs://another-bucket/" + run.SourceObject
		Expect(w.downloadRecognitionSource(context.Background(), &run, filepath.Join(GinkgoT().TempDir(), "source.mp4"))).To(MatchError("invalid preserved video identity"))
		Expect(storageTransport.Requests()).To(BeEmpty())
	})
	It("keeps the source generation query exact", func() {
		run := seedRun(newBatches())
		storageTransport.New(testhelpers.GCSBaseURL).Get("/test-bucket/" + run.SourceObject + "?generation=7").Reply(http.StatusOK).Body([]byte("pinned"))
		Expect(w.downloadRecognitionSource(context.Background(), &run, filepath.Join(GinkgoT().TempDir(), "source.mp4"))).To(Succeed())
		for _, request := range storageTransport.Requests() {
			u, err := url.Parse(request.URL)
			Expect(err).NotTo(HaveOccurred())
			Expect(u.Query().Get("generation")).To(Equal("7"))
		}
	})
})
