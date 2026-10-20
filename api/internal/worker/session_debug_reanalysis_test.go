package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/cost"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/workoutprogress"
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

	expectAnalysis := func(fileURI string, finalCalls ...bool) {
		geminiTransport.New(baseURL).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").
			MatchBodyContains("Locate the earliest actual WOD exercise onset").
			MatchBodyContains(`"fileUri":"` + fileURI + `"`).
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": `{"seconds":0,"target_confirmed":true,"preceding_activity":"rest_setup","evidence":"immediate start"}`}}},
			}},
		})
		analysisText := `교정 라벨을 영상에서 다시 확인했습니다.
` + "```highlights\n" + `[{"start":"0:00","end":"0:00.5","type":"key_moment","movement":"Pull-up","reason":"visible movement"}]` + "\n```\n" +
			"```wod_evidence\n" + `{"target_confirmed":true,"movement":"Pull-up","start_secs":0,"end_secs":0.75,"form_observations":["visible aligned pull"],"pace_observations":["steady"]}` + "\n```\n" +
			"```workout_observations\n" + `{"intervals":[{"start_secs":0,"end_secs":0.75,"activity":"exercise","movement":"Pull-up","target_confirmed":true,"rep_events":[{"movement":"Pull-up","unit":"reps","start_secs":0.1,"end_secs":0.5,"complete":true,"evidence":"complete pull and return"}],"form_observations":[{"start_secs":0.1,"end_secs":0.5,"evidence":"visible aligned pull"}],"rom_observations":[],"pace_observations":[]}],"unassessed":[]}` + "\n```"
		geminiTransport.New(baseURL).
			Post("/v1beta/models/"+gemini.ModelArgon4+":generateContent").
			MatchHeader("X-Goog-Api-Key", apiKey).
			MatchBodyContains(`"inlineData"`).
			MatchBodyContains("프레임 그리드(Contact Sheet) 입력 안내").
			MatchBodyContains("00:00:00.000").
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{"parts": []map[string]any{{"text": analysisText}}},
				}},
				"usageMetadata": map[string]any{"promptTokenCount": 17, "candidatesTokenCount": 9, "totalTokenCount": 26},
			})
		if len(finalCalls) > 0 && !finalCalls[0] {
			return
		}
		geminiTransport.New(baseURL).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").
			MatchBodyContains("weight_raw").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "```movements\n{\"movements\":[]}\n```"}}}}}})
		geminiTransport.New(baseURL).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").
			MatchBodyContains("Evaluate this WOD using ONLY the supplied validated observations").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": `{"form":80,"intensity":80,"consistency":80,"movements":{"Pull-up":{"form":80,"intensity":80}},"summary":"good"}`}}}}}})
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
		if errors.Is(err, asynq.ErrQueueNotFound) {
			return
		}
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
			if strings.Contains(request.URL, gemini.ModelArgon4+":generateContent") {
				prompt = string(request.Body)
			}
		}
		Expect(prompt).To(ContainSubstring("Pull-up"))
		Expect(prompt).NotTo(ContainSubstring("not_fatigued"))
		assessmentPrompt := ""
		for _, request := range geminiTransport.Requests() {
			if strings.Contains(string(request.Body), "Evaluate this WOD using ONLY the supplied validated observations") {
				assessmentPrompt = string(request.Body)
			}
		}
		Expect(assessmentPrompt).To(ContainSubstring("not_fatigued"))
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
		// Legacy WOD reanalysis produces a Version-1 WOD candidate score; footage after the last observed window is outside the assessed span.
		var parsedScore struct {
			EvaluationVersion int    `json:"evaluation_version"`
			EvidenceStatus    string `json:"evidence_status"`
			Form              *int   `json:"form"`
			Intensity         *int   `json:"intensity"`
			Consistency       *int   `json:"consistency"`
		}
		Expect(json.Unmarshal([]byte(completed.SessionScore), &parsedScore)).To(Succeed())
		Expect(parsedScore.EvaluationVersion).To(Equal(1))
		Expect(parsedScore.EvidenceStatus).To(Equal("complete"))
		Expect(parsedScore.Form).NotTo(BeNil())
		Expect(*parsedScore.Form).To(Equal(80))
		Expect(parsedScore.Intensity).NotTo(BeNil())
		Expect(*parsedScore.Intensity).To(Equal(80))
		Expect(parsedScore.Consistency).To(BeNil())
		Expect(completed.WorkoutProgress).NotTo(BeEmpty())
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

	It("keeps truncated Argon output out of candidates while recording billed usage", func() {
		_, run := seedChunkAndRun()
		Expect(database.Model(&db.SessionReanalysisRun{}).Where("id = ?", run.ID).Update("model", "gemini-4-argon").Error).To(Succeed())
		expires := time.Now().UTC().Add(time.Hour)
		testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusCompleted,
			SourceGCSURI: sourceURI, GeminiFileURI: baseURL + "/files/truncated-cache",
			GeminiFileName: "files/truncated-cache", GeminiMIMEType: "video/mp4", GeminiFileExpiresAt: &expires,
		})
		testhelpers.MockGCSDownloadWithBody(storageTransport, sourceURI, videoBytes)
		geminiTransport.New(baseURL).Get("/v1beta/files/truncated-cache").Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/truncated-cache", "state": "ACTIVE"})
		geminiTransport.New(baseURL).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").
			MatchBodyContains("Locate the earliest actual WOD exercise onset").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": `{"seconds":0,"target_confirmed":true,"preceding_activity":"rest_setup","evidence":"immediate start"}`}}},
			}},
		})
		geminiTransport.New(baseURL).Post("/v1beta/models/" + gemini.ModelArgon4 + ":generateContent").Reply(http.StatusOK).
			BodyString(`{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"partially generated observation"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":32748,"totalTokenCount":32868}}`)
		task, err := NewSessionDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleSessionDebugReanalysisTask(context.Background(), task)).To(MatchError(ContainSubstring("MAX_TOKENS")))
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())
		var persisted db.SessionReanalysisRun
		Expect(database.First(&persisted, run.ID).Error).To(Succeed())
		Expect(persisted.Status).To(Equal(db.SessionReanalysisStatusQueued))
		Expect(persisted.Output).To(BeEmpty())
		Expect(persisted.CompletedAt).To(BeNil())
		var usage db.TokenUsage
		Expect(database.Where("session_id = ? AND task_type = ?", session.SessionID, "session:reanalysis").First(&usage).Error).To(Succeed())
		Expect(usage.Model).To(Equal(gemini.ModelArgon4))
		Expect(usage.TotalTokens).To(Equal(int64(32868)))
		var metadata map[string]int64
		Expect(json.Unmarshal(usage.UsageMetadata, &metadata)).To(Succeed())
		Expect(metadata["thoughtsTokenCount"]).To(Equal(int64(32748)))
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
		expectAnalysis(baseURL+"/files/partial-cache", false)
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelArgon4+":generateContent").
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

		segments, _, _, _, err := w.buildSessionDebugSegments(context.Background(), session.SessionID, profile.ID, []sessionDebugCorrection{{
			ChunkID: chunk.ID, MovementName: "Unknown",
		}}, WorkoutTypeWOD)
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Type).To(Equal("Unknown"))
	})

	It("persists distinct token_usages rows across all WOD re-analysis steps and aggregates normalize-workout into run counters", func() {
		Expect(database.Model(&db.Session{}).Where("id = ?", session.ID).Update("wod_evaluation_version", 1).Error).To(Succeed())
		_, run := seedChunkAndRun()
		expires := time.Now().UTC().Add(time.Hour)
		testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusCompleted,
			SourceGCSURI: sourceURI, GeminiFileURI: baseURL + "/files/wod-usage-cache",
			GeminiFileName: "files/wod-usage-cache", GeminiMIMEType: "video/mp4", GeminiFileExpiresAt: &expires,
		})
		testhelpers.MockGCSDownloadWithBody(storageTransport, sourceURI, videoBytes)
		geminiTransport.New(baseURL).Get("/v1beta/files/wod-usage-cache").MatchHeader("X-Goog-Api-Key", apiKey).
			Reply(http.StatusOK).JSON(map[string]any{"name": "files/wod-usage-cache", "state": "ACTIVE"})

		// 1. video:wod-start (QueryVideoFlash)
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelFlash38+":generateContent").
			MatchBodyContains("Locate the earliest actual WOD exercise onset").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": `{"seconds":0,"target_confirmed":true,"preceding_activity":"rest_setup","evidence":"immediate start"}`}}},
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
		})

		// 2. session:reanalysis (AnalyzeVideoIntervalWithFrameGrids)
		analysisText := "교정 라벨을 영상에서 다시 확인했습니다.\n" +
			"```highlights\n" + `[{"start":"0:00","end":"0:00.5","type":"key_moment","movement":"Pull-up","reason":"visible movement"}]` + "\n```\n" +
			"```wod_evidence\n" + `{"target_confirmed":true,"movement":"Pull-up","start_secs":0,"end_secs":0.75,"form_observations":["aligned pull"],"pace_observations":["steady"]}` + "\n```\n" +
			"```workout_observations\n" + `{"intervals":[{"start_secs":0,"end_secs":0.75,"activity":"exercise","movement":"Pull-up","target_confirmed":true,"rep_events":[{"movement":"Pull-up","unit":"reps","start_secs":0.1,"end_secs":0.5,"complete":true,"evidence":"complete pull"}],"form_observations":[{"start_secs":0.1,"end_secs":0.5,"evidence":"aligned pull"}],"rom_observations":[],"pace_observations":[]}],"unassessed":[]}` + "\n```"
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelArgon4+":generateContent").
			MatchHeader("X-Goog-Api-Key", apiKey).
			MatchBodyContains(`"inlineData"`).
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": analysisText}}},
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 20, "candidatesTokenCount": 10, "totalTokenCount": 30},
		})

		// 3. session:normalize-workout (ParseText)
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelFlash38+":generateContent").
			MatchBodyContains("weight_raw").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": "```movements\n{\"movements\":[]}\n```"}}},
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 30, "candidatesTokenCount": 15, "totalTokenCount": 45},
		})

		// 4. video:wod-synthesis (ParseText)
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelFlash38+":generateContent").
			MatchBodyContains("Evaluate this WOD using ONLY the supplied validated observations").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": `{"form":85,"intensity":80,"consistency":null,"movements":{"Pull-up":{"form":85,"intensity":80}},"summary":"좋습니다."}`}}},
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 40, "candidatesTokenCount": 20, "totalTokenCount": 60},
		})

		task, err := NewSessionDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleSessionDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		var completed db.SessionReanalysisRun
		Expect(database.First(&completed, run.ID).Error).To(Succeed())
		Expect(completed.Status).To(Equal(db.SessionReanalysisStatusCompleted))
		Expect(completed.PromptTokens).To(Equal(int32(10 + 20 + 30 + 40)))
		Expect(completed.CandidateTokens).To(Equal(int32(5 + 10 + 15 + 20)))
		Expect(completed.TotalTokens).To(Equal(int32(15 + 30 + 45 + 60)))

		var usages []db.TokenUsage
		Expect(database.Where("session_id = ?", session.SessionID).Order("id ASC").Find(&usages).Error).To(Succeed())
		Expect(usages).To(HaveLen(4))
		Expect([]string{usages[0].TaskType, usages[1].TaskType, usages[2].TaskType, usages[3].TaskType}).To(Equal([]string{
			"video:wod-start",
			"session:reanalysis",
			"session:normalize-workout",
			"video:wod-synthesis",
		}))

		summary := cost.CalculateSessionCost(session.SessionID, usages)
		Expect(summary.ByTaskType).To(HaveLen(4))
		Expect(summary.PromptTokens).To(Equal(int64(100)))
		Expect(summary.CandidateTokens).To(Equal(int64(50)))
		Expect(summary.TotalTokens).To(Equal(int64(150)))
	})

	It("reuses unchanged segment outputs from a previous COMPLETED SessionReanalysisRun without re-calling Gemini for those segments", func() {
		firstChunk, firstRun := seedChunkAndRun()
		secondStart, secondEnd := 0.76, 0.95
		secondChunk := testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			ExerciseType: "Burpee", ObservedSignals: `{"movement":"Burpee","activity_state":"exercise"}`,
			MediaStartSecs: &secondStart, MediaEndSecs: &secondEnd,
		})
		expires := time.Now().UTC().Add(time.Hour)
		testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusCompleted,
			SourceGCSURI: sourceURI, GeminiFileURI: baseURL + "/files/segment-reuse-cache",
			GeminiFileName: "files/segment-reuse-cache", GeminiMIMEType: "video/mp4", GeminiFileExpiresAt: &expires,
		})
		testhelpers.MockGCSDownloadWithBody(storageTransport, sourceURI, videoBytes)
		geminiTransport.New(baseURL).Get("/v1beta/files/segment-reuse-cache").MatchHeader("X-Goog-Api-Key", apiKey).
			Reply(http.StatusOK).JSON(map[string]any{"name": "files/segment-reuse-cache", "state": "ACTIVE"})

		burpeeOutput := "버피 동작이 확인됩니다.\n" +
			"```highlights\n" + `[{"start":"0:00.8","end":"0:00.9","type":"key_moment","movement":"Burpee","reason":"burpee rep"}]` + "\n```\n" +
			"```workout_observations\n" + `{"intervals":[{"start_secs":0.76,"end_secs":0.95,"activity":"exercise","movement":"Burpee","target_confirmed":true,"rep_events":[{"movement":"Burpee","unit":"reps","start_secs":0.8,"end_secs":0.9,"complete":true,"evidence":"burpee"}],"form_observations":[{"start_secs":0.8,"end_secs":0.9,"evidence":"steady burpee"}],"rom_observations":[],"pace_observations":[]}],"unassessed":[]}` + "\n```"
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelArgon4+":generateContent").
			MatchHeader("X-Goog-Api-Key", apiKey).
			MatchBodyContains("Burpee").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": burpeeOutput}}},
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 14, "candidatesTokenCount": 6, "totalTokenCount": 20},
		})
		expectAnalysis(baseURL+"/files/segment-reuse-cache", false)
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelFlash38+":generateContent").
			MatchBodyContains("weight_raw").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": "```movements\n{\"movements\":[]}\n```"}}},
			}},
		})
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelFlash38+":generateContent").
			MatchBodyContains("Evaluate this WOD using ONLY the supplied validated observations").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": `{"form":80,"intensity":80,"consistency":80,"movements":{"Pull-up":{"form":80,"intensity":80},"Burpee":{"form":80,"intensity":80}},"summary":"good"}`}}},
			}},
		})

		firstTask, err := NewSessionDebugReanalysisTask(firstRun.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleSessionDebugReanalysisTask(context.Background(), firstTask)).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		// Second run: same firstChunk (0.0-0.75), plus a new correction on secondChunk (0.76-0.95 -> Box Jump).
		// Only the second segment (0.76-0.95) should invoke Gemini ModelArgon4; the first segment (0.0-0.75) should be reused!
		_ = firstChunk
		testhelpers.CreateAnalysisFeedback(database, &db.AnalysisFeedback{
			ProfileID:             profile.ID,
			SessionID:             session.SessionID,
			TargetType:            db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &secondChunk.ID,
			Category:              db.FeedbackCategoryMovement,
			Correction:            db.JSONDocument(`{"movement_name":"Box Jump"}`),
		})
		secondRun := testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusQueued,
			SourceGCSURI: sourceURI,
		})
		testhelpers.MockGCSDownloadWithBody(storageTransport, sourceURI, videoBytes)
		geminiTransport.New(baseURL).Get("/v1beta/files/segment-reuse-cache").MatchHeader("X-Goog-Api-Key", apiKey).
			Reply(http.StatusOK).JSON(map[string]any{"name": "files/segment-reuse-cache", "state": "ACTIVE"})

		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelFlash38+":generateContent").
			MatchHeader("X-Goog-Api-Key", apiKey).
			MatchBodyContains("Locate the earliest actual WOD exercise onset").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": `{"seconds":0,"target_confirmed":true,"preceding_activity":"rest_setup","evidence":"immediate start"}`}}},
			}},
		})
		boxJumpOutput := "박스 점프가 확인됩니다.\n" +
			"```highlights\n" + `[{"start":"0:00.8","end":"0:00.9","type":"key_moment","movement":"Box Jump","reason":"box jump rep"}]` + "\n```\n" +
			"```workout_observations\n" + `{"intervals":[{"start_secs":0.76,"end_secs":0.95,"activity":"exercise","movement":"Box Jump","target_confirmed":true,"rep_events":[{"movement":"Box Jump","unit":"reps","start_secs":0.8,"end_secs":0.9,"complete":true,"evidence":"box jump"}],"form_observations":[{"start_secs":0.8,"end_secs":0.9,"evidence":"soft landing"}],"rom_observations":[],"pace_observations":[]}],"unassessed":[]}` + "\n```"
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelArgon4+":generateContent").
			MatchHeader("X-Goog-Api-Key", apiKey).
			MatchBodyContains("Box Jump").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": boxJumpOutput}}},
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 14, "candidatesTokenCount": 6, "totalTokenCount": 20},
		})
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelFlash38+":generateContent").
			MatchBodyContains("weight_raw").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": "```movements\n{\"movements\":[]}\n```"}}},
			}},
		})
		geminiTransport.New(baseURL).Post("/v1beta/models/"+gemini.ModelFlash38+":generateContent").
			MatchHeader("X-Goog-Api-Key", apiKey).
			MatchBodyContains("Evaluate this WOD using ONLY the supplied validated observations").
			Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": `{"form":84,"intensity":80,"consistency":81,"movements":{"Pull-up":{"form":84,"intensity":80},"Box Jump":{"form":84,"intensity":80}},"summary":"쓰러스터와 박스 점프를 수행했습니다."}`}}},
			}},
		})

		secondTask, err := NewSessionDebugReanalysisTask(secondRun.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleSessionDebugReanalysisTask(context.Background(), secondTask)).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		var completed db.SessionReanalysisRun
		Expect(database.First(&completed, secondRun.ID).Error).To(Succeed())
		Expect(completed.Status).To(Equal(db.SessionReanalysisStatusCompleted))
		Expect(completed.Output).To(ContainSubstring("교정 라벨을 영상에서 다시 확인했습니다."))
		Expect(completed.Output).To(ContainSubstring("박스 점프가 확인됩니다."))
	})

	It("leaves legacy chunks unmapped when a retained object is missing and never reuses stored display headers", func() {
		testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			FilePath:     "gs://test-bucket/videos/missing-chunk-001.mp4",
			ExerciseType: "Thruster", ObservedSignals: `{"movement":"Thruster","activity_state":"exercise"}`,
		})
		// Legacy headers can carry capture-clock offsets that do not exist in merged media.
		testhelpers.CreateAnalysisResult(database, &db.AnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			Output: "## 세그먼트 1: Thruster (3:10 ~ 4:00)\n이전 쓰러스터 분석입니다.",
		})

		segments, skipped, timelineAvailable, _, err := w.buildSessionDebugSegments(context.Background(), session.SessionID, profile.ID, nil, WorkoutTypeWOD)
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(BeEmpty())
		Expect(skipped).To(BeEmpty())
		Expect(timelineAvailable).To(BeFalse())
		selected, _ := w.storedProgressSegments(context.Background(), session.SessionID, profile.ID, WorkoutTypeWOD, reusableGeminiFile{URI: baseURL + "/files/any"})
		Expect(selected).To(BeEmpty())
	})

	It("skips a legacy timeline rebuild that recently failed for the same chunk rows", func() {
		testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED",
			FilePath:     "gs://test-bucket/videos/missing-chunk-001.mp4",
			ExerciseType: "Thruster", ObservedSignals: `{"movement":"Thruster","activity_state":"exercise"}`,
		})

		_, _, _, failed, err := w.buildSessionDebugSegments(context.Background(), session.SessionID, profile.ID, nil, WorkoutTypeWOD)
		Expect(err).NotTo(HaveOccurred())
		Expect(failed).NotTo(BeEmpty())
		Expect(storageTransport.Requests()).NotTo(BeEmpty())

		testhelpers.CreateSessionReanalysisRun(database, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: db.SessionReanalysisStatusCompleted,
			SourceGCSURI: sourceURI, OriginalAnalysisSnapshot: db.JSONDocument(`{}`),
			SourceContextSnapshot: db.JSONDocument(fmt.Sprintf(`{"timeline_rebuild_failed":%q}`, failed)),
		})
		storageTransport.Reset()

		_, _, timelineAvailable, again, err := w.buildSessionDebugSegments(context.Background(), session.SessionID, profile.ID, nil, WorkoutTypeWOD)
		Expect(err).NotTo(HaveOccurred())
		Expect(timelineAvailable).To(BeFalse())
		Expect(again).To(BeEmpty(), "a skipped rebuild does not extend the retry window")
		Expect(storageTransport.Requests()).To(BeEmpty(), "no retained chunk is downloaded again")
	})

	It("reads the workout type of a legacy session without a sessions row from its ID", func() {
		legacyID := fmt.Sprintf("P%d-WARMUP-2025-03-01-08-00", profile.ID)
		testhelpers.CreateAnalysisResult(database, &db.AnalysisResult{
			SessionID: legacyID, ProfileID: profile.ID, Status: "COMPLETED", WODDescription: "Mobility flow",
		})

		target, err := w.loadSessionDebugContext(context.Background(), legacyID, profile.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(target.WorkoutType).To(Equal(WorkoutTypeWarmup))
		Expect(target.WODDescription).To(Equal("Mobility flow"))
	})

	It("writes no rebuilt offsets when any retained chunk of the legacy timeline is unavailable", func() {
		first, second := 0.0, 10.0
		available := testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", StartSecs: &first,
			FilePath:     fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_000.mp4", profile.ID, session.SessionID),
			ExerciseType: "Thruster", ObservedSignals: `{"movement":"Thruster","activity_state":"exercise"}`,
		})
		testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", StartSecs: &second,
			FilePath:     fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_001.mp4", profile.ID, session.SessionID),
			ExerciseType: "Thruster", ObservedSignals: `{"movement":"Thruster","activity_state":"exercise"}`,
		})
		testhelpers.MockGCSDownloadWithBody(storageTransport, available.FilePath, videoBytes)

		segments, _, timelineAvailable, _, err := w.buildSessionDebugSegments(context.Background(), session.SessionID, profile.ID, nil, WorkoutTypeWOD)
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(BeEmpty())
		Expect(timelineAvailable).To(BeFalse())
		var mapped int64
		Expect(database.Model(&db.ChunkAnalysisResult{}).Where("session_id = ? AND media_start_secs IS NOT NULL", session.SessionID).Count(&mapped).Error).To(Succeed())
		Expect(mapped).To(BeZero(), "a verified prefix must not hide the missing tail of the workout")
	})

	It("rebuilds the legacy timeline once per retained object and observes duplicate rows once", func() {
		first, second := 0.0, 10.0
		original := testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", StartSecs: &first,
			FilePath:     fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_000.mp4", profile.ID, session.SessionID),
			ExerciseType: "Thruster", ObservedSignals: `{"movement":"Thruster","activity_state":"exercise"}`,
		})
		duplicate := testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", StartSecs: &first,
			FilePath: original.FilePath, ExerciseType: "Thruster", ObservedSignals: `{"movement":"Thruster","activity_state":"exercise"}`,
		})
		next := testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", StartSecs: &second,
			FilePath:     fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_001.mp4", profile.ID, session.SessionID),
			ExerciseType: "Rest", ObservedSignals: `{"movement":"Rest","activity_state":"rest_setup"}`,
		})
		testhelpers.MockGCSDownloadWithBody(storageTransport, original.FilePath, videoBytes)
		testhelpers.MockGCSDownloadWithBody(storageTransport, next.FilePath, videoBytes)
		local := filepath.Join(GinkgoT().TempDir(), "probe.mp4")
		Expect(os.WriteFile(local, videoBytes, 0o600)).To(Succeed())
		duration := probeVideoDuration(context.Background(), local)
		Expect(duration).To(BeNumerically(">", 0))

		segments, skipped, timelineAvailable, _, err := w.buildSessionDebugSegments(context.Background(), session.SessionID, profile.ID, []sessionDebugCorrection{{
			ChunkID: duplicate.ID, MovementName: "Front Squat",
		}}, WorkoutTypeWOD)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(timelineAvailable).To(BeTrue())
		Expect(segments).To(HaveLen(1), "duplicate rows of one object are observed once")
		Expect(segments[0].Type).To(Equal("Front Squat"), "a correction saved on a duplicate row still applies")
		Expect(segments[0].Start).To(Equal("0:00"))
		Expect(skipped).To(Equal([]Segment{{Start: formatSegmentTimestamp(duration), End: formatSegmentTimestamp(2 * duration), Type: "Rest"}}))
		for _, id := range []uint{original.ID, duplicate.ID} {
			var row db.ChunkAnalysisResult
			Expect(database.First(&row, id).Error).To(Succeed())
			Expect(*row.MediaStartSecs).To(Equal(0.0))
			Expect(*row.MediaEndSecs).To(BeNumerically("~", duration, 0.001))
		}
		var tail db.ChunkAnalysisResult
		Expect(database.First(&tail, next.ID).Error).To(Succeed())
		Expect(*tail.MediaStartSecs).To(BeNumerically("~", duration, 0.001), "a duplicate row must not shift later offsets")
	})

	It("reuses stored workout_progress intervals only from the exact same observed source", func() {
		fileURI := baseURL + "/files/stored-progress"
		source, err := progressMediaSource(fileURI)
		Expect(err).NotTo(HaveOccurred())
		stored := workoutprogress.Build([]workoutprogress.Interval{
			{ID: "a", Start: 1, End: 20, Activity: "exercise", Movement: "Thruster"},
			{ID: "b", Start: 20, End: 30, Activity: "rest"},
			{ID: "c", Start: 30, End: 40, Activity: "unknown"},
		}, nil, source)
		raw, err := json.Marshal(stored)
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CreateAnalysisResult(database, &db.AnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: db.NullableJSONDocument(raw),
		})

		selected, skipped := w.storedProgressSegments(context.Background(), session.SessionID, profile.ID, WorkoutTypeWOD, reusableGeminiFile{URI: fileURI})
		Expect(selected).To(Equal([]Segment{
			{Start: "0:01", End: "0:20", Type: "Thruster", Description: "Previously observed interval; revalidate from visible evidence."},
			{Start: "0:30", End: "0:40", Type: "Unknown", Description: "Previously observed interval; revalidate from visible evidence."},
		}))
		Expect(skipped).To(Equal([]Segment{{Start: "0:20", End: "0:30", Type: "Rest"}}))

		recovery, recoverySkipped := w.storedProgressSegments(context.Background(), session.SessionID, profile.ID, WorkoutTypeCooldown, reusableGeminiFile{URI: fileURI})
		Expect(recovery).To(HaveLen(3), "recovery keeps rest-labelled holds for observation")
		Expect(recoverySkipped).To(BeEmpty())

		other, _ := w.storedProgressSegments(context.Background(), session.SessionID, profile.ID, WorkoutTypeWOD, reusableGeminiFile{URI: baseURL + "/files/other"})
		Expect(other).To(BeEmpty(), "another source's intervals are never applied")
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

	It("generates distinct request keys per task within a session reanalysis run", func() {
		startKey := sessionReanalysisTaskRequestKey(7, "video:wod-start")
		normKey := sessionReanalysisTaskRequestKey(7, "session:normalize-workout")
		synthKey := sessionReanalysisTaskRequestKey(7, "video:wod-synthesis")
		Expect(startKey).To(Equal("session-reanalysis:7:video:wod-start"))
		Expect(normKey).To(Equal("session-reanalysis:7:session:normalize-workout"))
		Expect(synthKey).To(Equal("session-reanalysis:7:video:wod-synthesis"))
		Expect(startKey).NotTo(Equal(normKey))
		Expect(normKey).NotTo(Equal(synthKey))
	})

	It("keys reusable segments by analysis mode", func() {
		grid := sessionDebugSegmentCacheKey(gemini.ModelArgon4, debugAnalysisModeFrameGrid, "0:00", "0:10", "prompt")
		video := sessionDebugSegmentCacheKey(gemini.ModelArgon4, debugAnalysisModeVideo, "0:00", "0:10", "prompt")
		Expect(grid).NotTo(Equal(video))
		Expect(chunkDebugAnalysisMode(true, 30*time.Second)).To(Equal(debugAnalysisModeFrameGrid))
		Expect(chunkDebugAnalysisMode(false, 30*time.Second)).To(Equal(debugAnalysisModeVideo))
		Expect(chunkDebugAnalysisMode(true, 10*time.Minute)).To(Equal(debugAnalysisModeVideo), "long intervals keep VideoMetadata analysis")
		Expect(chunkDebugPromptHash("prompt", debugAnalysisModeFrameGrid)).NotTo(Equal(chunkDebugPromptHash("prompt", debugAnalysisModeVideo)))
	})

	It("checkpoints state even after the task context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		persistCtx, done := sessionDebugPersistContext(ctx)
		defer done()
		Expect(persistCtx.Err()).NotTo(HaveOccurred())
	})
})

