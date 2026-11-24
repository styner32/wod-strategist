package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/oklog/ulid/v2"
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
			Post("/v1beta/models/"+gemini.ModelHydrogen4+":generateContent").
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

		sessionID = "WOD-20261124-" + ulid.Make().String()
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

		Expect(w.HandleChunkAnalysisWithSessionTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
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
			Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").
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

		err = w.HandleChunkAnalysisWithSessionTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("invalid file path")))
		Expect(errors.Is(err, asynq.SkipRetry)).To(BeTrue())
	})

	It("marks chunk as FAILED and returns SkipRetry when retry count reaches 3", func() {
		task, err := NewChunkAnalysisWithSessionTask(
			sessionID,
			"gs://test-bucket/chunks/sess-chunk-retry-002/chunk_001.mp4",
			profile.ID,
			2.0, 12.0,
			120,
			0.6,
		)
		Expect(err).NotTo(HaveOccurred())

		ctx := WithRetryCount(context.Background(), 3)
		err = w.HandleChunkAnalysisWithSessionTask(ctx, task)
		Expect(err).To(Equal(asynq.SkipRetry))

		var result db.ChunkAnalysisResult
		Expect(dbConn.Where("session_id = ? AND file_path = ?", sessionID, "gs://test-bucket/chunks/sess-chunk-retry-002/chunk_001.mp4").First(&result).Error).
			NotTo(HaveOccurred())
		Expect(result.Status).To(Equal("FAILED"))
		Expect(result.Output).To(Equal("Chunk analysis failed after max retries."))
		Expect(result.WorkoutConfidence).To(Equal(0.6))
		Expect(result.StartSecs).NotTo(BeNil())
		Expect(*result.StartSecs).To(Equal(2.0))
		Expect(result.EndSecs).NotTo(BeNil())
		Expect(*result.EndSecs).To(Equal(12.0))
		Expect(w.HandleChunkAnalysisWithSessionTask(ctx, task)).To(Equal(asynq.SkipRetry))
		var count int64
		Expect(dbConn.Model(&db.ChunkAnalysisResult{}).Where("session_id = ? AND file_path = ?", result.SessionID, result.FilePath).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))

	})

	It("returns SkipRetry without storage access when session belongs to another profile", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		otherSessionID := "WOD-20261124-01JOTHERPROFILE000000000000"
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:   otherSessionID,
			ProfileID:   otherProfile.ID,
			Status:      db.SessionStatusStarted,
			WorkoutType: WorkoutTypeWOD,
		})

		task, err := NewChunkAnalysisWithSessionTask(
			otherSessionID,
			"gs://test-bucket/chunks/other/chunk_001.mp4",
			profile.ID,
			0.0, 10.0,
			110,
			0.3,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleChunkAnalysisWithSessionTask(context.Background(), task)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, asynq.SkipRetry)).To(BeTrue())
		Expect(storageTransport.Requests()).To(BeEmpty())

		var count int64
		Expect(dbConn.Model(&db.ChunkAnalysisResult{}).Where("session_id = ?", otherSessionID).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	DescribeTable("rejects invalid session IDs with SkipRetry before storage access",
		func(invalidSID string) {
			task, err := NewChunkAnalysisWithSessionTask(
				invalidSID,
				"gs://test-bucket/chunks/invalid/chunk_001.mp4",
				profile.ID,
				0.0, 10.0,
				110,
				0.3,
			)
			Expect(err).NotTo(HaveOccurred())

			err = w.HandleChunkAnalysisWithSessionTask(context.Background(), task)
			Expect(err).To(MatchError(ContainSubstring("invalid session ID")))
			Expect(errors.Is(err, asynq.SkipRetry)).To(BeTrue())
			Expect(storageTransport.Requests()).To(BeEmpty())
		},
		Entry("empty session ID", ""),
		Entry("path traversal session ID", "../escape"),
		Entry("malformed session ID", "bad session id"),
	)

	It("removes the temporary chunk file when GCS download fails", func() {
		tmpDir := GinkgoT().TempDir()
		GinkgoT().Setenv("TMPDIR", tmpDir)

		storageTransport.New(testhelpers.GCSBaseURL).
			Get("/test-bucket/chunks/sess-download-fail/chunk_001.mp4").
			Reply(http.StatusNotFound).
			BodyString("not found")

		task, err := NewChunkAnalysisWithSessionTask(
			sessionID,
			"gs://test-bucket/chunks/sess-download-fail/chunk_001.mp4",
			profile.ID,
			0.0, 10.0,
			110,
			0.3,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleChunkAnalysisWithSessionTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("failed to download chunk file from GCS")))
		Expect(storageTransport.Verify()).To(Succeed())

		matches, globErr := filepath.Glob(filepath.Join(tmpDir, "chunk-*.mp4"))
		Expect(globErr).NotTo(HaveOccurred())
		Expect(matches).To(BeEmpty())
	})

	It("propagates a canceled context to profile and session DB lookups", func() {
		tmpDir := GinkgoT().TempDir()
		GinkgoT().Setenv("TMPDIR", tmpDir)

		task, err := NewChunkAnalysisWithSessionTask(
			sessionID,
			"gs://test-bucket/chunks/sess-canceled/chunk_001.mp4",
			profile.ID,
			0.0, 10.0,
			110,
			0.3,
		)
		Expect(err).NotTo(HaveOccurred())

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		err = w.handleChunkAnalysisWithSessionTask(canceledCtx, task)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		Expect(storageTransport.Requests()).To(BeEmpty())

		matches, globErr := filepath.Glob(filepath.Join(tmpDir, "chunk-*.mp4"))
		Expect(globErr).NotTo(HaveOccurred())
		Expect(matches).To(BeEmpty())
	})

	It("preserves session metadata and updates activity summary when retry count reaches 3 with live analysis enabled", func() {
		w.ActivityCountingEnabled = true
		warmupSessionID := "WARMUP-20261124-" + ulid.Make().String()
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:      warmupSessionID,
			ProfileID:      profile.ID,
			Status:         db.SessionStatusStarted,
			WorkoutType:    WorkoutTypeWarmup,
			WODDescription: "Mobility warmup",
			MovementHints:  db.JSONDocument(`["Air Squat"]`),
		})

		raw, err := json.Marshal(VideoAnalysisWithSessionPayload{
			LiveAnalysisVersion: 1,
			SessionID:           warmupSessionID,
			FilePath:            "gs://test-bucket/chunks/warmup-retry/chunk_001.mp4",
			ProfileID:           profile.ID,
			StartSecs:           0,
			EndSecs:             10,
			HeartRateBPM:        115,
			WorkoutConfidence:   0.5,
		})
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleChunkAnalysisWithSessionTask(WithRetryCount(context.Background(), 3), asynq.NewTask(TypeChunkAnalysisWithSession, raw))
		Expect(err).To(Equal(asynq.SkipRetry))

		var result db.ChunkAnalysisResult
		Expect(dbConn.Where("session_id = ? AND profile_id = ?", warmupSessionID, profile.ID).First(&result).Error).To(Succeed())
		Expect(result.Status).To(Equal("FAILED"))
		Expect(result.MovementObservations).NotTo(BeEmpty())

		var reloaded db.Session
		Expect(dbConn.Where("session_id = ? AND profile_id = ?", warmupSessionID, profile.ID).First(&reloaded).Error).To(Succeed())
		Expect(reloaded.WorkoutType).To(Equal(WorkoutTypeWarmup))
		Expect(reloaded.WODDescription).To(Equal("Mobility warmup"))
		Expect(strings.TrimSpace(string(reloaded.ActivitySummary))).NotTo(BeEmpty())
		Expect(strings.TrimSpace(string(reloaded.ActivitySummary))).NotTo(Equal("null"))
	})
})

var _ = Describe("buildChunkAnalysisWithSessionPrompt", func() {
	It("formats profile attributes and fitness level from the loaded profile without querying DB", func() {
		w := &Worker{DB: nil, logger: zap.NewNop()}
		birthYear, birthMonth, birthDay := 1995, 3, 15
		heightCm := 168
		weightKg := 58.5
		gender := "female"
		injuries := "Left Shoulder"

		prompt := w.buildChunkAnalysisWithSessionPrompt(
			VideoAnalysisWithSessionPayload{},
			&db.Profile{
				BirthYear:    &birthYear,
				BirthMonth:   &birthMonth,
				BirthDay:     &birthDay,
				Gender:       &gender,
				HeightCm:     &heightCm,
				WeightKg:     &weightKg,
				FitnessLevel: "advanced",
				Injuries:     &injuries,
			},
			&db.Session{
				WorkoutType:    WorkoutTypeWOD,
				WODDescription: "Fran",
				MovementHints:  db.JSONDocument(`["Thruster","Pull-up"]`),
			},
		)

		Expect(prompt).To(ContainSubstring(LevelPolicyAdvanced))
		Expect(prompt).To(ContainSubstring("생년월일: 1995년 3월 15일, 성별: 여, 키: 168cm, 몸무게: 58.5kg, 피트니스 레벨: 고급"))
		Expect(prompt).To(ContainSubstring("Left Shoulder"))
		Expect(prompt).To(ContainSubstring("Fran"))
		Expect(prompt).To(ContainSubstring("Thruster, Pull-up"))
	})

	It("handles a nil profile without panicking", func() {
		w := &Worker{DB: nil, logger: zap.NewNop()}
		var prompt string
		Expect(func() {
			prompt = w.buildChunkAnalysisWithSessionPrompt(VideoAnalysisWithSessionPayload{}, nil, &db.Session{WorkoutType: WorkoutTypeWOD})
		}).NotTo(Panic())
		Expect(prompt).NotTo(ContainSubstring("## 개인 프로필"))
		Expect(prompt).NotTo(ContainSubstring("## 알려진 부상 사항"))
	})
})
