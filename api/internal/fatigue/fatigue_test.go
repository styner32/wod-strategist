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

	Context("ComputeSessionMuscleLoadsWithSensor", func() {
		It("returns (nil, false) when movements are empty or all unknown/walking/rest", func() {
			scoreJSON := `{"intensity":70,"movements":{"walking":{},"rest":{},"unknown":{}}}`
			loads, ok := fatigue.ComputeSessionMuscleLoadsWithSensor(scoreJSON, "{}")
			Expect(ok).To(BeFalse())
			Expect(loads).To(BeNil())
		})

		It("calculates loads and adds sensor HR bonus to cardio_metabolic", func() {
			scoreJSON := `{"intensity":70,"movements":{"Thruster":{},"Pull-up":{}}}`
			sensorSummary := `{"quality":{"valid_hr":true,"is_complete":true},"hr_bonus":15.0}`
			loads, ok := fatigue.ComputeSessionMuscleLoadsWithSensor(scoreJSON, sensorSummary)
			Expect(ok).To(BeTrue())
			Expect(loads).NotTo(BeNil())
			Expect(loads[fatigue.GroupShouldersPush]).To(BeNumerically(">", 0.0))
			// HR bonus adds 15.0 to cardio metabolic
			loadsNoSensor, _ := fatigue.ComputeSessionMuscleLoadsWithSensor(scoreJSON, "{}")
			Expect(loads[fatigue.GroupCardioMetabolic] - loadsNoSensor[fatigue.GroupCardioMetabolic]).To(BeNumerically("~", 15.0, 0.5))
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
			resp := fatigue.PreWODAdviceResponse{
				TargetRPE: &fatigue.TargetRPEInfo{Score: 9},
				MuscleReadiness: []fatigue.MuscleReadinessItem{
					{Group: "shoulders_push", FatigueScore: 80},
				},
				ScalingAdvice: []fatigue.ScalingAdviceItem{
					{Movement: "Snatch", Recommendation: "최대 강도"},
				},
			}

			fatigue.FinalizePreWODAdvice(&resp, readiness)

			Expect(resp.OverallFatigueScore).To(BeNil())
			Expect(resp.TargetRPE).To(BeNil())
			Expect(resp.MuscleReadiness).To(BeNil())
			Expect(resp.ScalingAdvice).To(BeEmpty())
			Expect(resp.AdviceCode).To(Equal("check_condition"))
			Expect(resp.OverallSummary).To(ContainSubstring("최근 운동 근거만으로"))
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
	})
})
