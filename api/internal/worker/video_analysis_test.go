package worker

import (
	"context"
	"encoding/json"
	"net/http"
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

// ---------------------------------------------------------------------------
// Unit tests — no DB required
// ---------------------------------------------------------------------------



var _ = Describe("buildAnalysisPrompt", func() {
	var w *Worker

	BeforeEach(func() {
		w = &Worker{logger: zap.NewNop()}
	})

	It("uses the WOD prompt and keeps injury context when the workout type is wod", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Burpee"},
			Injuries:    []string{"Shoulder"},
		})

		Expect(prompt).To(ContainSubstring("# 운동 영상 분석 요청"))
		Expect(prompt).To(ContainSubstring("## 운동 종목: Burpee"))
		Expect(prompt).To(ContainSubstring("## 알려진 부상 사항: Shoulder"))
	})

	It("includes injury timestamp section when injuries are present", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Back Squat"},
			Injuries:    []string{"Lower Back"},
		})

		Expect(prompt).To(ContainSubstring("부상 관련 타임스탬프"))
		Expect(prompt).To(ContainSubstring("부상 부위: Lower Back"))
		Expect(prompt).To(ContainSubstring("json"))
	})

	It("does NOT include injury timestamp section when no injuries", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Burpee"},
		})

		Expect(prompt).NotTo(ContainSubstring("부상 관련 타임스탬프"))
	})

	It("uses the default profile when ProfileID is 0", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			ProfileID:   0,
		})

		Expect(prompt).To(ContainSubstring("1984년 10월 17일"))
	})

	It("normalizes any workout type to wod", func() {
		Expect(NormalizeWorkoutType("rehab")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("REHAB")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("wod")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("anything")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("")).To(Equal(WorkoutTypeWOD))
	})
})

var _ = Describe("parseInjuryTimestamps", func() {
	It("extracts valid JSON from fenced injury_timestamps code block", func() {
		input := "Some analysis text...\n```injury_timestamps\n[{\"start\":\"0:32\",\"end\":\"0:45\",\"reason\":\"무릎 내전\"}]\n```\nMore text."
		result := parseInjuryTimestamps(input)
		Expect(result).To(ContainSubstring("0:32"))
	})

	It("returns empty for no JSON block", func() {
		result := parseInjuryTimestamps("No JSON here")
		Expect(result).To(BeEmpty())
	})

	It("returns empty for invalid JSON in block", func() {
		input := "```injury_timestamps\nnot valid json\n```"
		result := parseInjuryTimestamps(input)
		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("NewVideoAnalysisTask", func() {
	It("normalizes workout type to wod and keeps injuries in the payload", func() {
		task, err := NewVideoAnalysisTask(
			"session-1",
			"gs://bucket/video.mp4",
			"REHAB",
			[]string{"Burpee"},
			[]string{"Knee"},
			42,
		)
		Expect(err).NotTo(HaveOccurred())

		var payload VideoAnalysisPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.WorkoutType).To(Equal(WorkoutTypeWOD))
		Expect(payload.Movements).To(Equal([]string{"Burpee"}))
		Expect(payload.Injuries).To(Equal([]string{"Knee"}))
		Expect(payload.ProfileID).To(Equal(uint(42)))
	})
})

const analysisWithHighlights = `
## 분석 결과

훌륭한 운동이었습니다. 코어 안정성이 매우 뛰어났습니다.

` + "```highlights" + `
[{"start":"0:05","end":"0:15","type":"best_form","reason":"완벽한 자세 유지"},{"start":"0:30","end":"0:45","type":"key_moment","reason":"최고 페이스 구간"}]
` + "```" + `

전반적으로 훌륭했습니다.`

var _ = Describe("HandleVideoAnalysisTask", func() {
	const (
		geminiBaseURL = "https://generativelanguage.googleapis.com"
		geminiAPIKey  = "test-api-key"
	)

	var (
		dbConn    *gorm.DB
		storage   *fakeStorage
		queueClient *asynq.Client
		inspector   *asynq.Inspector
		w          *Worker
	)

	// setupGeminiTransport creates a real gemini.Client backed by MockTransport
	// and wires it into w. It registers the standard 5-request chain:
	//   upload-start → upload-finalize → poll(ACTIVE) → generateContent → deleteFile
	// Returns the transport so each test can call Verify() and inspect Requests().
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
					"name": "files/mock-file",
					"uri":  geminiBaseURL + "/files/mock-file",
				},
			})

		transport.New(geminiBaseURL).
			Get("/v1beta/files/mock-file").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/mock-file", "state": "ACTIVE"})

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
			Delete("/v1beta/files/mock-file").
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

		storage = &fakeStorage{}
		queueClient = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		testhelpers.CleanupQueue(inspector)

		w = &Worker{
			DB:            dbConn,
			StorageClient: storage,
			GeminiClient:  nil, // set per-test via setupGeminiTransport
			QueueClient:   queueClient,
			BucketName:    "test-bucket",
			logger:        zap.NewNop(),
		}
	})

	It("persists a COMPLETED AnalysisResult with parsed highlight segments", func() {
		transport := setupGeminiTransport(analysisWithHighlights)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID:   "sess-happy-001",
			FilePath:    "gs://test-bucket/videos/sess-happy-001/chunk_001.mp4",
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Burpee", "Pull-up"},
		})
		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())

		Expect(transport.Verify()).To(Succeed())
		Expect(transport.Requests()).To(HaveLen(5))

		var result db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", "sess-happy-001").First(&result).Error).
			NotTo(HaveOccurred())

		Expect(result.Status).To(Equal("COMPLETED"))
		Expect(result.AnalysisType).To(Equal(db.AnalysisTypeWOD))
		Expect(result.Output).To(ContainSubstring("훌륭한 운동이었습니다"))

		var segs []HighlightSegment
		Expect(json.Unmarshal([]byte(result.HighlightSegments), &segs)).To(Succeed())
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].Type).To(Equal("best_form"))
		Expect(segs[1].Type).To(Equal("key_moment"))
	})

	It("sets ProfileID on the result when ProfileID > 0", func() {
		profile := db.Profile{
			BirthYear: 1990, BirthMonth: 6, BirthDay: 15,
			Gender: "male", HeightCm: 175, WeightKg: 75.0,
		}
		Expect(dbConn.Create(&profile).Error).NotTo(HaveOccurred())

		transport := setupGeminiTransport(analysisWithHighlights)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "sess-profile-001",
			FilePath:  "gs://test-bucket/videos/sess-profile-001/chunk_001.mp4",
			ProfileID: profile.ID,
		})

		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		var result db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", "sess-profile-001").First(&result).Error).
			NotTo(HaveOccurred())
		Expect(result.ProfileID).NotTo(BeNil())
		Expect(*result.ProfileID).To(Equal(profile.ID))
	})

	It("enqueues an injury:analysis follow-up task with parsed focus timestamps", func() {
		analysisWithInjury := analysisWithHighlights + "\n```injury_timestamps\n" +
			`[{"start":"0:32","end":"0:45","reason":"무릎 내전 관찰"}]` + "\n```"

		transport := setupGeminiTransport(analysisWithInjury)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "sess-injury-001",
			FilePath:  "gs://test-bucket/videos/sess-injury-001/chunk_001.mp4",
			Injuries:  []string{"Knee"},
		})

		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		// Give asynq a moment to flush to Redis, then inspect.
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal(TypeInjuryAnalysis))

		var injPayload InjuryAnalysisPayload
		Expect(json.Unmarshal(pending[0].Payload, &injPayload)).To(Succeed())
		Expect(injPayload.SessionID).To(Equal("sess-injury-001"))
		Expect(injPayload.Injuries).To(Equal([]string{"Knee"}))
		Expect(injPayload.FocusTimestamps).To(ContainSubstring("0:32"))
	})

	It("does NOT enqueue an injury task when there are no injuries", func() {
		transport := setupGeminiTransport(analysisWithHighlights)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "sess-no-injury-001",
			FilePath:  "gs://test-bucket/videos/sess-no-injury-001/chunk_001.mp4",
		})

		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		// No tasks should be enqueued for injury follow-up.
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})

	It("returns an error and saves no record when Gemini returns empty analysis", func() {
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

		// Upload succeeds...
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
					"name": "files/mock-file",
					"uri":  geminiBaseURL + "/files/mock-file",
				},
			})

		transport.New(geminiBaseURL).
			Get("/v1beta/files/mock-file").
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "files/mock-file", "state": "ACTIVE"})

		// ...but generateContent returns no candidates
		transport.New(geminiBaseURL).
			Post("/v1beta/models/gemini-3.1-pro-preview:generateContent").
			Reply(http.StatusOK).
			JSON(map[string]any{"candidates": []map[string]any{}})

		// Since geminiFile IS set, the defer deleteFile runs.
		transport.New(geminiBaseURL).
			Delete("/v1beta/files/mock-file").
			Reply(http.StatusOK).
			JSON(map[string]any{})

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "sess-empty-001",
			FilePath:  "gs://test-bucket/videos/sess-empty-001/chunk.mp4",
		})

		err = w.HandleVideoAnalysisTask(context.Background(), task)
		Expect(err).To(HaveOccurred())
		Expect(transport.Verify()).To(Succeed())

		var count int64
		dbConn.Model(&db.AnalysisResult{}).Where("session_id = ?", "sess-empty-001").Count(&count)
		Expect(count).To(BeZero())
	})

	It("skips retry and returns SkipRetry when file path is not a GCS URI", func() {
		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "sess-baduri-001",
			FilePath:  "/local/path/video.mp4",
		})

		err := w.HandleVideoAnalysisTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("invalid file path")))
	})
})
