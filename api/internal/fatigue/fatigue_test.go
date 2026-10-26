package fatigue_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/fatigue"
)

var _ = Describe("Fatigue Calculation & Readiness", func() {
	Context("GetMovementWeights", func() {
		It("preserves generic snatch weights without guessing other unspecified variants", func() {
			Expect(fatigue.GetMovementWeights("Snatch").QuadsSquat).To(Equal(0.7))
			Expect(fatigue.GetMovementWeights("Power Snatch").QuadsSquat).To(Equal(0.4))
			Expect(fatigue.GetMovementWeights("Squat Snatch").QuadsSquat).To(Equal(0.8))
			for _, raw := range []string{"Muscle-up", "Dips", "Squats", "Overhead Press", "Jump Rope", "GHD"} {
				Expect(fatigue.GetMovementWeights(raw)).To(Equal(fatigue.GetMovementWeights("Some Unknown Exercise")), raw)
			}
			Expect(fatigue.GetMovementWeights("Clean and Jerk")).To(Equal(fatigue.GetMovementWeights("Clean & Jerk")))
		})
		It("maps known movements correctly", func() {
			snatchW := fatigue.GetMovementWeights("Power Snatch")
			Expect(snatchW.PosteriorChain).To(BeNumerically(">=", 0.8))
			Expect(snatchW.ShouldersPush).To(BeNumerically(">=", 0.4))

			pullUpW := fatigue.GetMovementWeights("Pull-up")
			Expect(pullUpW.UpperPullGrip).To(BeNumerically("==", 1.0))
			Expect(pullUpW.QuadsSquat).To(BeNumerically("==", 0.0))

			thrusterW := fatigue.GetMovementWeights("Thruster")
			Expect(thrusterW.ShouldersPush).To(BeNumerically(">=", 0.8))
			Expect(thrusterW.QuadsSquat).To(BeNumerically(">=", 0.8))
		})

		It("returns sensible defaults for unknown movements", func() {
			unknownW := fatigue.GetMovementWeights("Some Unknown Exercise")
			Expect(unknownW.CardioMetabolic).To(Equal(0.5))
			Expect(unknownW.ShouldersPush).To(Equal(0.3))
		})

		It("matches deterministically across repeated calls", func() {
			first := fatigue.GetMovementWeights("squat")
			for i := 0; i < 100; i++ {
				w := fatigue.GetMovementWeights("squat")
				Expect(w).To(Equal(first))
			}
		})

		It("matches partial query deterministically to longest catalog key", func() {
			w := fatigue.GetMovementWeights("squat")
			Expect(w.QuadsSquat).To(BeNumerically(">", 0.0))

			ambiguousInputs := []string{"squat", "press", "snatch", "clean"}
			for _, input := range ambiguousInputs {
				expected := fatigue.GetMovementWeights(input)
				for i := 0; i < 50; i++ {
					Expect(fatigue.GetMovementWeights(input)).To(Equal(expected))
				}
			}
		})
	})

	Context("ComputeSessionMuscleLoads", func() {
		It("returns (nil, false) when movements are empty or all unknown/walking/rest", func() {
			scoreJSON := `{"intensity":70,"movements":{"walking":{},"rest":{},"unknown":{}}}`
			loads, ok := fatigue.ComputeSessionMuscleLoads(scoreJSON)
			Expect(ok).To(BeFalse())
			Expect(loads).To(BeNil())
		})

		It("calculates deterministic loads without sensor HR bonus", func() {
			scoreJSON := `{"intensity":70,"movements":{"Thruster":{},"Pull-up":{}}}`
			loads, ok := fatigue.ComputeSessionMuscleLoads(scoreJSON)
			Expect(ok).To(BeTrue())
			Expect(loads).NotTo(BeNil())
			Expect(loads[fatigue.GroupShouldersPush]).To(BeNumerically(">", 0.0))
			// Deterministic check: loads are identical regardless of sensor presence
			loadsWithSensor, _ := fatigue.ComputeSessionMuscleLoadsWithSensor(scoreJSON, `{"quality":{"valid_hr":true,"is_complete":true},"hr_bonus":15.0}`)
			Expect(loads[fatigue.GroupCardioMetabolic]).To(Equal(loadsWithSensor[fatigue.GroupCardioMetabolic]))
		})

		It("does not default the load factor when assessment withheld intensity", func() {
			loads, ok := fatigue.ComputeSessionMuscleLoads(`{"intensity":null,"movements":{"Power Clean":{"form":80}}}`)
			Expect(ok).To(BeFalse())
			Expect(loads).To(BeNil())
			_, ok = fatigue.ComputeSessionMuscleLoads(`{"movements":{"Power Clean":{"form":80}}}`)
			Expect(ok).To(BeTrue(), "legacy scores without an intensity key stay readable")
		})
	})

	Context("ComputeCurrentReadiness with decay and evidence status", func() {
		It("shows high fatigue immediately after workout, and decays over time", func() {
			now := time.Now()

			records := []fatigue.SessionLoadRecord{
				{
					SessionID: "sess-1",
					CreatedAt: now.Add(-12 * time.Hour),
					MuscleLoads: map[string]float64{
						fatigue.GroupShouldersPush:   80.0,
						fatigue.GroupPosteriorChain:  20.0,
						fatigue.GroupQuadsSquat:      10.0,
						fatigue.GroupUpperPullGrip:   10.0,
						fatigue.GroupCoreMidline:     10.0,
						fatigue.GroupCardioMetabolic: 30.0,
					},
				},
			}

			evidence := fatigue.EvidenceCounts{
				TotalSessions:          1,
				ValidSessions:          1,
				ExcludedSessions:       0,
				UnresolvedTimeSessions: 0,
			}

			readiness12h := fatigue.ComputeCurrentReadiness(records, evidence, now)
			Expect(readiness12h.EvidenceStatus).To(Equal("complete"))
			Expect(readiness12h.OverallFatigueScore).NotTo(BeNil())
			Expect(readiness12h.Muscles[fatigue.GroupShouldersPush].FatigueScore).To(BeNumerically("~", 61, 3))
			Expect(readiness12h.Muscles[fatigue.GroupShouldersPush].State).To(Equal("fatigued"))

			// Same session evaluated 60 hours later
			readiness60h := fatigue.ComputeCurrentReadiness(records, evidence, now.Add(48*time.Hour))
			Expect(readiness60h.Muscles[fatigue.GroupShouldersPush].FatigueScore).To(BeNumerically("~", 20, 2))
			Expect(readiness60h.Muscles[fatigue.GroupShouldersPush].State).To(Equal("fresh"))
		})

		It("sets evidence_status=no_history when total=0 and unresolved=0", func() {
			evidence := fatigue.EvidenceCounts{
				TotalSessions:          0,
				ValidSessions:          0,
				ExcludedSessions:       0,
				UnresolvedTimeSessions: 0,
			}
			res := fatigue.ComputeCurrentReadiness(nil, evidence, time.Now())
			Expect(res.EvidenceStatus).To(Equal("no_history"))
			Expect(res.OverallFatigueScore).To(BeNil())
			Expect(res.Muscles).To(BeNil())
		})

		It("sets evidence_status=insufficient when valid=0 but total or unresolved > 0", func() {
			evidence := fatigue.EvidenceCounts{
				TotalSessions:          2,
				ValidSessions:          0,
				ExcludedSessions:       2,
				UnresolvedTimeSessions: 0,
			}
			res := fatigue.ComputeCurrentReadiness(nil, evidence, time.Now())
			Expect(res.EvidenceStatus).To(Equal("insufficient"))
			Expect(res.OverallFatigueScore).To(BeNil())
			Expect(res.Muscles).To(BeNil())
		})

		It("sets evidence_status=partial when valid > 0 but excluded or unresolved > 0", func() {
			records := []fatigue.SessionLoadRecord{
				{
					SessionID:   "sess-1",
					CreatedAt:   time.Now().Add(-2 * time.Hour),
					MuscleLoads: map[string]float64{fatigue.GroupShouldersPush: 40.0},
				},
			}
			evidence := fatigue.EvidenceCounts{
				TotalSessions:          2,
				ValidSessions:          1,
				ExcludedSessions:       1,
				UnresolvedTimeSessions: 1,
			}
			res := fatigue.ComputeCurrentReadiness(records, evidence, time.Now())
			Expect(res.EvidenceStatus).To(Equal("partial"))
			Expect(res.OverallFatigueScore).NotTo(BeNil())
			Expect(res.Muscles).NotTo(BeNil())
		})
	})

	Context("FinalizePreWODAdvice", func() {
		It("strictly nullifies TargetRPE on non-complete evidence even if model injected RPE 9", func() {
			readiness := fatigue.ProfileReadinessState{
				EvidenceStatus: "partial",
				Evidence: fatigue.EvidenceCounts{
					TotalSessions:    2,
					ValidSessions:    1,
					ExcludedSessions: 1,
				},
				AsOf: time.Now(),
			}
			resp := fatigue.PreWODAdviceResponse{
				TargetRPE: &fatigue.TargetRPEInfo{
					Score:          9,
					Label:          "RPE 9 (최대 수행 도전)",
					PacingStrategy: "목표 기록 경신에 도전하세요.",
				},
				ScalingAdvice: []fatigue.ScalingAdviceItem{
					{
						Movement:       "Snatch",
						Recommendation: "최대 강도로 기록 경신",
						Detail:         "기록 경신을 위해 전력을 다하세요",
					},
				},
			}

			fatigue.FinalizePreWODAdvice(&resp, readiness)

			Expect(resp.TargetRPE).To(BeNil())
			Expect(resp.AdviceCode).To(Equal("partial_history"))
			Expect(resp.OverallSummary).To(ContainSubstring("일부 확인된 운동 이력"))
			Expect(resp.ScalingAdvice[0].Recommendation).NotTo(ContainSubstring("기록 경신"))
			Expect(resp.ScalingAdvice[0].Detail).NotTo(ContainSubstring("기록 경신"))
		})

		It("forces server-calculated muscle readiness scores over hallucinated model scores", func() {
			readiness := fatigue.ProfileReadinessState{
				EvidenceStatus: "complete",
				Muscles: map[string]fatigue.MuscleReadinessStatus{
					fatigue.GroupShouldersPush: {
						Group:        fatigue.GroupShouldersPush,
						NameKO:       "어깨 / 상체 밀기",
						FatigueScore: 25,
						State:        "fresh",
						StateKO:      "신선",
					},
				},
				AsOf: time.Now(),
			}
			resp := fatigue.PreWODAdviceResponse{
				MuscleReadiness: []fatigue.MuscleReadinessItem{
					{
						Group:        fatigue.GroupShouldersPush,
						FatigueScore: 99, // Injected fake score
						State:        "exhausted",
						StateKO:      "탈진",
						Note:         "어깨 피로 심각",
					},
				},
				TargetRPE: &fatigue.TargetRPEInfo{
					Score: 8,
				},
			}

			fatigue.FinalizePreWODAdvice(&resp, readiness)

			Expect(resp.MuscleReadiness).To(HaveLen(len(readiness.Muscles)))
			for _, m := range resp.MuscleReadiness {
				if m.Group == fatigue.GroupShouldersPush {
					Expect(m.FatigueScore).To(Equal(25))
					Expect(m.State).To(Equal("fresh"))
					Expect(m.StateKO).To(Equal("신선"))
					Expect(m.Note).To(Equal("어깨 피로 심각"))
				}
			}
		})

		It("nullifies scores, muscles, and scaling advice on insufficient evidence", func() {
			readiness := fatigue.ProfileReadinessState{
				EvidenceStatus: "insufficient",
				AsOf:           time.Now(),
			}
			effort := 8
			resp := fatigue.PreWODAdviceResponse{
				TargetRPE:              &fatigue.TargetRPEInfo{Score: 9},
				EffortReflectionReason: "직전 운동을 반영했습니다.",
				MuscleReadiness: []fatigue.MuscleReadinessItem{
					{Group: "shoulders_push", FatigueScore: 80},
				},
				ScalingAdvice: []fatigue.ScalingAdviceItem{
					{Movement: "Snatch", Recommendation: "최대 강도"},
				},
			}

			fatigue.FinalizePreWODAdvice(&resp, readiness, fatigue.RecentWorkoutSummary{
				SessionID: "WOD-20261024-01TEST",
				WorkoutAt: time.Now().Add(-12 * time.Hour),
				Movements: []string{"Snatch"},
				Effort:    &effort,
			})

			Expect(resp.OverallFatigueScore).To(BeNil())
			Expect(resp.TargetRPE).To(BeNil())
			Expect(resp.EffortReflection).To(BeNil())
			Expect(resp.EffortReflectionReason).To(BeEmpty())
			Expect(resp.MuscleReadiness).To(BeNil())
			Expect(resp.ScalingAdvice).To(BeEmpty())
			Expect(resp.AdviceCode).To(Equal("check_condition"))
			Expect(resp.OverallSummary).To(ContainSubstring("최근 운동 근거만으로"))
		})

		It("caps TargetRPE at 6 when OverallFatigueScore >= 60 despite low Effort or low hybrid sensor score, and sanitizes RIR/recovery claims", func() {
			highScore := 68
			readiness := fatigue.ProfileReadinessState{
				EvidenceStatus:      "complete",
				OverallFatigueScore: &highScore,
				OverallState:        "fatigued",
				OverallStateKO:      "피로 주의",
				Muscles: map[string]fatigue.MuscleReadinessStatus{
					fatigue.GroupPosteriorChain: {
						Group:        fatigue.GroupPosteriorChain,
						NameKO:       "허리 / 후면사슬",
						FatigueScore: 82,
						State:        "exhausted",
						StateKO:      "극심한 피로",
					},
				},
				AsOf: time.Now(),
			}
			lowEffort := 3
			dur := 720
			heart := 0.50
			motion := 0.10
			resp := fatigue.PreWODAdviceResponse{
				TargetRPE: &fatigue.TargetRPEInfo{
					Score:          9,
					Label:          "RPE 9 (최대 수행 도전)",
					PacingStrategy: "완전히 회복되었으므로 최대 강도로 도전하세요.",
				},
				EffortReflectionReason: "RIR 7 수준으로 완전히 회복되었으므로 쉬운 근력 운동으로 판단해 고강도를 추천합니다.",
			}

			fatigue.FinalizePreWODAdvice(&resp, readiness, fatigue.RecentWorkoutSummary{
				SessionID:       "WOD-20261024-01HEAVY",
				WorkoutAt:       time.Now().Add(-16 * time.Hour),
				Movements:       []string{"Deadlift", "Strict Press"},
				DurationSeconds: &dur,
				Effort:          &lowEffort,
				HybridSensor: &fatigue.HybridSensorBrief{
					HeartIntensity:  &heart,
					MotionIntensity: &motion,
					HybridIntensity: 40.0,
					DataCoverage:    0.95,
				},
			})

			Expect(resp.TargetRPE).NotTo(BeNil())
			Expect(resp.TargetRPE.Score).To(Equal(6))
			Expect(resp.TargetRPE.PacingStrategy).NotTo(ContainSubstring("완전히 회복되었"))
			Expect(resp.TargetRPE.PacingStrategy).NotTo(ContainSubstring("최대 강도로 도전"))
			Expect(resp.EffortReflectionReason).To(BeEmpty())
			Expect(resp.EffortReflection).NotTo(BeNil())
			Expect(resp.EffortReflection.Applied).To(BeTrue())
			Expect(resp.EffortReflection.RatedCount).To(Equal(1))
			Expect(resp.EffortReflection.SensorCount).To(Equal(1))
			Expect(resp.EffortReflection.Reason).NotTo(ContainSubstring("RIR"))
			Expect(resp.EffortReflection.Reason).NotTo(ContainSubstring("완전히 회복"))
			Expect(resp.EffortReflection.Reason).To(ContainSubstring("회복 완료로 단정하지 않고"))
		})

		It("discards hallucinated reflection reason when recent workouts have neither Effort nor verified sensor summary", func() {
			score := 35
			readiness := fatigue.ProfileReadinessState{
				EvidenceStatus:      "complete",
				OverallFatigueScore: &score,
				OverallState:        "moderate",
				OverallStateKO:      "보통",
				AsOf:                time.Now(),
			}
			dur := 510
			resp := fatigue.PreWODAdviceResponse{
				TargetRPE:              &fatigue.TargetRPEInfo{Score: 8, Label: "RPE 8", PacingStrategy: "일정한 페이스 유지"},
				EffortReflectionReason: "지난 운동의 Effort를 추측해 반영했습니다.",
			}

			fatigue.FinalizePreWODAdvice(&resp, readiness, fatigue.RecentWorkoutSummary{
				SessionID:       "WOD-20261024-01UNRATED",
				WorkoutAt:       time.Now().Add(-20 * time.Hour),
				Movements:       []string{"Thruster", "Pull-up"},
				DurationSeconds: &dur,
			})

			Expect(resp.EffortReflection).NotTo(BeNil())
			Expect(resp.EffortReflection.Applied).To(BeFalse())
			Expect(resp.EffortReflection.RatedCount).To(Equal(0))
			Expect(resp.EffortReflection.SensorCount).To(Equal(0))
			Expect(resp.EffortReflection.TotalRecent).To(Equal(1))
			Expect(resp.EffortReflection.Reason).To(BeEmpty())
			Expect(resp.EffortReflection.RecentWorkouts[0].Effort).To(BeNil())
		})
	})

	Context("BuildPreWODAdvicePrompt", func() {
		It("includes profile context, muscle readiness and WOD description", func() {
			score := 60
			readiness := fatigue.ProfileReadinessState{
				OverallFatigueScore: &score,
				OverallState:        "fatigued",
				OverallStateKO:      "피로 주의",
				Muscles: map[string]fatigue.MuscleReadinessStatus{
					fatigue.GroupShouldersPush: {
						Group:        fatigue.GroupShouldersPush,
						NameKO:       "어깨 / 상체 밀기",
						FatigueScore: 75,
						State:        "fatigued",
						StateKO:      "피로 주의",
					},
				},
			}
			profile := db.Profile{
				FitnessLevel: "advanced",
			}
			prompt := fatigue.BuildPreWODAdvicePrompt(readiness, profile, "Fran (21-15-9 Thrusters, Pull-ups)", []string{"Thruster", "Pull-up"}, []string{"Shoulder"})

			Expect(prompt).To(ContainSubstring("# 사용자 프로필"))
			Expect(prompt).To(ContainSubstring("advanced"))
			Expect(prompt).To(ContainSubstring("Fran (21-15-9 Thrusters, Pull-ups)"))
			Expect(prompt).To(ContainSubstring("Thruster, Pull-up"))
			Expect(prompt).To(ContainSubstring("Shoulder"))
			Expect(prompt).To(ContainSubstring("어깨 / 상체 밀기"))
		})

		It("formats recent up to 3 WODs with movements, duration, manual Effort, and separate hybrid sensor metrics", func() {
			score := 42
			readiness := fatigue.ProfileReadinessState{
				OverallFatigueScore: &score,
				OverallState:        "moderate",
				OverallStateKO:      "보통",
			}
			effort9 := 9
			dur495 := 495
			heart := 0.75
			motion := 0.80
			loadAU := 12.8
			recent := []fatigue.RecentWorkoutSummary{
				{
					SessionID:       "WOD-20261024-01A",
					WorkoutAt:       time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC),
					Movements:       []string{"Thruster", "Pull-up"},
					DurationSeconds: &dur495,
					Effort:          &effort9,
					HybridSensor: &fatigue.HybridSensorBrief{
						HeartIntensity:  &heart,
						MotionIntensity: &motion,
						HybridIntensity: 76.25,
						HybridLoadAU:    &loadAU,
						DataCoverage:    0.98,
					},
				},
				{
					SessionID: "WOD-20261022-01B",
					WorkoutAt: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
					Movements: []string{"Deadlift"},
				},
			}

			prompt := fatigue.BuildPreWODAdvicePrompt(readiness, db.Profile{FitnessLevel: "intermediate"}, "Cindy", []string{"Pull-up", "Push-up", "Air Squat"}, nil, recent...)

			Expect(prompt).To(ContainSubstring("최근 본 운동(WOD) 수행 및 강도 피드백"))
			Expect(prompt).To(ContainSubstring("종목 [Thruster, Pull-up]"))
			Expect(prompt).To(ContainSubstring("수행시간 8분 15초"))
			Expect(prompt).To(ContainSubstring("자기보고 Effort 9/10"))
			Expect(prompt).To(ContainSubstring("센서 보조지표: 하이브리드 활동강도 76/100 (심박 75 · 움직임 80 · 누적부하 12.8 AU · 커버리지 98%)"))
			Expect(prompt).To(ContainSubstring("수행시간 시간 미확정"))
			Expect(prompt).To(ContainSubstring("자기보고 Effort 미입력 (추측 금지)"))
			Expect(prompt).To(ContainSubstring("RIR"))
			Expect(prompt).To(ContainSubstring("effort_reflection_reason"))
		})
	})
})
