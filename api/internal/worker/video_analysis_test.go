package worker

import (
	"context"
	"encoding/json"
	"errors"
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
		Expect(prompt).To(ContainSubstring("운동 후보 힌트"))
		Expect(prompt).To(ContainSubstring("Burpee"))
		Expect(prompt).To(ContainSubstring("영상에 등장한다는 보장이 없습니다"))
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

	It("requires visible target evidence and excludes unseen planned movements and non-exercise fatigue", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			SessionID:      "session-evidence",
			WODDescription: "For Time: Pull-up, Rope Climb, then accessory work",
			Movements:      []string{"Pull-up", "Rope Climb"},
		}, 60)

		Expect(prompt).To(ContainSubstring("기구 접촉"))
		Expect(prompt).To(ContainSubstring("배경 인물"))
		Expect(prompt).To(ContainSubstring("목록에 있어도 보이지 않으면 결과·점수·하이라이트에서 제외"))
		Expect(prompt).To(ContainSubstring("걷기, 휴식, 회복, 준비, 장비 세팅, Unknown은 하이라이트"))
		Expect(prompt).To(ContainSubstring("fatigue_onset는 심박수만으로 만들지 말고"))
	})

	It("requests at most three exact evidence items without category quotas", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{WorkoutType: WorkoutTypeWOD}, 60)

		Expect(prompt).To(ContainSubstring("구간당 최대 3개"))
		Expect(prompt).To(ContainSubstring("카테고리별 개수 할당량은 없습니다"))
		Expect(prompt).To(ContainSubstring("confidence"))
		Expect(prompt).To(ContainSubstring("0.0~1.0"))
		for _, evidenceType := range []string{
			HighlightObservationPositiveForm,
			HighlightObservationFormIssue,
			HighlightObservationFatigueOnset,
			HighlightObservationTechnique,
		} {
			Expect(prompt).To(ContainSubstring(evidenceType))
		}
		Expect(prompt).NotTo(ContainSubstring("카테고리별 최소"))
		Expect(prompt).NotTo(ContainSubstring("2개 이상"))
		Expect(prompt).NotTo(ContainSubstring("무제한"))
	})

	It("uses the default profile when ProfileID is 0", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			ProfileID:   0,
		}, 0)

		Expect(prompt).To(ContainSubstring("1984년 10월 17일"))
	})

	It("normalizes known workout types and defaults unknown to wod", func() {
		Expect(NormalizeWorkoutType("warmup")).To(Equal(WorkoutTypeWarmup))
		Expect(NormalizeWorkoutType("WARMUP")).To(Equal(WorkoutTypeWarmup))
		Expect(NormalizeWorkoutType("accessory")).To(Equal(WorkoutTypeAccessory))
		Expect(NormalizeWorkoutType("cooldown")).To(Equal(WorkoutTypeCooldown))
		Expect(NormalizeWorkoutType("wod")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("rehab")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("anything")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("")).To(Equal(WorkoutTypeWOD))
	})

	It("validates known workout types and rejects unknown ones", func() {
		Expect(IsValidWorkoutType("wod")).To(BeTrue())
		Expect(IsValidWorkoutType("WOD")).To(BeTrue())
		Expect(IsValidWorkoutType("warmup")).To(BeTrue())
		Expect(IsValidWorkoutType("WARMUP")).To(BeTrue())
		Expect(IsValidWorkoutType("accessory")).To(BeTrue())
		Expect(IsValidWorkoutType("cooldown")).To(BeTrue())
		Expect(IsValidWorkoutType("rehab")).To(BeFalse())
		Expect(IsValidWorkoutType("anything")).To(BeFalse())
		Expect(IsValidWorkoutType("")).To(BeFalse())
	})
})

var _ = Describe("open-set movement prompts", func() {
	var w *Worker

	BeforeEach(func() {
		w = &Worker{logger: zap.NewNop()}
	})

	It("does not let nearby equipment or background athletes override target motion", func() {
		prompt := w.buildIndexPrompt(VideoAnalysisPayload{
			Movements: []string{"Rope Climb"},
		}, 2*time.Minute)

		Expect(prompt).To(ContainSubstring("suggestions, not confirmation and not a closed list"))
		Expect(prompt).To(ContainSubstring("apparatus contact"))
		Expect(prompt).To(ContainSubstring("body position"))
		Expect(prompt).To(ContainSubstring("continuous motion pattern"))
		Expect(prompt).To(ContainSubstring("A rope beside a pull-up bar does not make a target person's pull-up a rope climb"))
		Expect(prompt).To(ContainSubstring("Background athletes"))
		Expect(prompt).To(ContainSubstring(`use type "Unknown"`))
	})

	It("revalidates a preliminary chunk label during deep analysis", func() {
		prompt := w.buildSegmentAnalysisPrompt(
			VideoAnalysisPayload{Movements: []string{"Rope Climb"}},
			Segment{Start: "0:10", End: "0:20", Type: "Rope Climb"},
			"",
			"",
			false,
		)

		Expect(prompt).To(ContainSubstring("중간 종목 라벨 (확정 아님)"))
		Expect(prompt).To(ContainSubstring("현재 구간의 대상 인물 영상 근거로 다시 식별"))
		Expect(prompt).To(ContainSubstring("라벨을 교정하거나 Unknown"))
		Expect(prompt).To(ContainSubstring("힌트에 없는 종목이라도 시각적 근거가 충분"))
	})

	It("adds the all-chunk fatigue aggregate only to the final segment conclusion", func() {
		context := "\n\n## 세션 전체 구조화 피로 근거 (모든 수신 청크 집계)\n- 구조화 청크: 4"
		first := w.buildSegmentAnalysisPrompt(VideoAnalysisPayload{}, Segment{Start: "0:00", End: "0:10", Type: "Snatch"}, "", context, false)
		last := w.buildSegmentAnalysisPrompt(VideoAnalysisPayload{}, Segment{Start: "0:10", End: "0:20", Type: "Snatch"}, "", context, true)

		Expect(first).NotTo(ContainSubstring("모든 수신 청크 집계"))
		Expect(last).To(ContainSubstring("모든 수신 청크 집계"))
		Expect(last).To(ContainSubstring("구조화 청크: 4"))
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
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].Movement).To(Equal("Triceps Extension"))
		Expect(segs[1].Movement).To(Equal("Box Step-up"))
		Expect(segs[1].Observations).To(HaveLen(2))
		Expect(HighlightSegmentHasTag(segs[1], HighlightTagKeyMoment)).To(BeTrue())
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

	It("drops walking, rest, setup, recovery, and unknown highlights", func() {
		input := "```highlights\n" +
			`[{"start":"0:00","end":"0:05","type":"fatigue_point","movement":"Walking","reason":"high bpm"},{"start":"0:05","end":"0:10","type":"fatigue_point","movement":"Rest","reason":"resting"},{"start":"0:10","end":"0:15","type":"key_moment","movement":"Unknown","reason":"unclear"},{"start":"0:15","end":"0:20","type":"best_form","movement":"Pull-up","reason":"visible reps"},{"start":"0:20","end":"0:25","type":"fatigue_point","movement":"Burpee","reason":"heart rate reached 170 bpm"},{"start":"0:25","end":"0:30","type":"fatigue_point","movement":"Burpee","reason":"rep cadence slowed for several reps while heart rate stayed high"}]` +
			"\n```"

		result := ParseHighlightSegments(input)
		var segs []HighlightSegment
		Expect(json.Unmarshal([]byte(result), &segs)).To(Succeed())
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].Movement).To(Equal("Pull-up"))
		Expect(segs[1].Movement).To(Equal("Burpee"))
		Expect(segs[1].Reason).To(ContainSubstring("rep cadence slowed"))
	})

	It("parses the full 13-segment real-world Gemini output", func() {
		// This test uses the exact output format from a real 13-segment workout analysis.
		// Segments use mostly ```highlights blocks but segment 6 uses <highlights> XML tags.
		input := realWorldMultiSegmentOutput
		result := ParseHighlightSegments(input)
		Expect(result).NotTo(BeEmpty())

		var segs []HighlightSegment
		Expect(json.Unmarshal([]byte(result), &segs)).To(Succeed())

		// The legacy flat output is consolidated into parent playback events and
		// capped at one positive, one improvement, and one technique card per movement.
		Expect(segs).To(HaveLen(21))

		// Verify highlights from different segments are represented
		movements := map[string]bool{}
		starts := map[string]bool{}
		movementCounts := map[string]int{}
		for _, seg := range segs {
			movements[seg.Movement] = true
			starts[seg.Start] = true
			movementCounts[normalizeHighlightMovementKey(seg.Movement)]++
			Expect(seg.Version).To(Equal(2))
			Expect(seg.Observations).NotTo(BeEmpty())
			start, startErr := parseTimestampToSeconds(seg.Start)
			end, endErr := parseTimestampToSeconds(seg.End)
			Expect(startErr).NotTo(HaveOccurred())
			Expect(endErr).NotTo(HaveOccurred())
			Expect(end - start).To(BeNumerically(">=", 5))
			Expect(end - start).To(BeNumerically("<=", 20))
		}
		for _, count := range movementCounts {
			Expect(count).To(BeNumerically("<=", 3))
		}
		Expect(starts).NotTo(HaveKey("04:58.000"))
		Expect(starts).NotTo(HaveKey("17:48"))
		Expect(starts).NotTo(HaveKey("21:40"))
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
		Expect(toesToBarCount).To(BeNumerically(">", 0))
		Expect(toesToBarCount).To(BeNumerically("<=", 3))
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
			false,
			"",
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
		profileID        uint
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

		// The generateContent body must reference the file URI returned by the
		// upload chain — a client that drops or mangles it fails here, not in prod.
		transport.New(geminiBaseURL).
			Post("/v1beta/models/"+gemini.ModelPro31Preview+":generateContent").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			MatchBodyContains(`"fileUri":"` + geminiBaseURL + `/files/mock-file"`).
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

		logger, _ := zap.NewDevelopment()
		w = &Worker{
			DB:            dbConn,
			StorageClient: storageClient,
			GeminiClient:  nil, // set per-test via setupGeminiTransport
			QueueClient:   queueClient,
			BucketName:    "test-bucket",
			logger:        logger,
		}

		p := testhelpers.CreateProfile(dbConn, &db.Profile{})
		profileID = p.ID
	})

	It("rejects a task with no resolvable profile before accessing video services", func() {
		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "WOD-20260715-01J00000000000000000000000",
			FilePath:  "gs://test-bucket/videos/0/unowned/video.mp4",
		})

		err := w.HandleVideoAnalysisTask(context.Background(), task)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, asynq.SkipRetry)).To(BeTrue())
		Expect(storageTransport.Requests()).To(BeEmpty())

		var count int64
		Expect(dbConn.Model(&db.AnalysisResult{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("rejects a task whose profile does not own the persisted session", func() {
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID: "WOD-20260715-01J00000000000000000000001",
			ProfileID: profileID,
		})
		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "WOD-20260715-01J00000000000000000000001",
			ProfileID: other.ID,
			FilePath:  "gs://test-bucket/videos/mismatch/video.mp4",
		})

		err := w.HandleVideoAnalysisTask(context.Background(), task)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, asynq.SkipRetry)).To(BeTrue())
		Expect(storageTransport.Requests()).To(BeEmpty())
	})

	It("recovers profile_id=0 for a queued task from its persisted session", func() {
		const sessionID = "WOD-20260715-01J00000000000000000000002"
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID: sessionID,
			ProfileID: profileID,
		})
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/0/"+sessionID+"/video.mp4")
		setupGeminiTransport(analysisWithHighlights)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: sessionID,
			ProfileID: 0,
			FilePath:  "gs://test-bucket/videos/0/" + sessionID + "/video.mp4",
		})
		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())

		var result db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", sessionID).First(&result).Error).To(Succeed())
		Expect(result.ProfileID).To(Equal(profileID))
	})

	It("persists a COMPLETED AnalysisResult with parsed highlight segments", func() {
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-happy-001/chunk_001.mp4")
		transport := setupGeminiTransport(analysisWithHighlights)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID:   "sess-happy-001",
			ProfileID:   profileID,
			FilePath:    "gs://test-bucket/videos/sess-happy-001/chunk_001.mp4",
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Burpee", "Pull-up"},
		})
		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())

		Expect(transport.Verify()).To(Succeed())

		// Wire-level body check: the analyze request must carry the prompt built
		// for this payload's movements. Unmatched extra requests already fail in
		// RoundTrip, so no exact request-count assertion is needed.
		var genBody string
		for _, r := range transport.Requests() {
			if strings.Contains(r.URL, ":generateContent") {
				genBody = string(r.Body)
			}
		}
		Expect(genBody).To(ContainSubstring("운동 후보 힌트"))
		Expect(genBody).To(ContainSubstring("Burpee, Pull-up"))

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
		user := db.User{
			Username:     "test-username",
			PasswordHash: "test-password",
		}
		Expect(dbConn.Create(&user).Error).NotTo(HaveOccurred())

		profile := db.Profile{
			UserID:    user.ID,
			BirthYear: ptr(1990), BirthMonth: ptr(6), BirthDay: ptr(15),
			Gender: ptr("male"), HeightCm: ptr(175), WeightKg: ptr(75.0),
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
		Expect(result.ProfileID).To(Equal(profile.ID))
	})

	It("enqueues an injury:analysis follow-up task with parsed focus timestamps", func() {
		analysisWithInjury := analysisWithHighlights + "\n```injury_timestamps\n" +
			`[{"start":"0:32","end":"0:45","reason":"무릎 내전 관찰"}]` + "\n```"

		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-injury-001/chunk_001.mp4")
		transport := setupGeminiTransport(analysisWithInjury)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "sess-injury-001",
			FilePath:  "gs://test-bucket/videos/sess-injury-001/chunk_001.mp4",
			ProfileID: profileID,
			Injuries:  []string{"Knee"},
		})

		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		// Give asynq a moment to flush to Redis, then inspect.
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(2))
		var injuryTask *asynq.TaskInfo
		var taskTypes []string
		for _, info := range pending {
			taskTypes = append(taskTypes, info.Type)
			if info.Type == TypeInjuryAnalysis {
				injuryTask = info
			}
		}
		Expect(taskTypes).To(ConsistOf(TypeInjuryAnalysis, TypeGenerateHardSub))
		Expect(injuryTask).NotTo(BeNil())

		var injPayload InjuryAnalysisPayload
		Expect(json.Unmarshal(injuryTask.Payload, &injPayload)).To(Succeed())
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
			ProfileID: profileID,
		})

		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		// Hardsub is enqueued, but no injury follow-up is created.
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal(TypeGenerateHardSub))
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
			Post("/v1beta/models/" + gemini.ModelPro31Preview + ":generateContent").
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
			ProfileID: profileID,
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
	var (
		geminiBaseURL    = "https://generativelanguage.googleapis.com"
		geminiAPIKey     = "test-api-key"
		dbConn           *gorm.DB
		storageTransport *testhelpers.MockTransport
		queueClient      *asynq.Client
		inspector        *asynq.Inspector
		w                *Worker
		profileID        uint
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
			Post("/v1beta/models/"+gemini.ModelPro31Preview+":generateContent").
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
			Post("/v1beta/models/"+gemini.ModelPro31Preview+":generateContent").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{{"text": segmentAnalysis}},
					},
				}},
			})

		return transport
	}

	seedChunks := func(sessionID string, profileID uint) {
		start0 := 0.0
		end0 := 45.0
		start1 := 50.0
		end1 := 90.0
		mediaStart0 := 0.0
		mediaEnd0 := 45.0
		mediaStart1 := 50.0
		mediaEnd1 := 90.0
		Expect(dbConn.Create(&db.ChunkAnalysisResult{
			SessionID:       sessionID,
			ProfileID:       profileID,
			FilePath:        "gs://test-bucket/videos/" + sessionID + "/chunk_0.mp4",
			Status:          "COMPLETED",
			ExerciseType:    "Snatch",
			Output:          "좋은 스내치 자세입니다",
			ObservedSignals: `{"movement":"Snatch","activity_state":"exercise","fatigue_visually_established":true,"fatigue_evidence_types":["rep_slowdown"],"fatigue_evidence":["반복 속도가 지속적으로 감소함"]}`,
			StartSecs:       &start0,
			EndSecs:         &end0,
			MediaStartSecs:  &mediaStart0,
			MediaEndSecs:    &mediaEnd0,
		}).Error).NotTo(HaveOccurred())
		Expect(dbConn.Create(&db.ChunkAnalysisResult{
			SessionID:       sessionID,
			ProfileID:       profileID,
			FilePath:        "gs://test-bucket/videos/" + sessionID + "/chunk_1.mp4",
			Status:          "COMPLETED",
			ExerciseType:    "Pull-up",
			Output:          "풀업 킵핑 동작이 안정적입니다",
			ObservedSignals: `{"movement":"Pull-up","activity_state":"exercise","fatigue_visually_established":false,"fatigue_evidence_types":[],"fatigue_evidence":[]}`,
			StartSecs:       &start1,
			EndSecs:         &end1,
			MediaStartSecs:  &mediaStart1,
			MediaEndSecs:    &mediaEnd1,
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

		p := testhelpers.CreateProfile(dbConn, &db.Profile{})
		profileID = p.ID
	})

	It("builds deep-analysis segments only from verified media offsets", func() {
		captureStart := 900.0
		captureEnd := 910.0
		mediaStart := 12.25
		mediaEnd := 22.75
		Expect(dbConn.Create(&db.ChunkAnalysisResult{
			SessionID:         "sess-media-timeline-001",
			ProfileID:         profileID,
			Status:            "COMPLETED",
			ExerciseType:      "Unknown",
			Output:            "movement requires deeper revalidation",
			ObservedSignals:   `{"movement":"Unknown","activity_state":"unknown"}`,
			StartSecs:         &captureStart,
			EndSecs:           &captureEnd,
			MediaStartSecs:    &mediaStart,
			MediaEndSecs:      &mediaEnd,
			WorkoutConfidence: 0.5,
		}).Error).NotTo(HaveOccurred())

		missingMediaStart := 1000.0
		missingMediaEnd := 1010.0
		Expect(dbConn.Create(&db.ChunkAnalysisResult{
			SessionID:       "sess-media-timeline-001",
			ProfileID:       profileID,
			Status:          "COMPLETED",
			ExerciseType:    "Snatch",
			ObservedSignals: `{}`,
			StartSecs:       &missingMediaStart,
			EndSecs:         &missingMediaEnd,
		}).Error).NotTo(HaveOccurred())

		walkingStart := 22.75
		walkingEnd := 30.0
		Expect(dbConn.Create(&db.ChunkAnalysisResult{
			SessionID:       "sess-media-timeline-001",
			ProfileID:       profileID,
			Status:          "COMPLETED",
			ExerciseType:    "Walking",
			ObservedSignals: `{"movement":"Walking","activity_state":"walking"}`,
			MediaStartSecs:  &walkingStart,
			MediaEndSecs:    &walkingEnd,
		}).Error).NotTo(HaveOccurred())

		segments := w.buildSegmentsFromChunks("sess-media-timeline-001")
		Expect(segments).To(Equal([]Segment{{
			Start:       "0:12.25",
			End:         "0:22.75",
			Type:        "Unknown",
			Description: "movement requires deeper revalidation",
		}}))
	})

	It("persists COMPLETED result using chunk-based segments (no injuries)", func() {
		seedChunks("sess-twopass-001", profileID)
		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-twopass-001/merged.mp4")
		transport := setupTwoPassTransport(analysisWithHighlights, false)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID:   "sess-twopass-001",
			ProfileID:   profileID,
			FilePath:    "gs://test-bucket/videos/sess-twopass-001/merged.mp4",
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Snatch"},
		})
		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())

		Expect(transport.Verify()).To(Succeed())
		// 5 requests: upload + finalize + poll + analyzeSegment x2 (no deleteFile)
		Expect(transport.Requests()).To(HaveLen(5))

		var result db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", "sess-twopass-001").First(&result).Error).
			NotTo(HaveOccurred())
		Expect(result.Status).To(Equal("COMPLETED"))
		Expect(result.Output).To(ContainSubstring("훌륭한 운동이었습니다"))

		fatigueContextBodies := 0
		for _, request := range transport.Requests() {
			if strings.Contains(string(request.Body), "모든 수신 청크 집계") {
				fatigueContextBodies++
				Expect(string(request.Body)).To(ContainSubstring("구조화 청크: 2"))
				Expect(string(request.Body)).To(ContainSubstring("시각적으로 피로가 확립된 운동 청크: 1"))
			}
		}
		Expect(fatigueContextBodies).To(Equal(1))

		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		// Only hardsub:generate should be enqueued (no injury task)
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal("hardsub:generate"))
	})

	It("enqueues injury task with file URI and does NOT delete file", func() {
		seedChunks("sess-twopass-inj-001", profileID)
		analysisWithInjury := analysisWithHighlights + "\n```injury_timestamps\n" +
			`[{"start":"0:32","end":"0:45","reason":"무릎 내전 관찰"}]` + "\n```"

		testhelpers.MockGCSDownload(storageTransport, "gs://test-bucket/videos/sess-twopass-inj-001/merged.mp4")
		transport := setupTwoPassTransport(analysisWithInjury, true)

		task := makeVideoAnalysisTask(VideoAnalysisPayload{
			SessionID: "sess-twopass-inj-001",
			FilePath:  "gs://test-bucket/videos/sess-twopass-inj-001/merged.mp4",
			ProfileID: profileID,
			Injuries:  []string{"Knee"},
		})

		Expect(w.HandleVideoAnalysisTask(context.Background(), task)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		// 5 requests: upload + finalize + poll + analyzeSegment x2 (no deleteFile)
		Expect(transport.Requests()).To(HaveLen(5))

		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(2))
		var injuryTask *asynq.TaskInfo
		var taskTypes []string
		for _, info := range pending {
			taskTypes = append(taskTypes, info.Type)
			if info.Type == TypeInjuryAnalysis {
				injuryTask = info
			}
		}
		Expect(taskTypes).To(ConsistOf(TypeInjuryAnalysis, TypeGenerateHardSub))
		Expect(injuryTask).NotTo(BeNil())

		var injPayload InjuryAnalysisPayload
		Expect(json.Unmarshal(injuryTask.Payload, &injPayload)).To(Succeed())
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

	It("preserves fractional media timestamps", func() {
		Expect(convertToSeconds("0:12.250")).To(Equal(12250 * time.Millisecond))
		Expect(convertToSeconds("1:02.5")).To(Equal(62500 * time.Millisecond))
		Expect(convertToSeconds("1:00:00.125")).To(Equal(3600125 * time.Millisecond))
		Expect(convertToSeconds("12.25s")).To(Equal(12250 * time.Millisecond))
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

	It("preserves a rest gap between repeated movement labels", func() {
		segments := []Segment{
			{Start: "0:00", End: "0:10", Type: "Snatch"},
			{Start: "0:20", End: "0:30", Type: "Snatch"},
		}

		Expect(mergeSegmentsByMovement(segments)).To(HaveLen(2))
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
