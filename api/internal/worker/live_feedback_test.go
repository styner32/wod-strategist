package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func liveTestObservations(start, end float64) db.NullableJSONDocument {
	raw, _ := json.Marshal(activity.Observations{DurationSecs: 10, Version: 1, TargetState: "identified", ActivityState: "exercise", Events: []activity.Observation{{Movement: "Air Squat", Unit: "reps", Start: start, End: end, Complete: true, Evidence: "descends and stands"}}, Unassessed: []activity.Interval{}})
	return raw
}
func liveTestRow(id uint, start, end float64) db.ChunkAnalysisResult {
	return db.ChunkAnalysisResult{ID: id, ProfileID: 1, SessionID: "s", FilePath: fmt.Sprintf("chunk%d.mp4", id), Status: "COMPLETED", ExerciseType: "Air Squat", Output: "current cue", ObservedSignals: `{"form_issues_seen":["knee position"]}`, TargetConfidence: 0.9, StartSecs: &start, EndSecs: &end, MediaStartSecs: &start, MediaEndSecs: &end, MovementObservations: liveTestObservations(1, 3)}
}

var _ = Describe("Live coaching evidence window", func() {
	It("keeps differently cased capture metadata out of visible coaching", func() {
		w := &Worker{CaptureFeedbackEnabled: true}
		raw := "[NO_EXERCISE]\n```CAPTURE_ASSESSMENT\n" + `{"state":"needs_adjustment","issue":"dark","evidence":"body silhouette indistinct","advice":{"ko":"조명을 밝혀주세요","en":"Improve lighting"}}` + "\n```"
		row := db.ChunkAnalysisResult{}
		w.parseLiveFeedback(raw, 1, 0, &row)
		Expect(string(row.CaptureAssessment)).To(ContainSubstring("Improve lighting"))
		Expect(strings.TrimSpace(liveBlocks.ReplaceAllString(raw, ""))).To(Equal("[NO_EXERCISE]"))
	})
	It("uses at most six previous chunks inside sixty seconds in capture order", func() {
		current := liveTestRow(99, 80, 90)
		rows := []db.ChunkAnalysisResult{}
		for i := 0; i < 8; i++ {
			rows = append(rows, liveTestRow(uint(i+1), float64(i*10), float64(i*10+10)))
		}
		sources := recentCoachingSources(current, rows)
		Expect(sources).To(HaveLen(6))
		Expect(sources[0].Start).To(Equal(20.0))
		Expect(sources[5].End).To(Equal(80.0))
	})
	It("does not wait, bridge missing/rest evidence or use another athlete/session", func() {
		current := liveTestRow(4, 30, 40)
		prior := liveTestRow(3, 20, 30)
		Expect(recentCoachingSources(current, nil)).To(BeEmpty())
		prior.ProfileID = 2
		Expect(recentCoachingSources(current, []db.ChunkAnalysisResult{prior})).To(BeEmpty())
		prior = liveTestRow(3, 20, 30)
		prior.SessionID = "other"
		Expect(recentCoachingSources(current, []db.ChunkAnalysisResult{prior})).To(BeEmpty())
		prior = liveTestRow(3, 10, 20)
		sources := recentCoachingSources(current, []db.ChunkAnalysisResult{prior})
		Expect(sources).To(HaveLen(1))
		Expect(sources[0].GapAfterSecs).To(Equal(10.0))
		prior = liveTestRow(3, 20, 30)
		prior.ExerciseType = "Rest"
		Expect(recentCoachingSources(current, []db.ChunkAnalysisResult{prior})).To(BeEmpty())
		prior.ExerciseType = "Air Squat"
		prior.TargetConfidence = 0.5
		Expect(recentCoachingSources(current, []db.ChunkAnalysisResult{prior})).To(BeEmpty())
	})
	It("does not put recursive coaching in history or allow unknown source times", func() {
		current, prior := liveTestRow(2, 10, 20), liveTestRow(1, 0, 10)
		prior.ContextualCoaching = db.NullableJSONDocument(`{"text":{"ko":"recursive secret"}}`)
		raw, _ := json.Marshal(recentCoachingSources(current, []db.ChunkAnalysisResult{prior}))
		Expect(string(raw)).NotTo(ContainSubstring("recursive secret"))
		prior.StartSecs = nil
		Expect(recentCoachingSources(current, []db.ChunkAnalysisResult{prior})).To(BeEmpty())
	})
	It("requires both media and capture adjacency for review context", func() {
		a, b, c := liveTestRow(1, 0, 10), liveTestRow(2, 10, 20), liveTestRow(3, 20, 30)
		start, end, ok := reviewBounds([]db.ChunkAnalysisResult{a, b, c}, 1)
		Expect(ok).To(BeTrue())
		Expect(start).To(Equal(0.0))
		Expect(end).To(Equal(30.0))
		paused := 21.0
		c.StartSecs = &paused
		_, end, ok = reviewBounds([]db.ChunkAnalysisResult{a, b, c}, 1)
		Expect(ok).To(BeTrue())
		Expect(end).To(Equal(20.0))
	})
})

var _ = Describe("Live feedback worker integration", Ordered, ContinueOnFailure, func() {
	var conn *gorm.DB
	var w *Worker
	var transport, storageTransport *testhelpers.MockTransport
	var profile db.Profile
	var session db.Session
	var queue *asynq.Client
	const base = "https://generativelanguage.googleapis.com"
	BeforeAll(func() { var err error; conn, err = testhelpers.InitDB(); Expect(err).NotTo(HaveOccurred()) })
	AfterAll(func() {
		if conn != nil {
			sql, err := conn.DB()
			Expect(err).NotTo(HaveOccurred())
			Expect(sql.Close()).To(Succeed())
		}
	})
	BeforeEach(func() {
		testhelpers.CleanupDB(conn)
		profile = testhelpers.CreateProfile(conn, &db.Profile{})
		session = testhelpers.CreateSession(conn, &db.Session{SessionID: "WOD-20260916-01JACTIVITYTEST0000000000", ProfileID: profile.ID, WorkoutType: "wod"})
		transport = testhelpers.NewMockTransport()
		storageTransport = testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", BaseURL: base, HTTPClient: &http.Client{Transport: transport}, PollInterval: time.Millisecond, Sleep: func(time.Duration) {}})
		Expect(err).NotTo(HaveOccurred())
		storage, err := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(err).NotTo(HaveOccurred())
		queue = testhelpers.NewQueueClient()
		w = NewWorker(conn, storage, "test-bucket", client, queue, zap.NewNop())
		w.CaptureFeedbackEnabled = true
		w.ActivityCountingEnabled = true
		w.ContextualCoachingEnabled = true
	})
	AfterEach(func() { Expect(queue.Close()).To(Succeed()) })
	mockGeneration := func(reply string) {
		transport.New(base).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": reply}}}}}})
	}
	mockFile := func(duration string) {
		transport.New(base).Post("/upload/v1beta/files").Reply(200).Header("X-Goog-Upload-Url", base+"/upload-session").JSON(map[string]any{})
		transport.New(base).Post("/upload-session").Reply(200).Header("X-Goog-Upload-Status", "final").JSON(map[string]any{"file": map[string]any{"name": "files/activity-test", "uri": base + "/files/activity-test"}})
		transport.New(base).Get("/v1beta/files/activity-test").Reply(200).JSON(map[string]any{"name": "files/activity-test", "state": "ACTIVE", "videoMetadata": map[string]any{"videoDuration": duration}})
		transport.New(base).Delete("/v1beta/files/activity-test").Reply(200).JSON(map[string]any{})
	}
	createRow := func(start, end float64) db.ChunkAnalysisResult {
		row := liveTestRow(0, start, end)
		row.ID = 0
		row.ProfileID = profile.ID
		row.SessionID = session.SessionID
		row.FilePath = fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_%g.mp4", profile.ID, session.SessionID, start)
		return testhelpers.CreateChunkAnalysisResult(conn, &row)
	}
	DescribeTable("persists independent observations through the real chunk handler", func(sessionAware bool) {
		if !hasFfmpeg() {
			Skip("ffmpeg required")
		}
		filename := filepath.Join(GinkgoT().TempDir(), "sample.mp4")
		Expect(exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=32x32:r=2", "-t", "10", "-pix_fmt", "yuv420p", filename).Run()).To(Succeed())
		body, err := os.ReadFile(filename)
		Expect(err).NotTo(HaveOccurred())
		uri := fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_0.mp4", profile.ID, session.SessionID)
		testhelpers.MockGCSDownloadWithBody(storageTransport, uri, body)
		mockFile("10s")
		raw := "[NO_EXERCISE]\n```capture_assessment\n" + `{"state":"needs_adjustment","issue":"framing","evidence":"feet outside frame","advice":{"ko":"발끝까지 보이게 해주세요","en":"Include your feet"}}` + "\n```\n```movement_observations\n" + string(liveTestObservations(1, 3)) + "\n```"
		mockGeneration(raw)
		payload, _ := json.Marshal(VideoAnalysisPayload{SessionID: session.SessionID, ProfileID: profile.ID, FilePath: uri, StartSecs: 0, EndSecs: 10, LiveAnalysisVersion: 1})
		if sessionAware {
			Expect(w.HandleChunkAnalysisWithSessionTask(context.Background(), asynq.NewTask(TypeChunkAnalysisWithSession, payload))).To(Succeed())
		} else {
			Expect(w.HandleChunkAnalysisTask(context.Background(), asynq.NewTask(TypeChunkAnalysis, payload))).To(Succeed())
		}
		var saved db.ChunkAnalysisResult
		Expect(conn.Where("file_path = ?", uri).First(&saved).Error).To(Succeed())
		Expect(saved.Output).To(BeEmpty())
		Expect(string(saved.CaptureAssessment)).To(ContainSubstring("needs_adjustment"))
		Expect(saved.ContextualCoaching).To(BeEmpty())
		Expect(string(saved.MovementObservations)).To(ContainSubstring("conflicting_exercise_state"))
		// No second text request for [NO_EXERCISE], despite fabricated exercise metadata.
		Expect(transport.Verify()).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
	}, Entry("generic", false), Entry("session-aware", true))
	It("saves connected coaching without changing the independent observations", func() {
		prior := createRow(0, 10)
		current := createRow(10, 20)
		reply := fmt.Sprintf(`{"text":{"ko":"현재 코칭","en":"Current cue"},"current_chunk_id":%d,"source_chunk_ids":[%d]}`, current.ID, prior.ID)
		mockGeneration(reply)
		w.addContextualCoaching(context.Background(), 1, &current)
		var saved db.ChunkAnalysisResult
		Expect(conn.First(&saved, current.ID).Error).To(Succeed())
		Expect(saved.Output).To(Equal(current.Output))
		Expect(string(saved.MovementObservations)).To(MatchJSON(string(current.MovementObservations)))
		Expect(saved.ExerciseType).To(Equal(current.ExerciseType))
		Expect(string(saved.ContextualCoaching)).To(ContainSubstring("Current cue"))
		requests := transport.Requests()
		Expect(requests).To(HaveLen(1))
		Expect(string(requests[0].Body)).NotTo(ContainSubstring("fileData"))
		Expect(transport.Verify()).To(Succeed())
	})
	It("retains basic coaching when optional generation fails and keeps old records unopted", func() {
		createRow(0, 10)
		current := createRow(10, 20)
		transport.New(base).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(400).JSON(map[string]any{"error": map[string]any{"message": "bad response", "code": 400}})
		w.addContextualCoaching(context.Background(), 1, &current)
		var saved db.ChunkAnalysisResult
		Expect(conn.First(&saved, current.ID).Error).To(Succeed())
		Expect(saved.ContextualCoaching).To(BeEmpty())
		Expect(saved.Output).To(Equal(current.Output))
		old := testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED"})
		Expect(conn.First(&old, old.ID).Error).To(Succeed())
		Expect(old.MovementObservations).To(BeEmpty())
		Expect(old.CaptureAssessment).To(BeEmpty())
	})
	It("reviews overlapping video windows once per completion and resumes idempotently", func() {
		first, second := createRow(0, 10), createRow(10, 20)
		rows := []db.ChunkAnalysisResult{first, second}
		version := activity.SourceVersion(rows)
		object := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, session.SessionID)
		testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://test-bucket/"+object, []byte("video"))
		mockFile("20s")
		// The same cross-boundary repetition is visible in both review windows.
		mockGeneration("```movement_observations\n" + string(liveTestObservations(8, 12)) + "\n```")
		mockGeneration("```movement_observations\n" + string(liveTestObservations(8, 12)) + "\n```")
		payload, _ := json.Marshal(activityReviewPayload{session.SessionID, profile.ID, version, object, 1})
		task := asynq.NewTask(TypeActivityReview, payload)
		Expect(w.HandleActivityReviewTask(context.Background(), task)).To(Succeed())
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		var summary activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &summary)).To(Succeed())
		Expect(summary.Movements).To(HaveLen(1))
		Expect(summary.Movements[0].Count).To(Equal(1))
		Expect(summary.ReviewState).To(Equal("completed"))
		calls := len(transport.Requests())
		Expect(w.HandleActivityReviewTask(context.Background(), task)).To(Succeed())
		Expect(transport.Requests()).To(HaveLen(calls))
		Expect(transport.Verify()).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		for _, request := range transport.Requests() {
			if strings.Contains(request.URL, "generateContent") {
				Expect(string(request.Body)).To(ContainSubstring("videoMetadata"))
				Expect(string(request.Body)).NotTo(ContainSubstring("rep_count"))
			}
		}
	})
	It("rejects a stale review before reading video or changing newer results", func() {
		createRow(0, 10)
		payload, _ := json.Marshal(activityReviewPayload{session.SessionID, profile.ID, "outdated", fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, session.SessionID), 1})
		Expect(w.HandleActivityReviewTask(context.Background(), asynq.NewTask(TypeActivityReview, payload))).To(Succeed())
		Expect(storageTransport.Requests()).To(BeEmpty())
	})

	It("resumes a saved live chunk without re-running its video analysis", func() {
		row := createRow(0, 10)
		payload, err := json.Marshal(VideoAnalysisPayload{ProfileID: profile.ID, SessionID: session.SessionID, FilePath: row.FilePath, LiveAnalysisVersion: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleChunkAnalysisTask(context.Background(), asynq.NewTask(TypeChunkAnalysis, payload))).To(Succeed())
		Expect(transport.Requests()).To(BeEmpty())
		Expect(storageTransport.Requests()).To(BeEmpty())
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		var summary activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &summary)).To(Succeed())
		Expect(summary.Movements[0].Count).To(Equal(1))
	})

	It("records an all-failed opted-in recording as unassessed instead of zero or unavailable", func() {
		payload, _ := json.Marshal(VideoAnalysisPayload{ProfileID: profile.ID, SessionID: session.SessionID, FilePath: "gs://test-bucket/chunk_failed.mp4", StartSecs: 0, EndSecs: 10, LiveAnalysisVersion: 1})
		Expect(w.HandleChunkAnalysisTask(WithRetryCount(context.Background(), 3), asynq.NewTask(TypeChunkAnalysis, payload))).To(MatchError(asynq.SkipRetry))
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		var summary activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &summary)).To(Succeed())
		Expect(summary.Available).To(BeTrue())
		Expect(summary.Movements).To(BeEmpty())
		Expect(summary.Unassessed).NotTo(BeEmpty())
	})

	It("dispatches one durable independent review for repeated merge notifications", func() {
		createRow(0, 10)
		inspector := testhelpers.NewQueueInspector()
		defer inspector.Close()
		testhelpers.CleanupQueue(inspector)
		object := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, session.SessionID)
		for i := 0; i < 2; i++ {
			storageTransport.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + object).Reply(200).JSON(map[string]any{"name": object, "bucket": "test-bucket", "generation": "17"})
		}
		p := VideoAnalysisPayload{ProfileID: profile.ID, SessionID: session.SessionID}
		Expect(w.enqueueActivityReview(context.Background(), p, "gs://test-bucket/"+object)).To(Succeed())
		Expect(w.enqueueActivityReview(context.Background(), p, "gs://test-bucket/"+object)).To(Succeed())
		tasks, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(tasks).To(HaveLen(1))
		Expect(tasks[0].Type).To(Equal(TypeActivityReview))
		var queued activityReviewPayload
		Expect(json.Unmarshal(tasks[0].Payload, &queued)).To(Succeed())
		Expect(queued.Generation).To(Equal(int64(17)))
		// A duplicate merge must not relabel a terminal partial review as queued.
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		var partial activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &partial)).To(Succeed())
		partial.ReviewState = "partial"
		Expect(w.saveActivityReview(context.Background(), queued, &partial)).To(Succeed())
		storageTransport.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + object).Reply(200).JSON(map[string]any{"name": object, "bucket": "test-bucket", "generation": "17"})
		Expect(w.enqueueActivityReview(context.Background(), p, "gs://test-bucket/"+object)).To(Succeed())
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		Expect(json.Unmarshal(session.ActivitySummary, &partial)).To(Succeed())
		Expect(partial.ReviewState).To(Equal("partial"))
		Expect(storageTransport.Verify()).To(Succeed())
		testhelpers.CleanupQueue(inspector)
	})

	It("retains partial observations after a final review failure without failing full analysis", func() {
		createRow(0, 10)
		rows, err := w.activityRows(context.Background(), profile.ID, session.SessionID)
		Expect(err).NotTo(HaveOccurred())
		object := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, session.SessionID)
		testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://test-bucket/"+object, []byte("video"))
		mockFile("10s")
		transport.New(base).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(400).JSON(map[string]any{"error": map[string]any{"code": 400, "message": "failed review"}})
		payload, _ := json.Marshal(activityReviewPayload{session.SessionID, profile.ID, activity.SourceVersion(rows), object, 1})
		Expect(w.HandleActivityReviewTask(WithRetryCount(context.Background(), 2), asynq.NewTask(TypeActivityReview, payload))).To(Succeed())
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		var summary activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &summary)).To(Succeed())
		Expect(summary.ReviewState).To(Equal("partial"))
		Expect(summary.Movements[0].Count).To(Equal(1))
		Expect(summary.Unassessed).NotTo(BeEmpty())
		Expect(transport.Verify()).To(Succeed())
	})
})
