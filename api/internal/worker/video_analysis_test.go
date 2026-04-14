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
		}, 0)

		Expect(prompt).To(ContainSubstring("# 운동 영상 분석 요청"))
		Expect(prompt).To(ContainSubstring("## 운동 종목: Burpee"))
		Expect(prompt).To(ContainSubstring("## 알려진 부상 사항: Shoulder"))
	})

	It("includes injury timestamp section when injuries are present", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Back Squat"},
			Injuries:    []string{"Lower Back"},
		}, 0)

		Expect(prompt).To(ContainSubstring("부상 관련 타임스탬프"))
		Expect(prompt).To(ContainSubstring("부상 부위: Lower Back"))
		Expect(prompt).To(ContainSubstring("json"))
	})

	It("does NOT include injury timestamp section when no injuries", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Burpee"},
		}, 0)

		Expect(prompt).NotTo(ContainSubstring("부상 관련 타임스탬프"))
	})

	It("uses the default profile when ProfileID is 0", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			ProfileID:   0,
		}, 0)

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

	It("merges injury timestamps from multiple segments", func() {
		input := "## Seg 1\n" +
			"```injury_timestamps\n" +
			`[{"start": "0:45", "end": "0:50", "reason": "발목 부상"}]` +
			"\n```\n" +
			"## Seg 2\n" +
			"```injury_timestamps\n" +
			`[{"start": "04:54.000", "end": "04:58.000", "reason": "우측 발목 충격"}]` +
			"\n```"
		result := parseInjuryTimestamps(input)
		Expect(result).To(ContainSubstring("0:45"))
		Expect(result).To(ContainSubstring("04:54.000"))

		var parsed []json.RawMessage
		Expect(json.Unmarshal([]byte(result), &parsed)).To(Succeed())
		Expect(parsed).To(HaveLen(2))
	})

	It("handles XML-style <injury_timestamps> tags", func() {
		input := "<injury_timestamps>\n" +
			`[{"start": "12:55", "end": "12:57", "reason": "착지 충격"}]` +
			"\n</injury_timestamps>"
		result := parseInjuryTimestamps(input)
		Expect(result).To(ContainSubstring("12:55"))
	})
})

var _ = Describe("ParseHighlightSegments", func() {
	It("extracts highlights from a single backtick code block", func() {
		input := "Some text\n" +
			"```highlights\n" +
			`[{"start":"0:05","end":"0:15","type":"best_form","reason":"완벽한 자세"}]` +
			"\n```\nMore text."
		result := ParseHighlightSegments(input)
		var segs []HighlightSegment
		Expect(json.Unmarshal([]byte(result), &segs)).To(Succeed())
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Type).To(Equal("best_form"))
	})

	It("returns empty for no highlights block", func() {
		result := ParseHighlightSegments("No highlights here")
		Expect(result).To(BeEmpty())
	})

	It("returns empty for invalid JSON in block", func() {
		input := "```highlights\nnot valid json\n```"
		result := ParseHighlightSegments(input)
		Expect(result).To(BeEmpty())
	})

	It("merges highlights from multiple backtick blocks across segments", func() {
		input := "## Seg 1\n" +
			"```highlights\n" +
			`[{"start":"0:45","end":"0:47","type":"best_form","movement":"Triceps Extension","reason":"좋은 자세"}]` +
			"\n```\n" +
			"## Seg 2\n" +
			"```highlights\n" +
			`[{"start":"04:52.000","end":"04:54.000","type":"best_form","movement":"Box Step-up","reason":"안정적 템포"},{"start":"04:54.000","end":"04:58.000","type":"key_moment","movement":"Box Step-up","reason":"우측 다리 핵심 구간"}]` +
			"\n```"
		result := ParseHighlightSegments(input)
		var segs []HighlightSegment
		Expect(json.Unmarshal([]byte(result), &segs)).To(Succeed())
		Expect(segs).To(HaveLen(3))
		Expect(segs[0].Movement).To(Equal("Triceps Extension"))
		Expect(segs[1].Movement).To(Equal("Box Step-up"))
		Expect(segs[2].Movement).To(Equal("Box Step-up"))
	})

	It("handles XML-style <highlights> tags", func() {
		input := "<highlights>\n" +
			`[{"start":"12:46","end":"12:51","type":"best_form","movement":"Toes to Bar","reason":"키핑 리듬"}]` +
			"\n</highlights>"
		result := ParseHighlightSegments(input)
		var segs []HighlightSegment
		Expect(json.Unmarshal([]byte(result), &segs)).To(Succeed())
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Movement).To(Equal("Toes to Bar"))
	})

	It("merges highlights from mixed backtick and XML-style blocks", func() {
		input := "## Seg 1\n" +
			"```highlights\n" +
			`[{"start":"0:45","end":"0:47","type":"best_form","movement":"Snatch","reason":"좋은 풀"}]` +
			"\n```\n" +
			"## Seg 2\n" +
			"<highlights>\n" +
			`[{"start":"12:46","end":"12:51","type":"key_moment","movement":"Toes to Bar","reason":"연속 수행"}]` +
			"\n</highlights>"
		result := ParseHighlightSegments(input)
		var segs []HighlightSegment
		Expect(json.Unmarshal([]byte(result), &segs)).To(Succeed())
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].Movement).To(Equal("Snatch"))
		Expect(segs[1].Movement).To(Equal("Toes to Bar"))
	})

	It("parses the full 13-segment real-world Gemini output", func() {
		// This test uses the exact output format from a real 13-segment workout analysis.
		// Segments use mostly ```highlights blocks but segment 6 uses <highlights> XML tags.
		input := realWorldMultiSegmentOutput
		result := ParseHighlightSegments(input)
		Expect(result).NotTo(BeEmpty())

		var segs []HighlightSegment
		Expect(json.Unmarshal([]byte(result), &segs)).To(Succeed())

		// Count expected highlights per segment from the real output:
		// Seg 1: 4, Seg 2: 3, Seg 3: 4, Seg 4: 4, Seg 5: 5, Seg 6: 4 (XML)
		// Seg 7: 4, Seg 8: 4, Seg 9: 5, Seg 10: 4, Seg 11: 4, Seg 12: 4, Seg 13: 4
		Expect(segs).To(HaveLen(53))

		// Verify highlights from different segments are represented
		movements := map[string]bool{}
		for _, seg := range segs {
			movements[seg.Movement] = true
		}
		Expect(movements).To(HaveKey("Overhead Triceps Extension"))
		Expect(movements).To(HaveKey("Box Step-up"))
		Expect(movements).To(HaveKey("Dumbbell Snatch"))
		Expect(movements).To(HaveKey("Toes-to-bar (Knee Raise)"))
		Expect(movements).To(HaveKey("Toes to Bar"))
		Expect(movements).To(HaveKey("Pull-up"))
		Expect(movements).To(HaveKey("Box Jump"))
		Expect(movements).To(HaveKey("Burpee"))

		// Verify the XML-tagged segment 6 (Toes to Bar) is included
		var toesToBarCount int
		for _, seg := range segs {
			if seg.Movement == "Toes to Bar" {
				toesToBarCount++
			}
		}
		Expect(toesToBarCount).To(Equal(4)) // segment 6 has 4 highlights
	})

	It("also merges injury timestamps from the same real-world output", func() {
		input := realWorldMultiSegmentOutput
		result := parseInjuryTimestamps(input)
		Expect(result).NotTo(BeEmpty())

		var timestamps []json.RawMessage
		Expect(json.Unmarshal([]byte(result), &timestamps)).To(Succeed())
		// Many segments have injury timestamps; verify we got more than just the first one
		Expect(len(timestamps)).To(BeNumerically(">=", 10))
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
		dbConn           *gorm.DB
		storageTransport *testhelpers.MockTransport
		queueClient      *asynq.Client
		inspector        *asynq.Inspector
		w                *Worker
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

		storageTransport = testhelpers.NewMockTransport()
		storageClient, sErr := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(sErr).NotTo(HaveOccurred())

		queueClient = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		testhelpers.CleanupQueue(inspector)

		w = &Worker{
			DB:            dbConn,
			StorageClient: storageClient,
			GeminiClient:  nil, // set per-test via setupGeminiTransport
			QueueClient:   queueClient,
			BucketName:    "test-bucket",
			logger:        zap.NewNop(),
		}
	})

	It("persists a COMPLETED AnalysisResult with parsed highlight segments", func() {
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-happy-001/chunk_001.mp4")
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

		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-profile-001/chunk_001.mp4")
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

		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-injury-001/chunk_001.mp4")
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
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-no-injury-001/chunk_001.mp4")
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
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-empty-001/chunk.mp4")

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

var _ = Describe("HandleVideoAnalysisTask (UseCache / TwoPass)", func() {
	const (
		geminiBaseURL = "https://generativelanguage.googleapis.com"
		geminiAPIKey  = "test-api-key"
	)

	var (
		dbConn           *gorm.DB
		storageTransport *testhelpers.MockTransport
		queueClient      *asynq.Client
		inspector        *asynq.Inspector
		w                *Worker
	)

	// setupTwoPassTransport: upload → poll → analyzeSegment x2 (Pro) → deleteFile
	// No IndexVideo mock — chunks in DB provide the segment index.
	// Since seeded chunks have different exercise types (Snatch + Pull-up),
	// mergeSegmentsByMovement produces 2 segments → 2 generateContent calls.
	setupTwoPassTransport := func(segmentAnalysis string, hasInjuries bool) *testhelpers.MockTransport {
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
					"name": "files/mock-two-pass",
					"uri":  geminiBaseURL + "/files/mock-two-pass",
				},
			})

		transport.New(geminiBaseURL).
			Get("/v1beta/files/mock-two-pass").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"name":  "files/mock-two-pass",
				"state": "ACTIVE",
			})

		// Segment 1 (Snatch) analysis
		transport.New(geminiBaseURL).
			Post("/v1beta/models/gemini-3.1-pro-preview:generateContent").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{{"text": segmentAnalysis}},
					},
				}},
			})

		// Segment 2 (Pull-up) analysis
		transport.New(geminiBaseURL).
			Post("/v1beta/models/gemini-3.1-pro-preview:generateContent").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{{"text": segmentAnalysis}},
					},
				}},
			})

		if !hasInjuries {
			transport.New(geminiBaseURL).
				Delete("/v1beta/files/mock-two-pass").
				MatchHeader("X-Goog-Api-Key", geminiAPIKey).
				Reply(http.StatusOK).
				JSON(map[string]any{})
		}

		return transport
	}

	seedChunks := func(sessionID string) {
		start0 := 0.0
		end0 := 45.0
		start1 := 50.0
		end1 := 90.0
		Expect(dbConn.Create(&db.ChunkAnalysisResult{
			SessionID:    sessionID,
			FilePath:     "gs://test-bucket/videos/" + sessionID + "/chunk_0.mp4",
			Status:       "COMPLETED",
			ExerciseType: "Snatch",
			Output:       "좋은 스내치 자세입니다",
			StartSecs:    &start0,
			EndSecs:      &end0,
		}).Error).NotTo(HaveOccurred())
		Expect(dbConn.Create(&db.ChunkAnalysisResult{
			SessionID:    sessionID,
			FilePath:     "gs://test-bucket/videos/" + sessionID + "/chunk_1.mp4",
			Status:       "COMPLETED",
			ExerciseType: "Pull-up",
			Output:       "풀업 킵핑 동작이 안정적입니다",
			StartSecs:    &start1,
			EndSecs:      &end1,
		}).Error).NotTo(HaveOccurred())
	}

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		queueClient = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		testhelpers.CleanupQueue(inspector)

		storageTransport = testhelpers.NewMockTransport()
		storageClient, sErr := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(sErr).NotTo(HaveOccurred())

		w = &Worker{
			DB:            dbConn,
			StorageClient: storageClient,
			GeminiClient:  nil,
			QueueClient:   queueClient,
			BucketName:    "test-bucket",
			UseCache:      true,
			logger:        zap.NewNop(),
		}
	})

	It("persists COMPLETED result using chunk-based segments (no injuries)", func() {
		seedChunks("sess-twopass-001")
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-twopass-001/merged.mp4")
		transport := setupTwoPassTransport(analysisWithHighlights, false)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID:   "sess-twopass-001",
			FilePath:    "gs://test-bucket/videos/sess-twopass-001/merged.mp4",
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Snatch"},
		})
		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())

		Expect(transport.Verify()).To(Succeed())
		// 6 requests: upload + finalize + poll + analyzeSegment x2 + deleteFile
		Expect(transport.Requests()).To(HaveLen(6))

		var result db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", "sess-twopass-001").First(&result).Error).
			NotTo(HaveOccurred())
		Expect(result.Status).To(Equal("COMPLETED"))
		Expect(result.Output).To(ContainSubstring("훌륭한 운동이었습니다"))

		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})

	It("enqueues injury task with file URI and does NOT delete file", func() {
		seedChunks("sess-twopass-inj-001")
		analysisWithInjury := analysisWithHighlights + "\n```injury_timestamps\n" +
			`[{"start":"0:32","end":"0:45","reason":"무릎 내전 관찰"}]` + "\n```"

		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-twopass-inj-001/merged.mp4")
		transport := setupTwoPassTransport(analysisWithInjury, true)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "sess-twopass-inj-001",
			FilePath:  "gs://test-bucket/videos/sess-twopass-inj-001/merged.mp4",
			Injuries:  []string{"Knee"},
		})

		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		// 5 requests: upload + finalize + poll + analyzeSegment x2 (no deleteFile)
		Expect(transport.Requests()).To(HaveLen(5))

		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal(TypeInjuryAnalysis))

		var injPayload InjuryAnalysisPayload
		Expect(json.Unmarshal(pending[0].Payload, &injPayload)).To(Succeed())
		Expect(injPayload.GeminiFileURI).To(ContainSubstring("/files/mock-two-pass"))
		Expect(injPayload.GeminiFileName).To(Equal("files/mock-two-pass"))
		Expect(injPayload.FocusTimestamps).To(ContainSubstring("0:32"))
	})
})

var _ = Describe("parseSegments", func() {
	It("extracts segments from JSON in code fence", func() {
		text := "Some text\n```json\n[{\"start\":\"0:30\",\"end\":\"1:00\",\"type\":\"Snatch\",\"description\":\"desc\"}]\n```\nMore text"
		segments := parseSegments(text)
		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Type).To(Equal("Snatch"))
		Expect(segments[0].Start).To(Equal("0:30"))
	})

	It("returns empty for unparseable text", func() {
		segments := parseSegments("no json here")
		Expect(segments).To(BeEmpty())
	})
})

var _ = Describe("convertToSeconds", func() {
	It("parses MM:SS format", func() {
		Expect(convertToSeconds("1:30")).To(Equal(90 * time.Second))
		Expect(convertToSeconds("0:45")).To(Equal(45 * time.Second))
		Expect(convertToSeconds("10:00")).To(Equal(600 * time.Second))
	})

	It("handles plain seconds", func() {
		Expect(convertToSeconds("30s")).To(Equal(30 * time.Second))
		Expect(convertToSeconds("60")).To(Equal(60 * time.Second))
	})

	It("returns 0 for unparseable input", func() {
		Expect(convertToSeconds("invalid")).To(Equal(time.Duration(0)))
	})
})

// ---------------------------------------------------------------------------
// parseChunkExercise & stripExerciseTag
// ---------------------------------------------------------------------------

var _ = Describe("parseChunkExercise", func() {
	It("extracts exercise name from [EXERCISE: ...] tag", func() {
		Expect(parseChunkExercise("[EXERCISE: Snatch]\nGreat form")).To(Equal("Snatch"))
		Expect(parseChunkExercise("[EXERCISE: Back Squat]\nKeep it up")).To(Equal("Back Squat"))
		Expect(parseChunkExercise("[EXERCISE: Pull-up]\nNice kipping")).To(Equal("Pull-up"))
	})

	It("returns empty string for [NO_EXERCISE]", func() {
		Expect(parseChunkExercise("[NO_EXERCISE]")).To(BeEmpty())
		Expect(parseChunkExercise("[NO_EXERCISE]\n")).To(BeEmpty())
	})

	It("returns empty string when no tag found", func() {
		Expect(parseChunkExercise("Just some text")).To(BeEmpty())
	})

	It("is case-insensitive", func() {
		Expect(parseChunkExercise("[exercise: deadlift]\nFeedback")).To(Equal("deadlift"))
	})
})

var _ = Describe("stripExerciseTag", func() {
	It("removes [EXERCISE: ...] tag and trims", func() {
		result := stripExerciseTag("[EXERCISE: Snatch]\nGreat form on the pull")
		Expect(result).To(Equal("Great form on the pull"))
	})

	It("removes [NO_EXERCISE] tag", func() {
		result := stripExerciseTag("[NO_EXERCISE]")
		Expect(result).To(BeEmpty())
	})

	It("returns original text when no tag present", func() {
		result := stripExerciseTag("Just feedback text")
		Expect(result).To(Equal("Just feedback text"))
	})
})

// ---------------------------------------------------------------------------
// mergeSegmentsByMovement
// ---------------------------------------------------------------------------

var _ = Describe("mergeSegmentsByMovement", func() {
	It("merges consecutive segments with the same exercise type", func() {
		segments := []Segment{
			{Start: "0:00", End: "0:10", Type: "Snatch", Description: "rep 1"},
			{Start: "0:10", End: "0:20", Type: "Snatch", Description: "rep 2"},
			{Start: "0:20", End: "0:30", Type: "Snatch", Description: "rep 3"},
			{Start: "0:30", End: "0:40", Type: "Burpee", Description: "fast"},
			{Start: "0:40", End: "0:50", Type: "Burpee", Description: "slower"},
		}

		merged := mergeSegmentsByMovement(segments)
		Expect(merged).To(HaveLen(2))
		Expect(merged[0].Type).To(Equal("Snatch"))
		Expect(merged[0].Start).To(Equal("0:00"))
		Expect(merged[0].End).To(Equal("0:30"))
		Expect(merged[1].Type).To(Equal("Burpee"))
		Expect(merged[1].Start).To(Equal("0:30"))
		Expect(merged[1].End).To(Equal("0:50"))
	})

	It("handles alternating movement types", func() {
		segments := []Segment{
			{Start: "0:00", End: "0:10", Type: "Snatch", Description: "s1"},
			{Start: "0:10", End: "0:20", Type: "Burpee", Description: "b1"},
			{Start: "0:20", End: "0:30", Type: "Snatch", Description: "s2"},
		}

		merged := mergeSegmentsByMovement(segments)
		Expect(merged).To(HaveLen(3))
	})

	It("merges case-insensitively", func() {
		segments := []Segment{
			{Start: "0:00", End: "0:10", Type: "snatch", Description: "lower"},
			{Start: "0:10", End: "0:20", Type: "Snatch", Description: "upper"},
		}

		merged := mergeSegmentsByMovement(segments)
		Expect(merged).To(HaveLen(1))
		Expect(merged[0].End).To(Equal("0:20"))
	})

	It("returns empty for empty input", func() {
		Expect(mergeSegmentsByMovement(nil)).To(BeNil())
	})

	It("returns single segment unchanged", func() {
		segments := []Segment{{Start: "0:00", End: "0:10", Type: "Snatch"}}
		merged := mergeSegmentsByMovement(segments)
		Expect(merged).To(HaveLen(1))
	})
})

// ---------------------------------------------------------------------------
// maxSegmentsForDuration
// ---------------------------------------------------------------------------

var _ = Describe("maxSegmentsForDuration", func() {
	It("returns 1 segment per 2 minutes", func() {
		Expect(maxSegmentsForDuration(10 * time.Minute)).To(Equal(5))
		Expect(maxSegmentsForDuration(30 * time.Minute)).To(Equal(15))
	})

	It("has a minimum of 3", func() {
		Expect(maxSegmentsForDuration(2 * time.Minute)).To(Equal(3))
		Expect(maxSegmentsForDuration(0)).To(Equal(3))
	})

	It("has a maximum of 20", func() {
		Expect(maxSegmentsForDuration(60 * time.Minute)).To(Equal(20))
	})
})

// ---------------------------------------------------------------------------
// parseTriagedSegments
// ---------------------------------------------------------------------------

var _ = Describe("parseTriagedSegments", func() {
	allSegments := []Segment{
		{Start: "0:00", End: "0:30", Type: "Snatch"},
		{Start: "0:30", End: "1:00", Type: "Burpee"},
		{Start: "1:00", End: "1:30", Type: "Pull-up"},
		{Start: "1:30", End: "2:00", Type: "Back Squat"},
	}

	It("selects segments by index and returns in chronological order", func() {
		output := "```json\n" +
			`[{"index": 3, "score": 9, "reason": "form"}, {"index": 0, "score": 8, "reason": "technique"}]` +
			"\n```"

		result := parseTriagedSegments(output, allSegments, 5)
		Expect(result).To(HaveLen(2))
		// Should be in chronological order (0, 3), not score order (3, 0)
		Expect(result[0].Type).To(Equal("Snatch"))
		Expect(result[1].Type).To(Equal("Back Squat"))
	})

	It("caps at maxSegs", func() {
		output := "```json\n" +
			`[{"index": 0, "score": 9}, {"index": 1, "score": 8}, {"index": 2, "score": 7}, {"index": 3, "score": 6}]` +
			"\n```"

		result := parseTriagedSegments(output, allSegments, 2)
		Expect(result).To(HaveLen(2))
	})

	It("ignores out-of-range indices", func() {
		output := "```json\n" +
			`[{"index": 99, "score": 9}, {"index": 1, "score": 8}]` +
			"\n```"

		result := parseTriagedSegments(output, allSegments, 5)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Type).To(Equal("Burpee"))
	})

	It("returns nil for unparseable output", func() {
		result := parseTriagedSegments("not json", allSegments, 5)
		Expect(result).To(BeNil())
	})

	It("deduplicates repeated indices", func() {
		output := "```json\n" +
			`[{"index": 1, "score": 9}, {"index": 1, "score": 8}, {"index": 2, "score": 7}]` +
			"\n```"

		result := parseTriagedSegments(output, allSegments, 5)
		Expect(result).To(HaveLen(2))
	})
})
