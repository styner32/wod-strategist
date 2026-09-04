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
	})

	Context("ComputeSessionMuscleLoads", func() {
		It("calculates higher load for shoulder/quad movements on Thruster chunks", func() {
			signals := []string{
				`{"movement":"Thruster","rep_count":15,"fatigue_visually_established":true}`,
				`{"movement":"Pull-up","rep_count":15,"fatigue_visually_established":false}`,
			}
			scoreJSON := `{"intensity":85,"movements":{"Thruster":{"form":80,"intensity":90},"Pull-up":{"form":85,"intensity":80}}}`
			loads := fatigue.ComputeSessionMuscleLoads(scoreJSON, signals, 160)

			Expect(loads[fatigue.GroupShouldersPush]).To(BeNumerically(">", 20.0))
			Expect(loads[fatigue.GroupUpperPullGrip]).To(BeNumerically(">", 15.0))
			Expect(loads[fatigue.GroupCardioMetabolic]).To(BeNumerically(">", 20.0))
		})

		It("falls back cleanly when chunk signals are empty", func() {
			scoreJSON := `{"intensity":70,"movements":{"Deadlift":{"form":75,"intensity":80}}}`
			loads := fatigue.ComputeSessionMuscleLoads(scoreJSON, nil, 0)

			Expect(loads[fatigue.GroupPosteriorChain]).To(BeNumerically(">", loads[fatigue.GroupQuadsSquat]))
		})
	})

	Context("ComputeCurrentReadiness with decay", func() {
		It("shows high fatigue immediately after workout, and decays over time", func() {
			now := time.Now()

			// Session 12 hours ago with heavy shoulder push load (e.g. 80.0)
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

			readiness12h := fatigue.ComputeCurrentReadiness(records, now)
			// Shoulder half-life is 30h, after 12h: 80 * 2^(-12/30) = 80 * 0.7578 ≈ 61
			Expect(readiness12h.Muscles[fatigue.GroupShouldersPush].FatigueScore).To(BeNumerically("~", 61, 3))
			Expect(readiness12h.Muscles[fatigue.GroupShouldersPush].State).To(Equal("fatigued"))

			// Same session evaluated 60 hours later
			readiness60h := fatigue.ComputeCurrentReadiness(records, now.Add(48*time.Hour))
			// After 60h (2 half-lives): 80 * (0.5)^2 = 20
			Expect(readiness60h.Muscles[fatigue.GroupShouldersPush].FatigueScore).To(BeNumerically("~", 20, 2))
			Expect(readiness60h.Muscles[fatigue.GroupShouldersPush].State).To(Equal("fresh"))
		})
	})

	Context("BuildPreWODAdvicePrompt", func() {
		It("includes profile context, muscle readiness and WOD description", func() {
			readiness := fatigue.ProfileReadinessState{
				OverallFatigueScore: 60,
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

			Expect(prompt).To(ContainSubstring("advanced"))
			Expect(prompt).To(ContainSubstring("Fran"))
			Expect(prompt).To(ContainSubstring("Thruster"))
			Expect(prompt).To(ContainSubstring("어깨 / 상체 밀기"))
			Expect(prompt).To(ContainSubstring("Shoulder"))
		})
	})
})
