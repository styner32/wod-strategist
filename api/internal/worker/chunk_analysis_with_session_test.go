package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("NewChunkAnalysisWithSessionTask", func() {
	It("includes timing fields in the payload", func() {
		task, err := NewChunkAnalysisWithSessionTask(
			"session-1",
			"gs://bucket/chunk.mp4",
			1,    // profileID
			10.5, // startSecs
			20.5, // endSecs
			110,  // heartRateBPM
			0.3,  // workoutConfidence
		)
		Expect(err).NotTo(HaveOccurred())

		var payload VideoAnalysisWithSessionPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.FilePath).To(Equal("gs://bucket/chunk.mp4"))
		Expect(payload.StartSecs).To(Equal(10.5))
		Expect(payload.EndSecs).To(Equal(20.5))
		Expect(payload.ProfileID).To(Equal(uint(1)))
		Expect(payload.HeartRateBPM).To(Equal(110))
		Expect(payload.WorkoutConfidence).To(Equal(0.3))
	})
})

var _ = Describe("HandleChunkAnalysisWithSessionTask", func() {
	const (
		geminiBaseURL   = "https://generativelanguage.googleapis.com"
		geminiAPIKey    = "test-api-key"
		chunkAnalysis   = "[EXERCISE: Deadlift]\n엉덩이를 더 내려주세요. 코어가 흔들리고 있습니다."
		chunkNoExercise = "[NO_EXERCISE]"
	)

	var (
		dbConn           *gorm.DB
		storageTransport *testhelpers.MockTransport
		queueClient      *asynq.Client
		w                *Worker

		profile   db.Profile
		sessionID string
	)

	setupGeminiTransport := func(generateContentText string) *testhelpers.MockTransport {
		transport := testhelpers.NewMockTransport()
		realClient, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:       geminiAPIKey,
			BaseURL:      geminiBaseURL,
			HTTPClient:   &http.Client{Transport: transport},
			PollInterval: time.Millisecond,
			Sleep:        func(time.Duration) {},
		})
		Expect(err).NotTo(HaveOccurred())
		w.GeminiClient = realClient

		transport.New(geminiBaseURL).
			Post("/upload/v1beta/files").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			Header("X-Goog-Upload-Url", geminiBaseURL+"/upload-session").
			JSON(map[string]any{})

		transport.New(geminiBaseURL).
			Post("/upload-session").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			Header("X-Goog-Upload-Status", "final").
			JSON(map[string]any{
				"file": map[string]any{
					"name": "files/mock-chunk",
					"uri":  geminiBaseURL + "/files/mock-chunk",
				},
			})

		transport.New(geminiBaseURL).
			Get("/v1beta/files/mock-chunk").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/mock-chunk", "state": "ACTIVE"})

		transport.New(geminiBaseURL).
			Post("/v1beta/models/"+gemini.ModelFlash35+":generateContent").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{{"text": generateContentText}},
					},
				}},
			})

		transport.New(geminiBaseURL).
			Delete("/v1beta/files/mock-chunk").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{})

		return transport
	}

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		storageTransport = testhelpers.NewMockTransport()
		storageClient, sErr := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(sErr).NotTo(HaveOccurred())

		queueClient = testhelpers.NewQueueClient()

		w = &Worker{
			DB:            dbConn,
			StorageClient: storageClient,
			GeminiClient:  nil,
			QueueClient:   queueClient,
			BucketName:    "test-bucket",
			logger:        zap.NewNop(),
		}

		injuries := "Right Ankle"
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{
			Injuries: &injuries,
		})

		sessionID = "WOD-202605221010-abcd"
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:      sessionID,
			ProfileID:      profile.ID,
			WODDescription: `2 Rounds of: 10 Reverse Burpees, 20 Devil Presses, 30 Dumbbell Cleans, 20 Dumbbell Box Step-overs`,
		})
	})

	It("persists a COMPLETED ChunkAnalysisResult", func() {
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/chunks/sess-chunk-001/chunk_001.mp4")
		transport := setupGeminiTransport(chunkAnalysis)

		task, err := NewChunkAnalysisWithSessionTask(
			sessionID,
			"gs://test-bucket/chunks/sess-chunk-001/chunk_001.mp4",
			profile.ID,
			0.0, 10.0,
			110,
			0.3,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(w.HandleChunkAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		Expect(transport.Requests()).To(HaveLen(5))

		var result db.ChunkAnalysisResult
		Expect(dbConn.Where("session_id = ?", sessionID).First(&result).Error).
			NotTo(HaveOccurred())
		Expect(result.Status).To(Equal("COMPLETED"))
		// Exercise type is now detected from the model response, not from user input
		Expect(result.ExerciseType).To(Equal("Deadlift"))
		// Output should be stripped of the exercise tag
		Expect(result.Output).To(Equal("엉덩이를 더 내려주세요. 코어가 흔들리고 있습니다."))
		Expect(result.StartSecs).NotTo(BeNil())
		Expect(*result.StartSecs).To(BeNumerically("~", 0.0))
		Expect(result.EndSecs).NotTo(BeNil())
		Expect(*result.EndSecs).To(BeNumerically("~", 10.0))
		Expect(result.WorkoutConfidence).To(Equal(0.3))
	})

	It("returns an error and saves no record when Gemini returns empty candidates", func() {
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/chunks/sess-chunk-001/chunk_001.mp4")

		transport := testhelpers.NewMockTransport()
		realClient, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:       geminiAPIKey,
			BaseURL:      geminiBaseURL,
			HTTPClient:   &http.Client{Transport: transport},
			PollInterval: time.Millisecond,
			Sleep:        func(time.Duration) {},
		})
		Expect(err).NotTo(HaveOccurred())
		w.GeminiClient = realClient

		transport.New(geminiBaseURL).
			Post("/upload/v1beta/files").
			Reply(http.StatusOK).
			Header("X-Goog-Upload-Url", geminiBaseURL+"/upload-session").
			JSON(map[string]any{})

		transport.New(geminiBaseURL).
			Post("/upload-session").
			Reply(http.StatusOK).
			Header("X-Goog-Upload-Status", "final").
			JSON(map[string]any{
				"file": map[string]any{
					"name": "files/mock-chunk",
					"uri":  geminiBaseURL + "/files/mock-chunk",
				},
			})

		transport.New(geminiBaseURL).
			Get("/v1beta/files/mock-chunk").
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/mock-chunk", "state": "ACTIVE"})

		transport.New(geminiBaseURL).
			Post("/v1beta/models/" + gemini.ModelFlash35 + ":generateContent").
			Reply(http.StatusOK).
			JSON(map[string]any{"candidates": []map[string]any{}})

		// geminiFile IS set so defer delete runs
		transport.New(geminiBaseURL).
			Delete("/v1beta/files/mock-chunk").
			Reply(http.StatusOK).
			JSON(map[string]any{})

		task, err := NewChunkAnalysisWithSessionTask(
			sessionID,
			"gs://test-bucket/chunks/sess-chunk-001/chunk_001.mp4",
			profile.ID,
			0.0, 10.0,
			110,
			0.3,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleChunkAnalysisWithSessionTask(context.Background(), task)
		Expect(err).To(HaveOccurred())
		Expect(transport.Verify()).To(Succeed())

		var count int64
		dbConn.Model(&db.ChunkAnalysisResult{}).Where("session_id = ?", sessionID).Count(&count)
		Expect(count).To(BeZero())
	})

	It("returns SkipRetry immediately when file path is not a GCS URI", func() {
		task, err := NewChunkAnalysisWithSessionTask(
			sessionID,
			"/local/path/chunk.mp4",
			profile.ID,
			0.0, 10.0,
			110,
			0.3,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleChunkAnalysisTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("invalid file path")))
	})
})
