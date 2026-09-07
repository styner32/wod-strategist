package worker

import (
	"context"
	"encoding/json"
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

var _ = Describe("chunk debug re-analysis helpers", func() {
	Describe("NewChunkDebugReanalysisTask", func() {
		It("serializes only the authoritative run ID", func() {
			task, err := NewChunkDebugReanalysisTask(42)
			Expect(err).NotTo(HaveOccurred())
			Expect(task.Type()).To(Equal(TypeChunkDebugReanalysis))

			var payload map[string]any
			Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
			Expect(payload).To(Equal(map[string]any{"run_id": float64(42)}))
		})

		It("rejects a missing run ID", func() {
			_, err := NewChunkDebugReanalysisTask(0)
			Expect(err).To(MatchError("run_id is required"))
		})
	})

	Describe("selectChunkDebugSessionVideo", func() {
		It("prefers the analysis copy while ignoring derived chunks", func() {
			objects := []string{
				"videos/1/s/chunk_001.mp4",
				"videos/1/s/split_chunk_000.mp4",
				"videos/1/s/merged.mp4",
				"videos/1/s/analysis.mp4",
			}
			Expect(selectChunkDebugSessionVideo(objects)).To(Equal("videos/1/s/analysis.mp4"))
		})

		It("supports the legacy merged filename", func() {
			objects := []string{"videos/P1-WOD-2026-01-01-12-00_merged_123.mp4"}
			Expect(selectChunkDebugSessionVideo(objects)).To(Equal(objects[0]))
		})

		It("rejects arbitrary retained mobile chunks and derived hardsubs", func() {
			objects := []string{
				"videos/1/s/A1B2C3D4.mp4",
				"videos/1/s/hardsubbed.mp4",
				"videos/1/s/hl_full_abcd.mp4",
			}
			Expect(selectChunkDebugSessionVideo(objects)).To(BeEmpty())
		})
	})

	Describe("parseChunkDebugCandidate", func() {
		It("keeps the detected movement, coaching, and structured signals separate", func() {
			candidate := parseChunkDebugCandidate(`[EXERCISE: Pull-up]
바를 당길 때 갈비뼈를 아래로 유지하세요.
` + "```observed_signals\n" + `{"movement":"Pull-up","rep_count":3}` + "\n```")

			Expect(candidate.ExerciseType).To(Equal("Pull-up"))
			Expect(candidate.Output).To(Equal("바를 당길 때 갈비뼈를 아래로 유지하세요."))
			Expect(candidate.ObservedSignals).To(HaveKeyWithValue("movement", "Pull-up"))
		})

		It("represents walking or rest as no exercise", func() {
			candidate := parseChunkDebugCandidate("[NO_EXERCISE]")
			Expect(candidate.ExerciseType).To(BeEmpty())
			Expect(candidate.Output).To(BeEmpty())
		})
	})

	Describe("buildChunkDebugReanalysisPrompt", func() {
		It("treats WOD and heart rate as non-exclusive supporting context", func() {
			w := NewWorker(nil, nil, "", nil, nil, zap.NewNop())
			injuries := "[]"
			prompt := w.buildChunkDebugReanalysisPrompt(&chunkDebugTarget{
				ProfileID:       1,
				WODDescription:  "Pull-ups",
				MovementHints:   db.JSONDocument(`["Pull-up","Custom Carry"]`),
				HeartRateBPM:    165,
				ProfileInjuries: &injuries,
			})

			Expect(prompt).To(ContainSubstring("불완전하고 비배타적인 참고 정보"))
			Expect(prompt).To(ContainSubstring("Pull-up, Custom Carry"))
			Expect(prompt).To(ContainSubstring("심박수만으로 피로를 판정"))
			Expect(prompt).To(ContainSubstring("주변 기구나 배경 인물은 종목 근거가 아닙니다"))
			Expect(prompt).NotTo(ContainSubstring("기존 분석 결과:"))
			Expect(prompt).NotTo(ContainSubstring("개인 종목 혼동 참고"))
		})
	})

	Describe("media interval guards", func() {
		It("accepts only ordered non-negative media-relative offsets", func() {
			start, end := 10.25, 20.75
			Expect(debugMediaIntervalValid(&start, &end)).To(BeTrue())
			end = start
			Expect(debugMediaIntervalValid(&start, &end)).To(BeFalse())
			Expect(debugMediaIntervalValid(nil, &end)).To(BeFalse())
		})

		It("recognizes every persisted terminal state", func() {
			for _, status := range []string{
				db.ChunkReanalysisStatusCompleted,
				db.ChunkReanalysisStatusFailed,
				db.ChunkReanalysisStatusVideoUnavailable,
				db.ChunkReanalysisStatusIntervalUnavailable,
			} {
				Expect(isChunkReanalysisTerminal(status)).To(BeTrue(), status)
			}
			Expect(isChunkReanalysisTerminal(db.ChunkReanalysisStatusQueued)).To(BeFalse())
			Expect(secondsDuration(1.25)).To(Equal(1250 * time.Millisecond))
			Expect(chunkDebugMIMEType("")).To(Equal("video/mp4"))
		})
	})

	It("does not expose storage locations through the persisted model JSON", func() {
		run := db.ChunkReanalysisRun{
			SourceGCSURI:  "gs://private/session.mp4",
			GeminiFileURI: "https://private/files/1",
		}
		encoded, err := json.Marshal(run)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(encoded), "private")).To(BeFalse())
	})
})

var _ = Describe("HandleChunkDebugReanalysisTask", func() {
	const (
		geminiBaseURL = "https://example.test"
		geminiAPIKey  = "test-api-key"
	)

	var (
		dbConn           *gorm.DB
		storageTransport *testhelpers.MockTransport
		geminiTransport  *testhelpers.MockTransport
		inspector        *asynq.Inspector
		w                *Worker
		profile          db.Profile
		session          db.Session
	)

	newGeminiClient := func() {
		var err error
		w.GeminiClient, err = gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:       geminiAPIKey,
			BaseURL:      geminiBaseURL,
			HTTPClient:   &http.Client{Transport: geminiTransport},
			PollInterval: time.Millisecond,
			Sleep:        func(time.Duration) {},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	expectSegmentAnalysis := func(fileURI string) {
		geminiTransport.New(geminiBaseURL).
			Post("/v1beta/models/"+gemini.ModelPro31Preview+":generateContent").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			MatchBodyContains(`"fileUri":"` + fileURI + `"`).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{{"text": `[EXERCISE: Pull-up]
대상 인물이 바를 직접 잡고 당기고 있습니다.
` + "```observed_signals\n" + `{"movement":"Pull-up","rep_count":3,"fatigue_detected":false}` + "\n```"}},
					},
				}},
				"usageMetadata": map[string]any{
					"promptTokenCount":     11,
					"candidatesTokenCount": 7,
					"totalTokenCount":      18,
				},
			})
	}

	seedRun := func(clientRequestID, filePath string, start, end *float64) (db.ChunkAnalysisResult, db.ChunkReanalysisRun) {
		chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:         session.SessionID,
			ProfileID:         profile.ID,
			FilePath:          filePath,
			ExerciseType:      "Rope Climb",
			Status:            "COMPLETED",
			Output:            "original coaching",
			ObservedSignals:   `{"movement":"Rope Climb","fatigue_detected":false}`,
			HeartRateBPM:      142,
			MediaStartSecs:    start,
			MediaEndSecs:      end,
			WorkoutConfidence: 0.8,
		})
		run := testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
			SessionID:             session.SessionID,
			ProfileID:             profile.ID,
			ChunkAnalysisResultID: chunk.ID,
			ClientRequestID:       clientRequestID,
			Status:                db.ChunkReanalysisStatusQueued,
		})
		return chunk, run
	}

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		storageTransport = testhelpers.NewMockTransport()
		storageClient, storageErr := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(storageErr).NotTo(HaveOccurred())
		geminiTransport = testhelpers.NewMockTransport()
		queueClient := testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		testhelpers.CleanupQueue(inspector)
		w = NewWorker(dbConn, storageClient, "test-bucket", nil, queueClient, zap.NewNop())
		newGeminiClient()

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:      "WOD-20260715-01JDEBUGREANALYSIS000",
			ProfileID:      profile.ID,
			WODDescription: "5 rounds of Pull-ups",
			WorkoutType:    "wod",
		})
	})

	AfterEach(func() {
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty(), "debug re-analysis must not enqueue derived work")
	})

	It("reuses a valid exact-source Files upload and analyzes the exact persisted media interval without mutating production results", func() {
		start, end := 12.25, 19.75
		chunk, run := seedRun("reuse-analysis-file", "gs://test-bucket/videos/1/chunk_001.mp4", &start, &end)
		expires := time.Now().UTC().Add(time.Hour)
		sourceURI := "gs://test-bucket/videos/1/" + session.SessionID + "/merged.mp4"
		testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
			SessionID:             session.SessionID,
			ProfileID:             profile.ID,
			ChunkAnalysisResultID: chunk.ID,
			ClientRequestID:       "prior-exact-source-cache",
			Status:                db.ChunkReanalysisStatusCompleted,
			SourceKind:            db.ChunkReanalysisSourceSessionVideo,
			SourceGCSURI:          sourceURI,
			GeminiFileURI:         geminiBaseURL + "/files/cached-session",
			GeminiFileName:        "files/cached-session",
			GeminiMIMEType:        "video/mp4",
			GeminiFileExpiresAt:   &expires,
		})
		originalAnalysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:           session.SessionID,
			ProfileID:           profile.ID,
			Status:              "COMPLETED",
			Output:              "immutable final analysis",
			HighlightSegments:   `[{"start":"0:01","end":"0:02","type":"key_moment","reason":"original"}]`,
			GeminiFileURI:       geminiBaseURL + "/files/unverified-production-cache",
			GeminiFileName:      "files/unverified-production-cache",
			GeminiMIMEType:      "video/mp4",
			GeminiFileExpiresAt: &expires,
		})

		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/1/"+session.SessionID+"/", []string{
			"videos/1/" + session.SessionID + "/merged.mp4",
		})
		geminiTransport.New(geminiBaseURL).
			Get("/v1beta/files/cached-session").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/cached-session", "state": "ACTIVE"})
		expectSegmentAnalysis(geminiBaseURL + "/files/cached-session")

		task, err := NewChunkDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleChunkDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		var analyzeBody map[string]any
		for _, request := range geminiTransport.Requests() {
			if strings.Contains(request.URL, gemini.ModelPro31Preview+":generateContent") {
				Expect(json.Unmarshal(request.Body, &analyzeBody)).To(Succeed())
			}
		}
		startOffset, endOffset := segmentOffsetsFromRequest(analyzeBody)
		Expect(startOffset).To(Equal("12.250s"))
		Expect(endOffset).To(Equal("19.750s"))

		var completed db.ChunkReanalysisRun
		Expect(dbConn.First(&completed, run.ID).Error).To(Succeed())
		Expect(completed.Status).To(Equal(db.ChunkReanalysisStatusCompleted))
		Expect(completed.SourceKind).To(Equal(db.ChunkReanalysisSourceSessionVideo))
		Expect(completed.SourceGCSURI).To(Equal("gs://test-bucket/videos/1/" + session.SessionID + "/merged.mp4"))
		Expect(completed.MediaStartSecs).NotTo(BeNil())
		Expect(*completed.MediaStartSecs).To(BeNumerically("~", start))
		Expect(completed.MediaEndSecs).NotTo(BeNil())
		Expect(*completed.MediaEndSecs).To(BeNumerically("~", end))
		Expect(completed.Model).To(Equal(gemini.ModelPro31Preview))
		Expect(completed.PromptTokens).To(Equal(int32(11)))
		Expect(completed.CandidateTokens).To(Equal(int32(7)))
		Expect(completed.TotalTokens).To(Equal(int32(18)))
		Expect(string(completed.StructuredCandidate)).To(MatchJSON(`{"exercise_type":"Pull-up","output":"대상 인물이 바를 직접 잡고 당기고 있습니다.","observed_signals":{"fatigue_detected":false,"movement":"Pull-up","rep_count":3}}`))

		var persistedChunk db.ChunkAnalysisResult
		Expect(dbConn.First(&persistedChunk, chunk.ID).Error).To(Succeed())
		Expect(persistedChunk.Status).To(Equal(chunk.Status))
		Expect(persistedChunk.ExerciseType).To(Equal(chunk.ExerciseType))
		Expect(persistedChunk.Output).To(Equal(chunk.Output))
		Expect(persistedChunk.ObservedSignals).To(Equal(chunk.ObservedSignals))
		var persistedAnalysis db.AnalysisResult
		Expect(dbConn.First(&persistedAnalysis, originalAnalysis.ID).Error).To(Succeed())
		Expect(persistedAnalysis.Output).To(Equal(originalAnalysis.Output))
		Expect(persistedAnalysis.HighlightSegments).To(Equal(originalAnalysis.HighlightSegments))
		var chunkCount, analysisCount int64
		Expect(dbConn.Model(&db.ChunkAnalysisResult{}).Count(&chunkCount).Error).To(Succeed())
		Expect(dbConn.Model(&db.AnalysisResult{}).Count(&analysisCount).Error).To(Succeed())
		Expect(chunkCount).To(Equal(int64(1)))
		Expect(analysisCount).To(Equal(int64(1)))
		for _, request := range storageTransport.Requests() {
			Expect(request.Method).To(Equal(http.MethodGet), "re-analysis must not create a clipped GCS object")
		}
	})

	It("falls back to analysis_results.wod_description when the sessions row is missing (legacy session)", func() {
		Expect(dbConn.Delete(&session).Error).To(Succeed())

		start, end := 12.25, 19.75
		chunk, run := seedRun("fallback-wod-desc", "gs://test-bucket/videos/1/chunk_001.mp4", &start, &end)
		expires := time.Now().UTC().Add(time.Hour)
		sourceURI := "gs://test-bucket/videos/1/" + session.SessionID + "/merged.mp4"
		testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
			SessionID:             session.SessionID,
			ProfileID:             profile.ID,
			ChunkAnalysisResultID: chunk.ID,
			ClientRequestID:       "prior-fallback-wod-cache",
			Status:                db.ChunkReanalysisStatusCompleted,
			SourceKind:            db.ChunkReanalysisSourceSessionVideo,
			SourceGCSURI:          sourceURI,
			GeminiFileURI:         geminiBaseURL + "/files/cached-session",
			GeminiFileName:        "files/cached-session",
			GeminiMIMEType:        "video/mp4",
			GeminiFileExpiresAt:   &expires,
		})

		originalAnalysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:      session.SessionID,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			Output:         "immutable final analysis",
			WODDescription: "Legacy 3 rounds of Burpees",
		})

		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/1/"+session.SessionID+"/", []string{
			"videos/1/" + session.SessionID + "/merged.mp4",
		})
		geminiTransport.New(geminiBaseURL).
			Get("/v1beta/files/cached-session").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/cached-session", "state": "ACTIVE"})
		expectSegmentAnalysis(geminiBaseURL + "/files/cached-session")

		task, err := NewChunkDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleChunkDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		var completed db.ChunkReanalysisRun
		Expect(dbConn.First(&completed, run.ID).Error).To(Succeed())
		var contextMap map[string]any
		Expect(json.Unmarshal([]byte(completed.SourceContextSnapshot), &contextMap)).To(Succeed())
		Expect(contextMap["wod_description"]).To(Equal("Legacy 3 rounds of Burpees"))

		var promptBody map[string]any
		for _, request := range geminiTransport.Requests() {
			if strings.Contains(request.URL, gemini.ModelPro31Preview+":generateContent") {
				Expect(json.Unmarshal(request.Body, &promptBody)).To(Succeed())
			}
		}
		contents, _ := promptBody["contents"].([]any)
		Expect(contents).NotTo(BeEmpty())
		content0, _ := contents[0].(map[string]any)
		parts, _ := content0["parts"].([]any)
		var promptText string
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if text, ok := part["text"].(string); ok {
				promptText += text
			}
		}
		Expect(promptText).To(ContainSubstring("Legacy 3 rounds of Burpees"))

		Expect(dbConn.Delete(&originalAnalysis).Error).To(Succeed())
	})

	It("falls back to analysis_results.wod_description when the sessions value is blank", func() {
		Expect(dbConn.Model(&db.Session{}).Where("id = ?", session.ID).
			Update("wod_description", " \n\t ").Error).To(Succeed())

		start, end := 12.25, 19.75
		_, run := seedRun("fallback-blank-wod-desc", "gs://test-bucket/videos/1/chunk_001.mp4", &start, &end)
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:      session.SessionID,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			Output:         "immutable final analysis",
			WODDescription: "Legacy 3 rounds of Burpees",
		})

		target, err := w.loadChunkDebugTarget(context.Background(), &run)
		Expect(err).NotTo(HaveOccurred())
		Expect(target.WODDescription).To(Equal("Legacy 3 rounds of Burpees"))
	})

	It("uploads the GCS session video once and reuses that cached Files upload on the next run", func() {
		start, end := 4.5, 9.25
		chunk, firstRun := seedRun("upload-fallback-1", "gs://test-bucket/videos/1/chunk_001.mp4", &start, &end)
		objectName := "videos/1/" + session.SessionID + "/merged.mp4"
		sourceURI := "gs://test-bucket/" + objectName

		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/1/"+session.SessionID+"/", []string{objectName})
		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/1/"+session.SessionID+"/", []string{objectName})
		testhelpers.MockGCSDownloadWithBody(storageTransport, sourceURI, []byte("session-video"))

		geminiTransport.New(geminiBaseURL).
			Post("/upload/v1beta/files").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			Header("X-Goog-Upload-Url", geminiBaseURL+"/upload-session").
			JSON(map[string]any{})
		geminiTransport.New(geminiBaseURL).
			Post("/upload-session").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			Header("X-Goog-Upload-Status", "final").
			JSON(map[string]any{"file": map[string]any{
				"name": "files/uploaded-session",
				"uri":  geminiBaseURL + "/files/uploaded-session",
			}})
		geminiTransport.New(geminiBaseURL).
			Get("/v1beta/files/uploaded-session").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"name":          "files/uploaded-session",
				"state":         "ACTIVE",
				"videoMetadata": map[string]any{"videoDuration": "60s"},
			})
		expectSegmentAnalysis(geminiBaseURL + "/files/uploaded-session")

		firstTask, err := NewChunkDebugReanalysisTask(firstRun.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleChunkDebugReanalysisTask(context.Background(), firstTask)).To(Succeed())

		secondRun := testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
			SessionID:             session.SessionID,
			ProfileID:             profile.ID,
			ChunkAnalysisResultID: chunk.ID,
			ClientRequestID:       "upload-fallback-2",
			Status:                db.ChunkReanalysisStatusQueued,
		})
		geminiTransport.New(geminiBaseURL).
			Get("/v1beta/files/uploaded-session").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/uploaded-session", "state": "ACTIVE"})
		expectSegmentAnalysis(geminiBaseURL + "/files/uploaded-session")

		secondTask, err := NewChunkDebugReanalysisTask(secondRun.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleChunkDebugReanalysisTask(context.Background(), secondTask)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		var firstCompleted, secondCompleted db.ChunkReanalysisRun
		Expect(dbConn.First(&firstCompleted, firstRun.ID).Error).To(Succeed())
		Expect(dbConn.First(&secondCompleted, secondRun.ID).Error).To(Succeed())
		Expect(firstCompleted.Status).To(Equal(db.ChunkReanalysisStatusCompleted))
		Expect(secondCompleted.Status).To(Equal(db.ChunkReanalysisStatusCompleted))
		Expect(firstCompleted.GeminiFileURI).To(Equal(geminiBaseURL + "/files/uploaded-session"))
		Expect(secondCompleted.GeminiFileURI).To(BeEmpty(), "the second run references the reusable cache without duplicating cache metadata")

		downloads := 0
		for _, request := range storageTransport.Requests() {
			Expect(request.Method).To(Equal(http.MethodGet), "re-analysis must not upload a GCS clip")
			if !strings.Contains(request.URL, "/storage/v1/b/") {
				downloads++
			}
		}
		Expect(downloads).To(Equal(1), "the cached Gemini file must avoid a second GCS download")
	})

	It("uses the retained chunk's probed duration instead of its capture-clock delta", func() {
		mediaStart, mediaEnd := 100.0, 110.0
		chunkURI := "gs://test-bucket/videos/1/" + session.SessionID + "/chunk_001.mp4"
		chunk, run := seedRun("retained-chunk-duration", chunkURI, &mediaStart, &mediaEnd)
		captureStart, captureEnd := 500.0, 530.0
		Expect(dbConn.Model(&db.ChunkAnalysisResult{}).Where("id = ?", chunk.ID).Updates(map[string]any{
			"start_secs": captureStart,
			"end_secs":   captureEnd,
		}).Error).To(Succeed())

		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/1/"+session.SessionID+"/", nil)
		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/"+session.SessionID+"_", nil)
		testhelpers.MockGCSDownloadWithBody(storageTransport, chunkURI, []byte("retained-chunk-video"))
		geminiTransport.New(geminiBaseURL).
			Post("/upload/v1beta/files").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			Header("X-Goog-Upload-Url", geminiBaseURL+"/upload-retained-chunk").
			JSON(map[string]any{})
		geminiTransport.New(geminiBaseURL).
			Post("/upload-retained-chunk").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			Header("X-Goog-Upload-Status", "final").
			JSON(map[string]any{"file": map[string]any{
				"name": "files/retained-chunk",
				"uri":  geminiBaseURL + "/files/retained-chunk",
			}})
		geminiTransport.New(geminiBaseURL).
			Get("/v1beta/files/retained-chunk").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"name":          "files/retained-chunk",
				"state":         "ACTIVE",
				"videoMetadata": map[string]any{"videoDuration": "2.5s"},
			})
		expectSegmentAnalysis(geminiBaseURL + "/files/retained-chunk")

		task, err := NewChunkDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleChunkDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		var analyzeBody map[string]any
		for _, request := range geminiTransport.Requests() {
			if strings.Contains(request.URL, gemini.ModelPro31Preview+":generateContent") {
				Expect(json.Unmarshal(request.Body, &analyzeBody)).To(Succeed())
			}
		}
		startOffset, endOffset := segmentOffsetsFromRequest(analyzeBody)
		Expect(startOffset).To(Equal("0s"))
		Expect(endOffset).To(Equal("2.500s"))

		var completed db.ChunkReanalysisRun
		Expect(dbConn.First(&completed, run.ID).Error).To(Succeed())
		Expect(completed.Status).To(Equal(db.ChunkReanalysisStatusCompleted))
		Expect(completed.SourceKind).To(Equal(db.ChunkReanalysisSourceChunk))
		Expect(completed.MediaStartSecs).NotTo(BeNil())
		Expect(*completed.MediaStartSecs).To(Equal(0.0))
		Expect(completed.MediaEndSecs).NotTo(BeNil())
		Expect(*completed.MediaEndSecs).To(Equal(2.5))
	})

	It("records VIDEO_UNAVAILABLE when an exact interval has no session video or retained chunk", func() {
		start, end := 1.0, 3.0
		_, run := seedRun("missing-video", "", &start, &end)
		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/1/"+session.SessionID+"/", nil)
		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/"+session.SessionID+"_", nil)

		task, err := NewChunkDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleChunkDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(geminiTransport.Requests()).To(BeEmpty())

		var completed db.ChunkReanalysisRun
		Expect(dbConn.First(&completed, run.ID).Error).To(Succeed())
		Expect(completed.Status).To(Equal(db.ChunkReanalysisStatusVideoUnavailable))
		Expect(completed.SafeError).To(Equal("The session video is unavailable."))
	})

	It("records INTERVAL_UNAVAILABLE rather than using unverified capture-clock offsets", func() {
		captureStart, captureEnd := 120.0, 130.0
		chunk, run := seedRun("missing-interval", "", nil, nil)
		Expect(dbConn.Model(&db.ChunkAnalysisResult{}).Where("id = ?", chunk.ID).Updates(map[string]any{
			"start_secs": captureStart,
			"end_secs":   captureEnd,
		}).Error).To(Succeed())

		task, err := NewChunkDebugReanalysisTask(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleChunkDebugReanalysisTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Requests()).To(BeEmpty())
		Expect(geminiTransport.Requests()).To(BeEmpty())

		var completed db.ChunkReanalysisRun
		Expect(dbConn.First(&completed, run.ID).Error).To(Succeed())
		Expect(completed.Status).To(Equal(db.ChunkReanalysisStatusIntervalUnavailable))
		Expect(completed.MediaStartSecs).To(BeNil())
		Expect(completed.MediaEndSecs).To(BeNil())
		Expect(completed.SafeError).To(Equal("An exact media interval is unavailable for this chunk."))
	})
})

func segmentOffsetsFromRequest(body map[string]any) (string, string) {
	contents, _ := body["contents"].([]any)
	if len(contents) == 0 {
		return "", ""
	}
	content, _ := contents[0].(map[string]any)
	parts, _ := content["parts"].([]any)
	for _, rawPart := range parts {
		part, _ := rawPart.(map[string]any)
		metadata, _ := part["videoMetadata"].(map[string]any)
		if metadata == nil {
			continue
		}
		start, _ := metadata["startOffset"].(string)
		end, _ := metadata["endOffset"].(string)
		return start, end
	}
	return "", ""
}
