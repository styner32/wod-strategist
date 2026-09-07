package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

var _ = Describe("HandleSessionDebugReanalysisTask", func() {
	const (
		baseURL = "https://session-reanalysis.test"
		apiKey  = "test-api-key"
	)

	var (
		database         *gorm.DB
		storageTransport *testhelpers.MockTransport
		geminiTransport  *testhelpers.MockTransport
		inspector        *asynq.Inspector
		w                *Worker
		profile          db.Profile
		session          db.Session
		sourceURI        string
		videoBytes       []byte
	)

	expectAnalysis := func(fileURI string) {
		analysisText := `교정 라벨을 영상에서 다시 확인했습니다.
` + "```highlights\n" + `[{"start":"0:00","end":"0:00.5","type":"key_moment","movement":"Pull-up","reason":"visible movement"}]` + "\n```\n" +
			"```score\n" + `{"overall":80,"form":80,"intensity":80,"consistency":80,"movements":{"Pull-up":{"form":80,"intensity":80}},"summary":"good"}` + "\n```"
		geminiTransport.New(baseURL).
			Post("/v1beta/models/"+gemini.ModelPro31Preview+":generateContent").
			MatchHeader("X-Goog-Api-Key", apiKey).
			MatchBodyContains(`"fileUri":"` + fileURI + `"`).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{"parts": []map[string]any{{"text": analysisText}}},
				}},
				"usageMetadata": map[string]any{"promptTokenCount": 17, "candidatesTokenCount": 9, "totalTokenCount": 26},
			})
	}

	seedChunkAndRun := func() (db.ChunkAnalysisResult, db.SessionReanalysisRun) {
		start, end := 0.0, 0.75
		chunk := testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			ExerciseType: "Rope Climb", Output: "immutable chunk output",
			ObservedSignals: `{"movement":"Rope Climb","activity_state":"exercise","fatigue_visually_established":true,"fatigue_evidence_types":["rep_slowdown"],"fatigue_evidence":["original-fatigue-marker"]}`,
			MediaStartSecs:  &start, MediaEndSecs: &end,
		})
		run := testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusQueued,
			SourceGCSURI: sourceURI, OriginalAnalysisSnapshot: db.JSONDocument(`{"output":"immutable final"}`),
		})
		return chunk, run
	}

	BeforeEach(func() {
		if !hasFfmpeg() {
			Skip("ffmpeg/ffprobe are required for exact source duration tests")
		}
		var err error
		database, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(database)
		storageTransport = testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(err).NotTo(HaveOccurred())
		geminiTransport = testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey: apiKey, BaseURL: baseURL, HTTPClient: &http.Client{Transport: geminiTransport},
			PollInterval: time.Millisecond, Sleep: func(time.Duration) {},
		})
		Expect(err).NotTo(HaveOccurred())
		queue := testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		testhelpers.CleanupQueue(inspector)
		w = NewWorker(database, storageClient, "test-bucket", client, queue, zap.NewNop())
		profile = testhelpers.CreateProfile(database, &db.Profile{})
		session = testhelpers.CreateSession(database, &db.Session{
			SessionID: "WOD-20260716-01JSESSIONDEBUGWORKER000", ProfileID: profile.ID,
			WorkoutType: "wod", WODDescription: "Pull-ups",
		})
		sourceURI = fmt.Sprintf("gs://test-bucket/videos/%d/%s/analysis.mp4", profile.ID, session.SessionID)
		path := createTinyMP4(GinkgoT())
		videoBytes, err = os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty(), "session re-analysis must not enqueue derived output")
	})

	It("reuses an exact-source Files upload, applies only saved structured corrections, and keeps production rows immutable", func() {
		chunk, run := seedChunkAndRun()
		original := testhelpers.CreateAnalysisResult(database, &db.AnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			Output: "immutable final output", HighlightSegments: `[{"start":"0:00","end":"0:01"}]`,
		})
		movementFeedback := testhelpers.CreateAnalysisFeedback(database, &db.AnalysisFeedback{
			ProfileID: profile.ID, SessionID: session.SessionID, TargetType: db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &chunk.ID, Category: db.FeedbackCategoryMovement,
			Correction: db.JSONDocument(`{"movement_name":"Pull-up"}`), Note: "PRIVATE FREE TEXT NOTE",
		})
		testhelpers.CreateAnalysisFeedback(database, &db.AnalysisFeedback{
			ProfileID: profile.ID, SessionID: session.SessionID, TargetType: db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &chunk.ID, Category: db.FeedbackCategoryFatigue,
			Correction: db.JSONDocument(`{"fatigue_state":"not_fatigued"}`),
		})
		unmappedChunk := testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			ExerciseType: "Unknown", ObservedSignals: `{"movement":"Unknown","activity_state":"unknown"}`,
		})
		testhelpers.CreateAnalysisFeedback(database, &db.AnalysisFeedback{
			ProfileID: profile.ID, SessionID: session.SessionID, TargetType: db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &unmappedChunk.ID, Category: db.FeedbackCategoryMovement,
			Correction: db.JSONDocument(`{"movement_name":"Unmapped Legacy Carry"}`),
		})
		expires := time.Now().UTC().Add(time.Hour)
		testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusCompleted,
			SourceGCSURI: sourceURI, GeminiFileURI: baseURL + "/files/cached-session",
			GeminiFileName: "files/cached-session", GeminiMIMEType: "video/mp4", GeminiFileExpiresAt: &expires,
		})
		testhelpers.MockGCSDownloadWithBody(storageTransport, sourceURI, videoBytes)
		geminiTransport.New(baseURL).Get("/v1beta/files/cached-session").MatchHeader("X-Goog-Api-Key", apiKey).
			Reply(http.StatusOK).JSON(map[string]any{"name": "files/cached-session", "state": "ACTIVE"})
		expectAnalysis(baseURL + "/files/cached-session")

		task, err := NewSessionDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleSessionDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		var prompt string
		for _, request := range geminiTransport.Requests() {
			if strings.Contains(request.URL, gemini.ModelPro31Preview+":generateContent") {
				prompt = string(request.Body)
			}
		}
		Expect(prompt).To(ContainSubstring("Pull-up"))
		Expect(prompt).To(ContainSubstring("not_fatigued"))
		Expect(prompt).To(ContainSubstring("media_start_secs"))
		Expect(prompt).NotTo(ContainSubstring("PRIVATE FREE TEXT NOTE"))
		Expect(prompt).NotTo(ContainSubstring("original-fatigue-marker"))
		Expect(prompt).NotTo(ContainSubstring("Unmapped Legacy Carry"))
		Expect(movementFeedback.ID).NotTo(BeZero())

		var completed db.SessionReanalysisRun
		Expect(database.First(&completed, run.ID).Error).To(Succeed())
		Expect(completed.Status).To(Equal(db.SessionReanalysisStatusCompleted))
		Expect(completed.Output).To(ContainSubstring("교정 라벨"))
		Expect(completed.HighlightSegments).NotTo(BeEmpty())
		Expect(completed.SessionScore).To(MatchJSON(`{"overall":80,"form":80,"intensity":80,"consistency":80,"movements":{"Pull-up":{"form":80,"intensity":80}},"summary":"good"}`))
		Expect(completed.PromptTokens).To(Equal(int32(17)))

		var persistedChunk db.ChunkAnalysisResult
		Expect(database.First(&persistedChunk, chunk.ID).Error).To(Succeed())
		Expect(persistedChunk.ExerciseType).To(Equal("Rope Climb"))
		Expect(persistedChunk.Output).To(Equal("immutable chunk output"))
		var persistedOriginal db.AnalysisResult
		Expect(database.First(&persistedOriginal, original.ID).Error).To(Succeed())
		Expect(persistedOriginal.Output).To(Equal("immutable final output"))
		var analysisCount int64
		Expect(database.Model(&db.AnalysisResult{}).Count(&analysisCount).Error).To(Succeed())
		Expect(analysisCount).To(Equal(int64(1)))
	})

	It("uploads the authoritative GCS video when no exact-source cache is reusable", func() {
		_, run := seedChunkAndRun()
		testhelpers.MockGCSDownloadWithBody(storageTransport, sourceURI, videoBytes)
		geminiTransport.New(baseURL).Post("/upload/v1beta/files").MatchHeader("X-Goog-Api-Key", apiKey).
			Reply(http.StatusOK).Header("X-Goog-Upload-Url", baseURL+"/upload-session").JSON(map[string]any{})
		geminiTransport.New(baseURL).Post("/upload-session").MatchHeader("X-Goog-Api-Key", apiKey).
			Reply(http.StatusOK).Header("X-Goog-Upload-Status", "final").JSON(map[string]any{"file": map[string]any{
			"name": "files/uploaded-session", "uri": baseURL + "/files/uploaded-session",
		}})
		geminiTransport.New(baseURL).Get("/v1beta/files/uploaded-session").MatchHeader("X-Goog-Api-Key", apiKey).
			Reply(http.StatusOK).JSON(map[string]any{
			"name": "files/uploaded-session", "state": "ACTIVE", "videoMetadata": map[string]any{"videoDuration": "1s"},
		})
		expectAnalysis(baseURL + "/files/uploaded-session")

		task, err := NewSessionDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleSessionDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())
		var completed db.SessionReanalysisRun
		Expect(database.First(&completed, run.ID).Error).To(Succeed())
		Expect(completed.Status).To(Equal(db.SessionReanalysisStatusCompleted))
		Expect(completed.GeminiFileURI).To(Equal(baseURL + "/files/uploaded-session"))
	})

	It("retries instead of publishing a partial candidate when any selected segment fails", func() {
		_, run := seedChunkAndRun()
		secondStart, secondEnd := 0.76, 0.95
		testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			ExerciseType: "Burpee", ObservedSignals: `{"movement":"Burpee","activity_state":"exercise"}`,
			MediaStartSecs: &secondStart, MediaEndSecs: &secondEnd,
		})
		expires := time.Now().UTC().Add(time.Hour)
		testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusCompleted,
			SourceGCSURI: sourceURI, GeminiFileURI: baseURL + "/files/partial-cache",
			GeminiFileName: "files/partial-cache", GeminiMIMEType: "video/mp4", GeminiFileExpiresAt: &expires,
		})
		testhelpers.MockGCSDownloadWithBody(storageTransport, sourceURI, videoBytes)
		geminiTransport.New(baseURL).Get("/v1beta/files/partial-cache").MatchHeader("X-Goog-Api-Key", apiKey).
			Reply(http.StatusOK).JSON(map[string]any{"name": "files/partial-cache", "state": "ACTIVE"})
		expectAnalysis(baseURL + "/files/partial-cache")
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelPro31Preview+":generateContent").
			MatchHeader("X-Goog-Api-Key", apiKey).MatchBodyContains("Burpee").
			Reply(http.StatusInternalServerError).JSON(map[string]any{"error": map[string]any{"message": "temporary"}})

		task, err := NewSessionDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleSessionDebugReanalysisTask(context.Background(), task)).To(HaveOccurred())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())
		var persisted db.SessionReanalysisRun
		Expect(database.First(&persisted, run.ID).Error).To(Succeed())
		Expect(persisted.Status).To(Equal(db.SessionReanalysisStatusQueued))
		Expect(persisted.Output).To(BeEmpty())
		Expect(persisted.CompletedAt).To(BeNil())
	})

	It("records terminal source failures and resets transient failures for retry", func() {
		run := testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusQueued,
			SourceGCSURI: "gs://another-bucket/private.mp4",
		})
		task, err := NewSessionDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleSessionDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.Status).To(Equal(db.SessionReanalysisStatusVideoUnavailable))

		retryRun := testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusRunning,
			SourceGCSURI: sourceURI,
		})
		cause := errors.New("temporary analyzer failure")
		Expect(w.failSessionDebugRun(context.Background(), retryRun.ID, 0, time.Now(), "safe", cause)).To(MatchError(cause))
		Expect(database.First(&retryRun, retryRun.ID).Error).To(Succeed())
		Expect(retryRun.Status).To(Equal(db.SessionReanalysisStatusQueued))
		terminalErr := w.failSessionDebugRun(context.Background(), retryRun.ID, sessionDebugMaxRetries, time.Now(), "safe", cause)
		Expect(terminalErr).To(HaveOccurred())
		Expect(database.First(&retryRun, retryRun.ID).Error).To(Succeed())
		Expect(retryRun.Status).To(Equal(db.SessionReanalysisStatusFailed))
		Expect(retryRun.SafeError).To(Equal("safe"))
	})

	It("retains Unknown as an exercise interval for deeper visual revalidation", func() {
		start, end := 0.0, 0.75
		chunk := testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			ExerciseType: "Walking", ObservedSignals: `{"movement":"Walking","activity_state":"walking"}`,
			MediaStartSecs: &start, MediaEndSecs: &end,
		})

		segments, err := w.buildSessionDebugSegments(context.Background(), session.SessionID, profile.ID, []sessionDebugCorrection{{
			ChunkID: chunk.ID, MovementName: "Unknown",
		}}, WorkoutTypeWOD)
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Type).To(Equal("Unknown"))
	})
})

var _ = Describe("session debug re-analysis helpers", func() {
	It("serializes only a run ID and recognizes terminal states", func() {
		task, err := NewSessionDebugReanalysisTask(42)
		Expect(err).NotTo(HaveOccurred())
		Expect(task.Type()).To(Equal(TypeSessionDebugReanalysis))
		var payload map[string]any
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload).To(Equal(map[string]any{"run_id": float64(42)}))
		_, err = NewSessionDebugReanalysisTask(0)
		Expect(err).To(MatchError("run_id is required"))
		for _, status := range []string{db.SessionReanalysisStatusCompleted, db.SessionReanalysisStatusFailed,
			db.SessionReanalysisStatusVideoUnavailable, db.SessionReanalysisStatusContextUnavailable} {
			Expect(isSessionReanalysisTerminal(status)).To(BeTrue())
		}
		Expect(isSessionReanalysisTerminal(db.SessionReanalysisStatusRunning)).To(BeFalse())
	})
})
