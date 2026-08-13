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

		It("sanitizes recommendations by resolving existing/similar DB stretches and formatting new valid stretches", func() {
			current := []MobilityObservation{
				{Joint: "Hip", Side: "both", Observation: "limited_hip_flexion", Movement: "Squat", Evidence: "스쿼트 하단 깊이 제한", Confidence: 0.9, Assessable: true},
				{Joint: "Wrist", Side: "right", Observation: "limited_wrist_extension", Movement: "Clean", Evidence: "프론트렉 손목 목통증", Confidence: 0.8, Assessable: true},
			}
			history := []db.MobilityRestriction{
				{Joint: "Hip", Side: "both", Observation: "limited_hip_flexion", SessionCount: 3, Movements: []string{"Squat"}},
			}

			// Seed a custom stretch & alias in DB
			s := testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Samson (Hip Flexor Lunge) Stretch", TargetArea: "Hip"})
			testhelpers.CreateStretchAlias(dbConn, &db.StretchAlias{StretchID: s.ID, Alias: "Hip Flexor Stretch"})

			dbNames, dbResolver := w.loadStretchCatalog(context.Background())
			Expect(dbNames).To(ContainElement("Samson (Hip Flexor Lunge) Stretch"))
			Expect(dbResolver["hip flexor stretch"]).To(Equal("Samson (Hip Flexor Lunge) Stretch"))

			recs := []StretchRecommendation{
				// 1. Exact match
				{Stretch: "Pigeon Pose", TargetArea: "Hip", Reason: "스쿼트 고관절 굴곡 제한", Provisional: false},
				// 2. Alias match
				{Stretch: "Hip Flexor Stretch", TargetArea: "Hip", Reason: "고관절 굴곡근 타이트함", Provisional: false},
				// 3. Similar / fuzzy match ("Wrist Flexor Stretch" matches catalog "Wrist Flexor/Extensor Stretch")
				{Stretch: "Wrist Flexor Stretch", TargetArea: "Wrist", Reason: "프론트렉 손목 신전 제한", Provisional: true},
				// 4. Valid new stretch (not in DB catalog) formatted to Title Case
				{Stretch: "hip 90/90 stretch", TargetArea: "Hip", Reason: "고관절 회전 모빌리티", Provisional: true},
				// 5. Invalid non-English / sentence new stretch (should be dropped)
				{Stretch: "스쿼트 하단 고관절 스트레칭", TargetArea: "Hip", Reason: "서술적 명칭", Provisional: true},
			}

			sanitized := w.sanitizeAndPersistStretchRecommendations(context.Background(), recs, current, history, dbResolver)
			Expect(sanitized).To(HaveLen(3)) // capped at 3
			Expect(sanitized[0].Stretch).To(Equal("Pigeon Pose"))
			Expect(sanitized[1].Stretch).To(Equal("Samson (Hip Flexor Lunge) Stretch"))
			Expect(sanitized[2].Stretch).To(Equal("Wrist Flexor/Extensor Stretch")) // resolved similar match!
		})

		It("formats and auto-persists a new valid stretch to DB catalog when no similar stretch exists", func() {
			current := []MobilityObservation{
				{Joint: "Hip", Side: "both", Observation: "limited_hip_flexion", Movement: "Squat", Evidence: "스쿼트 깊이 부족", Confidence: 0.9, Assessable: true},
			}

			_, dbResolver := w.loadStretchCatalog(context.Background())

			recs := []StretchRecommendation{
				{Stretch: "adductor groin stretch", TargetArea: "Hip", Reason: "내전근 가동성 향상", DurationHint: "60s", Caution: "과도한 자극 피함", Provisional: true},
			}

			sanitized := w.sanitizeAndPersistStretchRecommendations(context.Background(), recs, current, nil, dbResolver)
			Expect(sanitized).To(HaveLen(1))
			Expect(sanitized[0].Stretch).To(Equal("Adductor Groin Stretch")) // Formatted Title Case

			// Verify it was auto-inserted into DB stretches table
			var newDbStretch db.Stretch
			Expect(dbConn.Where("normalized_key = ?", "adductor groin stretch").First(&newDbStretch).Error).NotTo(HaveOccurred())
			Expect(newDbStretch.Name).To(Equal("Adductor Groin Stretch"))
			Expect(newDbStretch.TargetArea).To(Equal("Hip"))
		})
	})
})
