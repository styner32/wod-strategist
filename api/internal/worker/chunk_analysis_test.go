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

var _ = Describe("buildChunkAnalysisPrompt", func() {
	var w *Worker

	BeforeEach(func() {
		w = &Worker{logger: zap.NewNop()}
	})

	It("includes movements, injuries, and profile in the chunk prompt", func() {
		prompt := w.buildChunkAnalysisPrompt(VideoAnalysisPayload{
			Movements: []string{"Deadlift"},
			Injuries:  []string{"Knee"},
		})

		Expect(prompt).To(ContainSubstring("실시간 피드백"))
		Expect(prompt).To(ContainSubstring("확인된 운동 종목"))
		Expect(prompt).To(ContainSubstring("확실히 수행되는 운동"))
		Expect(prompt).To(ContainSubstring("Deadlift"))
		Expect(prompt).To(ContainSubstring("## 알려진 부상 사항"))
		Expect(prompt).To(ContainSubstring("Knee"))
		Expect(prompt).To(ContainSubstring("반드시 경고하세요"))
		Expect(prompt).To(ContainSubstring("## 개인 프로필"))
	})

	It("still includes profile even without movements/injuries", func() {
		prompt := w.buildChunkAnalysisPrompt(VideoAnalysisPayload{})

		Expect(prompt).To(ContainSubstring("## 개인 프로필"))
		Expect(prompt).NotTo(ContainSubstring("확인된 운동 종목"))
		Expect(prompt).NotTo(ContainSubstring("## 알려진 부상 사항"))
	})
})

var _ = Describe("NewChunkAnalysisTask", func() {

	It("includes timing fields in the payload", func() {
		task, err := NewChunkAnalysisTask(
			"session-1",
			"gs://bucket/chunk.mp4",
			"wod",
			[]string{"Deadlift"},
			nil,
			7,
			10.5,
			20.5,
		)
		Expect(err).NotTo(HaveOccurred())

		var payload VideoAnalysisPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.StartSecs).To(Equal(10.5))
		Expect(payload.EndSecs).To(Equal(20.5))
		Expect(payload.Movements).To(Equal([]string{"Deadlift"}))
		Expect(payload.ProfileID).To(Equal(uint(7)))
	})
})

var _ = Describe("HandleChunkAnalysisTask", func() {
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
			Post("/v1beta/models/gemini-3.1-pro-preview:generateContent").
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
	})

	It("persists a COMPLETED ChunkAnalysisResult", func() {
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/chunks/sess-chunk-001/chunk_001.mp4")
		transport := setupGeminiTransport(chunkAnalysis)

		task, err := NewChunkAnalysisTask(
			"sess-chunk-001",
			"gs://test-bucket/chunks/sess-chunk-001/chunk_001.mp4",
			WorkoutTypeWOD,
			[]string{"Deadlift"},
			nil,
			0,
			0.0, 10.0,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(w.HandleChunkAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		Expect(transport.Requests()).To(HaveLen(5))

		var result db.ChunkAnalysisResult
		Expect(dbConn.Where("session_id = ?", "sess-chunk-001").First(&result).Error).
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
	})

	It("sets ProfileID on the result when ProfileID > 0", func() {
		profile := db.Profile{
			BirthYear: 1988, BirthMonth: 3, BirthDay: 10,
			Gender: "female", HeightCm: 165, WeightKg: 60.0,
		}
		Expect(dbConn.Create(&profile).Error).NotTo(HaveOccurred())

		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/chunks/sess-chunk-profile-001/chunk_001.mp4")
		transport := setupGeminiTransport(chunkAnalysis)

		task, err := NewChunkAnalysisTask(
			"sess-chunk-profile-001",
			"gs://test-bucket/chunks/sess-chunk-profile-001/chunk_001.mp4",
			WorkoutTypeWOD,
			nil, nil,
			profile.ID,
			5.0, 15.0,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(w.HandleChunkAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		var result db.ChunkAnalysisResult
		Expect(dbConn.Where("session_id = ?", "sess-chunk-profile-001").First(&result).Error).
			NotTo(HaveOccurred())
		Expect(result.ProfileID).NotTo(BeNil())
		Expect(*result.ProfileID).To(Equal(profile.ID))
	})

	It("returns an error and saves no record when Gemini returns empty candidates", func() {
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/chunks/sess-chunk-empty-001/chunk.mp4")

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
			Post("/v1beta/models/gemini-3.1-pro-preview:generateContent").
			Reply(http.StatusOK).
			JSON(map[string]any{"candidates": []map[string]any{}})

		// geminiFile IS set so defer delete runs
		transport.New(geminiBaseURL).
			Delete("/v1beta/files/mock-chunk").
			Reply(http.StatusOK).
			JSON(map[string]any{})

		task, err := NewChunkAnalysisTask(
			"sess-chunk-empty-001",
			"gs://test-bucket/chunks/sess-chunk-empty-001/chunk.mp4",
			WorkoutTypeWOD,
			nil, nil, 0, 0, 10,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleChunkAnalysisTask(context.Background(), task)
		Expect(err).To(HaveOccurred())
		Expect(transport.Verify()).To(Succeed())

		var count int64
		dbConn.Model(&db.ChunkAnalysisResult{}).Where("session_id = ?", "sess-chunk-empty-001").Count(&count)
		Expect(count).To(BeZero())
	})

	It("returns SkipRetry immediately when file path is not a GCS URI", func() {
		task, err := NewChunkAnalysisTask(
			"sess-chunk-baduri-001",
			"/local/path/chunk.mp4",
			WorkoutTypeWOD,
			nil, nil, 0, 0, 10,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleChunkAnalysisTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("invalid file path")))
	})
})
