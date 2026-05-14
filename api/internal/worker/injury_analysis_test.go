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

var _ = Describe("buildInjuryAnalysisPrompt", func() {
	var w *Worker

	BeforeEach(func() {
		w = &Worker{logger: zap.NewNop()}
	})

	It("includes focus timestamps when provided", func() {
		prompt := w.buildInjuryAnalysisPrompt(InjuryAnalysisPayload{
			Injuries:        []string{"Knee"},
			FocusTimestamps: `[{"start":"0:32","end":"0:45","reason":"무릎 내전"}]`,
		})

		Expect(prompt).To(ContainSubstring("부상 부위 집중 분석"))
		Expect(prompt).To(ContainSubstring("집중 분석 구간 (Focus Timestamps)"))
		Expect(prompt).To(ContainSubstring("0:32"))
		Expect(prompt).To(ContainSubstring("Knee"))
	})

	It("uses fallback text when timestamps are empty", func() {
		prompt := w.buildInjuryAnalysisPrompt(InjuryAnalysisPayload{
			Injuries:        []string{"Shoulder"},
			FocusTimestamps: "",
		})

		Expect(prompt).To(ContainSubstring("타임스탬프가 제공되지 않았습니다"))
		Expect(prompt).To(ContainSubstring("Shoulder"))
	})
})

var _ = Describe("NewInjuryAnalysisTask", func() {

	It("creates a task with the correct payload", func() {
		timestamps := `[{"start":"1:00","end":"1:15","reason":"test"}]`
		task, err := NewInjuryAnalysisTask(
			"session-1",
			"gs://bucket/video.mp4",
			[]string{"Knee", "Shoulder"},
			5,
			timestamps,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(task.Type()).To(Equal(TypeInjuryAnalysis))

		var payload InjuryAnalysisPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.FilePath).To(Equal("gs://bucket/video.mp4"))
		Expect(payload.Injuries).To(Equal([]string{"Knee", "Shoulder"}))
		Expect(payload.ProfileID).To(Equal(uint(5)))
		Expect(payload.FocusTimestamps).To(Equal(timestamps))
	})
})

var _ = Describe("HandleInjuryAnalysisTask", func() {
	const (
		geminiBaseURL  = "https://generativelanguage.googleapis.com"
		geminiAPIKey   = "test-api-key"
		injuryAnalysis = "무릎 내전이 관찰됩니다. 스쿼트 깊이를 줄이고 발 간격을 조정하세요."
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
					"name": "files/mock-injury",
					"uri":  geminiBaseURL + "/files/mock-injury",
				},
			})

		transport.New(geminiBaseURL).
			Get("/v1beta/files/mock-injury").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/mock-injury", "state": "ACTIVE"})

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
			Delete("/v1beta/files/mock-injury").
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

	It("appends injury output to existing WOD analysis row", func() {
		// Pre-create a WOD analysis row for the session
		wodResult := &db.AnalysisResult{
			SessionID:    "sess-injury-001",
			AnalysisType: db.AnalysisTypeWOD,
			Status:       "COMPLETED",
			Output:       "WOD analysis output",
		}
		Expect(dbConn.Create(wodResult).Error).NotTo(HaveOccurred())

		timestamps := `[{"start":"0:32","end":"0:45","reason":"무릎 내전 관찰"}]`
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-injury-001/full.mp4")
		transport := setupGeminiTransport(injuryAnalysis)

		task, err := NewInjuryAnalysisTask(
			"sess-injury-001",
			"gs://test-bucket/videos/sess-injury-001/full.mp4",
			[]string{"Knee"},
			0,
			timestamps,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(w.HandleInjuryAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		Expect(transport.Requests()).To(HaveLen(5))

		// Verify injury output is on the same WOD row
		var result db.AnalysisResult
		Expect(dbConn.First(&result, wodResult.ID).Error).NotTo(HaveOccurred())
		Expect(result.AnalysisType).To(Equal(db.AnalysisTypeWOD))
		Expect(result.Output).To(Equal("WOD analysis output"))
		Expect(result.InjuryOutput).To(Equal(injuryAnalysis))

		// No separate injury_supplement row should exist
		var count int64
		dbConn.Model(&db.AnalysisResult{}).Where("session_id = ? AND analysis_type = ?", "sess-injury-001", db.AnalysisTypeInjurySupplement).Count(&count)
		Expect(count).To(BeZero())
	})

	It("falls back to standalone row with ProfileID when no WOD row exists", func() {
		user := db.User{
			Username:     "test-username",
			PasswordHash: "test-password-hash",
		}
		Expect(dbConn.Create(&user).Error).NotTo(HaveOccurred())

		profile := db.Profile{
			UserID:    user.ID,
			BirthYear: ptr(1992), BirthMonth: ptr(9), BirthDay: ptr(20),
			Gender: ptr("male"), HeightCm: ptr(180), WeightKg: ptr(82.0),
		}
		Expect(dbConn.Create(&profile).Error).NotTo(HaveOccurred())

		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-injury-profile-001/full.mp4")
		transport := setupGeminiTransport(injuryAnalysis)

		task, err := NewInjuryAnalysisTask(
			"sess-injury-profile-001",
			"gs://test-bucket/videos/sess-injury-profile-001/full.mp4",
			[]string{"Shoulder"},
			profile.ID,
			"",
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(w.HandleInjuryAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		// Falls back to creating a standalone injury_supplement row
		var result db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", "sess-injury-profile-001").First(&result).Error).
			NotTo(HaveOccurred())
		Expect(result.AnalysisType).To(Equal(db.AnalysisTypeInjurySupplement))
		Expect(result.ProfileID).To(Equal(profile.ID))
		Expect(result.Output).To(Equal(injuryAnalysis))
	})

	It("returns an error and saves no record when Gemini returns empty candidates", func() {
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-injury-empty-001/full.mp4")

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
					"name": "files/mock-injury",
					"uri":  geminiBaseURL + "/files/mock-injury",
				},
			})

		transport.New(geminiBaseURL).
			Get("/v1beta/files/mock-injury").
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/mock-injury", "state": "ACTIVE"})

		transport.New(geminiBaseURL).
			Post("/v1beta/models/gemini-3.1-pro-preview:generateContent").
			Reply(http.StatusOK).
			JSON(map[string]any{"candidates": []map[string]any{}})

		// geminiFile IS set so defer delete runs
		transport.New(geminiBaseURL).
			Delete("/v1beta/files/mock-injury").
			Reply(http.StatusOK).
			JSON(map[string]any{})

		task, err := NewInjuryAnalysisTask(
			"sess-injury-empty-001",
			"gs://test-bucket/videos/sess-injury-empty-001/full.mp4",
			[]string{"Knee"},
			0, "",
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleInjuryAnalysisTask(context.Background(), task)
		Expect(err).To(HaveOccurred())
		Expect(transport.Verify()).To(Succeed())

		var count int64
		dbConn.Model(&db.AnalysisResult{}).Where("session_id = ?", "sess-injury-empty-001").Count(&count)
		Expect(count).To(BeZero())
	})

	It("returns SkipRetry immediately when file path is not a GCS URI", func() {
		task, err := NewInjuryAnalysisTask(
			"sess-injury-baduri-001",
			"/local/path/video.mp4",
			[]string{"Knee"},
			0, "",
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleInjuryAnalysisTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("invalid file path")))
	})
})
