package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/fatigue"
)

var _ = Describe("buildDeterministicFallbackAdvice", func() {
	moderate := 40
	readiness := fatigue.ProfileReadinessState{
		EvidenceStatus:      "complete",
		OverallFatigueScore: &moderate,
		OverallState:        "moderate",
		OverallStateKO:      "보통",
	}

	It("lowers RPE for an older high sensor session and explains that same session", func() {
		recent := []RecentWorkoutSummary{
			{SessionID: "latest", HybridSensor: &HybridSensorBrief{HybridIntensity: 55, DataCoverage: 0.9}},
			{SessionID: "older", Movements: []string{"Burpee"}, HybridSensor: &HybridSensorBrief{HybridIntensity: 85, DataCoverage: 0.9}},
		}
		resp := buildDeterministicFallbackAdvice(1, readiness, db.Profile{}, "", nil, nil, recent)

		Expect(resp.TargetRPE.Score).To(Equal(7))
		Expect(resp.TargetRPE.Label).To(Equal("RPE 7 (조절된 페이스)"))
		Expect(resp.TargetRPE.PacingStrategy).To(HavePrefix("최근 Burpee WOD에서 높은 센서 하이브리드 활동강도"))
		Expect(resp.EffortReflectionReason).To(ContainSubstring("최근 Burpee WOD의 센서 하이브리드 활동강도(85/100)"))
		Expect(resp.EffortReflectionReason).NotTo(ContainSubstring("55/100"))
	})

	It("names the low-Effort workout it cites instead of calling it the previous WOD", func() {
		seven, five := 7, 5
		recent := []RecentWorkoutSummary{
			{SessionID: "latest", Effort: &seven},
			{SessionID: "older", Effort: &five},
		}
		resp := buildDeterministicFallbackAdvice(1, readiness, db.Profile{}, "", nil, nil, recent)

		Expect(resp.TargetRPE.Score).To(Equal(8))
		Expect(resp.TargetRPE.PacingStrategy).To(HavePrefix("2회 전 WOD에서 비교적 여유 있는 체감 강도가 기록되었으므로"))
		Expect(resp.EffortReflectionReason).To(HavePrefix("2회 전 WOD에서 비교적 여유 있는 체감 강도(Effort 5/10)"))
	})
})

var _ = Describe("extractRecentWorkoutMovements", func() {
	It("lists only movements that add muscle load", func() {
		Expect(extractRecentWorkoutMovements(
			`{"movements":{"Thruster":{},"Rest":{},"Walking":{},"Setup":{},"Unknown":{}}}`, "",
		)).To(Equal([]string{"Thruster"}))
	})
})
