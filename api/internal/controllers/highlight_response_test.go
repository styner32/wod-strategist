package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/fatigue"
)

var _ = Describe("normalizeHighlightResultsForResponse session fatigue", func() {
	It("populates session fatigue with session score", func() {
		results := []db.AnalysisResult{
			{
				SessionID:    "WOD-20260904-01TESTSESSION01",
				Status:       "COMPLETED",
				SessionScore: `{"intensity":85,"movements":{"Thruster":{"reps":45},"Pull-up":{"reps":30}}}`,
			},
		}

		normalized := normalizeHighlightResultsForResponse(results)
		Expect(normalized).To(HaveLen(1))

		fatigueRes := normalized[0].SessionFatigue
		Expect(fatigueRes).NotTo(BeNil())

		Expect(fatigueRes.OverallScore).To(BeNumerically(">", 0))
		Expect(fatigueRes.OverallScore).To(BeNumerically("<=", 100))
		Expect(fatigueRes.State).NotTo(BeEmpty())
		Expect(fatigueRes.StateKO).NotTo(BeEmpty())

		for _, g := range fatigue.AllMuscleGroups {
			score, ok := fatigueRes.Muscles[g]
			Expect(ok).To(BeTrue(), "expected muscle group %q in Muscles map", g)
			Expect(score).To(BeNumerically(">=", 0))
			Expect(score).To(BeNumerically("<=", 100))
		}

		// Thrusters + Pull-ups should heavily load shoulders and quads
		Expect(fatigueRes.Muscles[fatigue.GroupShouldersPush]).To(BeNumerically(">", 0))
		Expect(fatigueRes.Muscles[fatigue.GroupQuadsSquat]).To(BeNumerically(">", 0))
	})

	It("falls back to output when session score is empty", func() {
		results := []db.AnalysisResult{
			{
				SessionID:    "WOD-20260904-01FALLBACK01",
				Status:       "COMPLETED",
				SessionScore: "",
				Output: "Analysis report.\n```score\n" +
					`{"overall":80,"form":75,"intensity":80,"consistency":70,"movements":{"Deadlift":{"form":75,"intensity":80}}}` +
					"\n```\n",
			},
		}

		normalized := normalizeHighlightResultsForResponse(results)
		Expect(normalized[0].SessionFatigue).NotTo(BeNil())

		fatigueRes := normalized[0].SessionFatigue
		Expect(fatigueRes.Muscles[fatigue.GroupPosteriorChain]).To(BeNumerically(">", 0))
	})

	It("leaves session fatigue nil for incomplete or empty results", func() {
		results := []db.AnalysisResult{
			{
				SessionID: "WOD-20260904-01FAILED01",
				Status:    "FAILED",
				Output:    "Video failed",
			},
			{
				SessionID: "WOD-20260904-01PENDING01",
				Status:    "PENDING",
			},
			{
				SessionID: "WOD-20260904-01EMPTY01",
				Status:    "COMPLETED",
				Output:    "No score here",
			},
		}

		normalized := normalizeHighlightResultsForResponse(results)
		for _, res := range normalized {
			Expect(res.SessionFatigue).To(BeNil(), "result %s expected SessionFatigue to be nil", res.SessionID)
		}
	})

	It("marks schema v1 status as insufficient_evidence when movements are absent", func() {
		results := []db.AnalysisResult{
			{
				SessionID: "WOD-20260904-01EMPTY01",
				Status:    "COMPLETED",
				Output:    "No movements here",
			},
		}

		normalized := normalizeHighlightResultsForResponseWithSchema(results, 1)
		Expect(normalized[0].SessionFatigue).NotTo(BeNil())
		Expect(normalized[0].SessionFatigue.Status).To(Equal("insufficient_evidence"))
		Expect(normalized[0].SessionFatigue.Guidance).To(BeNil())
	})

	It("populates schema v1 guidance when available", func() {
		results := []db.AnalysisResult{
			{
				SessionID:    "WOD-20260904-01AVAILABLE01",
				Status:       "COMPLETED",
				SessionScore: `{"intensity":85,"movements":{"Thruster":{"reps":45},"Pull-up":{"reps":30}}}`,
			},
		}

		normalized := normalizeHighlightResultsForResponseWithSchema(results, 1)
		Expect(normalized[0].SessionFatigue).NotTo(BeNil())
		fatigueRes := normalized[0].SessionFatigue
		Expect(fatigueRes.Status).To(Equal("available"))
		Expect(fatigueRes.Guidance).NotTo(BeNil())
		Expect(fatigueRes.Guidance.StateCode).To(Equal(fatigueRes.State))
		Expect(fatigueRes.Guidance.TextKO).NotTo(BeEmpty())
		Expect(fatigueRes.Guidance.TextEN).NotTo(BeEmpty())
	})

	It("rejects stale sensor summary", func() {
		// If sensor version in summary does not match row's SensorVersion, summary must NOT be used
		results := []db.AnalysisResult{
			{
				SessionID:        "WOD-20260904-01STALE01",
				Status:           "COMPLETED",
				SessionScore:     `{"intensity":70,"movements":{"Row":{"meters":1000}}}`,
				SensorVersion:    2,
				SensorState:      db.SensorStateCompleted,
				SensorProcessing: db.JSONDocument(`{"request_id":"req-new","target_generation":"100"}`),
				SensorSummary:    db.JSONDocument(`{"version":1,"request_id":"req-old","source_generation":"99","calculation_version":1,"quality":{"valid_hr":true,"is_complete":true},"hr_bonus":15.0}`),
			},
		}

		normalized := normalizeHighlightResultsForResponseWithSchema(results, 1)
		fatigueRes := normalized[0].SessionFatigue
		Expect(fatigueRes).NotTo(BeNil())
		Expect(fatigueRes.HeartRateAdjusted).To(BeFalse())
		Expect(fatigueRes.SensorStatus).To(Equal("none"))
	})

	It("applies cardiovascular floor under high heart rate", func() {
		results := []db.AnalysisResult{
			{
				SessionID:        "WOD-20261007-01M4A0BYQYDW8KGMMCSK4ZFJZB",
				Status:           "COMPLETED",
				SessionScore:     `{"intensity":70,"movements":{"Power Clean":{"reps":52}}}`,
				SensorVersion:    1,
				SensorState:      db.SensorStateCompleted,
				SensorProcessing: db.JSONDocument(`{"request_id":"req-hr","target_generation":"1"}`),
				SensorSummary: db.JSONDocument(`{
					"version": 1,
					"request_id": "req-hr",
					"source_generation": "1",
					"calculation_version": 1,
					"quality": {"valid_hr": true, "is_complete": true},
					"metrics": {
						"hr": {
							"valid_hr": true,
							"weighted_mean_bpm": 158.0,
							"peak_bpm": 170,
							"zones": {
								"z4_ratio": 0.35,
								"z5_ratio": 0.24
							}
						}
					}
				}`),
			},
		}

		normalized := normalizeHighlightResultsForResponseWithSchema(results, 1)
		fatigueRes := normalized[0].SessionFatigue
		Expect(fatigueRes).NotTo(BeNil())
		Expect(fatigueRes.HeartRateAdjusted).To(BeTrue())
		Expect(fatigueRes.OverallScore).To(BeNumerically(">=", 40))
		Expect(fatigueRes.State).NotTo(Equal("fresh"))
	})
})
