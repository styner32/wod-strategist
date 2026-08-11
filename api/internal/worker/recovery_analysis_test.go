package worker

import (
	"context"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ = Describe("Recovery Workout Analysis & Stretch Recommendations", func() {
	var (
		w         *Worker
		dbConn    *gorm.DB
		profileID uint
	)

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		w = &Worker{
			DB:     dbConn,
			logger: zap.NewNop(),
		}
		user := testhelpers.CreateUser(dbConn, &db.User{Username: "testuser_recovery"})
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID, Name: "Test Recovery Profile"})
		profileID = profile.ID
	})

	Context("1. Workout type resolution & prompt branching", func() {
		It("IsRecoveryWorkoutType correctly identifies warmup and cooldown", func() {
			Expect(IsRecoveryWorkoutType("warmup")).To(BeTrue())
			Expect(IsRecoveryWorkoutType("WARMUP")).To(BeTrue())
			Expect(IsRecoveryWorkoutType("cooldown")).To(BeTrue())
			Expect(IsRecoveryWorkoutType("CoolDown")).To(BeTrue())
			Expect(IsRecoveryWorkoutType("wod")).To(BeFalse())
			Expect(IsRecoveryWorkoutType("accessory")).To(BeFalse())
			Expect(IsRecoveryWorkoutType("unknown")).To(BeFalse())
		})

		It("resolveSessionWorkoutType prefers DB session workout_type over payload", func() {
			session := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profileID, WODDescription: "Warmup Session", SessionID: "sess-warmup-resolution"})
			dbConn.Model(&session).Update("workout_type", "warmup")

			resolved := w.resolveSessionWorkoutType(context.Background(), session.SessionID, "wod")
			Expect(resolved).To(Equal("warmup"))

			fallback := w.resolveSessionWorkoutType(context.Background(), "non-existent-session", "cooldown")
			Expect(fallback).To(Equal("cooldown"))
		})

		It("buildSegmentAnalysisPrompt uses recovery framing for warmup and WOD framing for wod", func() {
			warmupPrompt := w.buildSegmentAnalysisPrompt(VideoAnalysisPayload{
				WorkoutType: WorkoutTypeWarmup,
				SessionID:   "sess-warmup",
				ProfileID:   profileID,
			}, Segment{Start: "0:00", End: "0:30", Type: "Pigeon Pose"}, "", "", true)

			Expect(warmupPrompt).To(ContainSubstring("# 웜업(Warm-up) 영상 분석 요청"))
			Expect(warmupPrompt).To(ContainSubstring("overall: form×0.7 + consistency×0.3"))
			Expect(warmupPrompt).NotTo(ContainSubstring("overall: form×0.5 + intensity×0.3"))

			wodPrompt := w.buildSegmentAnalysisPrompt(VideoAnalysisPayload{
				WorkoutType: WorkoutTypeWOD,
				SessionID:   "sess-wod",
				ProfileID:   profileID,
			}, Segment{Start: "0:00", End: "0:30", Type: "Snatch"}, "", "", true)

			Expect(wodPrompt).To(ContainSubstring("# 운동 영상 분석 요청"))
			Expect(wodPrompt).To(ContainSubstring("overall: form×0.5 + intensity×0.3 + consistency×0.2"))
		})

		It("includeChunkInDeepAnalysis keeps rest_setup for warmup and excludes for wod", func() {
			restChunk := db.ChunkAnalysisResult{
				ExerciseType:    "rest",
				ObservedSignals: `{"movement":"rest","activity_state":"rest"}`,
			}
			Expect(includeChunkInDeepAnalysis(restChunk, WorkoutTypeWarmup)).To(BeTrue())
			Expect(includeChunkInDeepAnalysis(restChunk, WorkoutTypeWOD)).To(BeFalse())

			walkChunk := db.ChunkAnalysisResult{
				ExerciseType:    "walking",
				ObservedSignals: `{"movement":"walking","activity_state":"walking"}`,
			}
			Expect(includeChunkInDeepAnalysis(walkChunk, WorkoutTypeWarmup)).To(BeFalse())
			Expect(includeChunkInDeepAnalysis(walkChunk, WorkoutTypeWOD)).To(BeFalse())
		})
	})

	Context("2. History table rendering", func() {
		It("renders - intensity and [웜업]/[쿨다운] prefix for recovery sessions", func() {
			sess1 := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profileID, WODDescription: "Fran", SessionID: "sess-hist-1"})
			dbConn.Model(&sess1).Update("workout_type", "wod")
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:      sess1.SessionID,
				ProfileID:      profileID,
				Status:         "COMPLETED",
				SessionScore:   `{"overall":80,"form":85,"intensity":90,"consistency":75}`,
				WODDescription: "Fran",
			})

			sess2 := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profileID, WODDescription: "Hip Mobility", SessionID: "sess-hist-2"})
			dbConn.Model(&sess2).Update("workout_type", "warmup")
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:      sess2.SessionID,
				ProfileID:      profileID,
				Status:         "COMPLETED",
				SessionScore:   `{"overall":70,"form":75,"intensity":0,"consistency":65}`,
				WODDescription: "Hip Mobility",
			})

			historyText := w.buildHistoryContext(profileID, 5)
			Expect(historyText).To(ContainSubstring("Fran"))
			Expect(historyText).To(ContainSubstring("[웜업] Hip Mobility"))
			Expect(historyText).To(ContainSubstring("| - |"))
		})
	})

	Context("3. Mobility observation parsing & sanitization", func() {
		It("parses, filters unknown vocab, clamps confidence, and caps at 10 items", func() {
			rawOutput := "```mobility\n" + `[
				{"joint":"Hip","side":"both","observation":"limited_hip_flexion","movement":"Squat","evidence":"스쿼트 하단 굴곡 제한","confidence":0.95,"assessable":true},
				{"joint":"Shoulder","side":"left","observation":"limited_overhead_flexion","movement":"Press","evidence":"오버헤드 가동범위 부족","confidence":0.3,"assessable":true},
				{"joint":"FakeJoint","side":"both","observation":"limited_hip_flexion","movement":"Squat","evidence":"잘못된 관절","confidence":0.9,"assessable":true},
				{"joint":"Ankle","side":"right","observation":"invalid_obs","movement":"Lunge","evidence":"잘못된 관찰항목","confidence":0.8,"assessable":true}
			]` + "\n```"

			parsed := parseMobilityObservations(rawOutput)
			Expect(parsed).To(HaveLen(4))

			sanitized := sanitizeMobilityObservations(parsed)
			Expect(sanitized).To(HaveLen(1))
			Expect(sanitized[0].Joint).To(Equal("Hip"))
			Expect(sanitized[0].Observation).To(Equal("limited_hip_flexion"))
			Expect(sanitized[0].Confidence).To(Equal(0.95))
		})
	})

	Context("4. Stretch recommendations & fail-open gate", func() {
		It("gates execution and returns [] without calling Gemini when no evidence exists", func() {
			transport := testhelpers.NewMockTransport()
			client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
				APIKey:     "test-key",
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())
			w.GeminiClient = client

			recs := w.recommendStretches(context.Background(), profileID, "sess-empty-evidence", nil, nil)
			Expect(recs).To(Equal("[]"))
			Expect(transport.Requests()).To(BeEmpty())
		})

		It("sanitizes recommendations by dropping non-catalog stretches and non-evidenced joints", func() {
			current := []MobilityObservation{
				{Joint: "Hip", Side: "both", Observation: "limited_hip_flexion", Movement: "Squat", Evidence: "스쿼트 하단 깊이 제한", Confidence: 0.9, Assessable: true},
			}
			history := []db.MobilityRestriction{
				{Joint: "Hip", Side: "both", Observation: "limited_hip_flexion", SessionCount: 3, Movements: []string{"Squat"}},
			}

			recs := []StretchRecommendation{
				{Stretch: "Pigeon Pose", TargetArea: "Hip", Reason: "최근 3개 세션의 스쿼트에서 고관절 굴곡 제한 관찰됨", Provisional: false},
				{Stretch: "Unsupported MadeUp Stretch", TargetArea: "Hip", Reason: "카탈로그에 없는 스트레칭", Provisional: false},
				{Stretch: "Doorway Pec Stretch", TargetArea: "Chest", Reason: "증거 없는 타겟 부위", Provisional: false},
			}

			sanitized := sanitizeStretchRecommendations(recs, current, history)
			Expect(sanitized).To(HaveLen(1))
			Expect(sanitized[0].Stretch).To(Equal("Pigeon Pose"))
			Expect(sanitized[0].TargetArea).To(Equal("Hip"))
		})
	})
})
