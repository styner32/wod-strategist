package cost_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/cost"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("Cost calculation", func() {
	Describe("GetModelRate", func() {
		It("returns correct pricing for gemini-3.8-flash", func() {
			rate := cost.GetModelRate("gemini-3.8-flash")
			Expect(rate.PromptPerMillion).To(Equal(1.50))
			Expect(rate.CandidatePerMillion).To(Equal(9.00))
		})

		It("returns correct pricing for gemini-3.5-flash-lite", func() {
			rate := cost.GetModelRate("gemini-3.5-flash-lite")
			Expect(rate.PromptPerMillion).To(Equal(0.25))
			Expect(rate.CandidatePerMillion).To(Equal(1.50))
		})

		It("returns correct pricing for gemini-3.1-pro-preview", func() {
			rate := cost.GetModelRate("gemini-3.1-pro-preview")
			Expect(rate.PromptPerMillion).To(Equal(1.25))
			Expect(rate.CandidatePerMillion).To(Equal(5.00))
		})

		It("returns default rate for unknown models", func() {
			rate := cost.GetModelRate("unknown-future-model")
			Expect(rate.PromptPerMillion).To(Equal(1.50))
			Expect(rate.CandidatePerMillion).To(Equal(9.00))
		})
	})

	Describe("CalculateTokensCost", func() {
		It("calculates costs accurately for Flash 3.8", func() {
			// 1,000,000 prompt tokens ($1.50) + 1,000,000 candidate tokens ($9.00) = $10.50
			usd, krw := cost.CalculateTokensCost("gemini-3.8-flash", 1000000, 1000000)
			Expect(usd).To(Equal(10.50))
			Expect(krw).To(Equal(10.50 * 1380.0))
		})

		It("calculates fractional token costs with proper rounding", func() {
			// 10,000 prompt: (10,000 / 1,000,000) * 1.50 = 0.015
			// 5,000 candidate: (5,000 / 1,000,000) * 9.00 = 0.045
			// total: 0.06 USD
			usd, krw := cost.CalculateTokensCost("gemini-3.8-flash", 10000, 5000)
			Expect(usd).To(Equal(0.06))
			Expect(krw).To(Equal(0.06 * 1380.0))
		})

		It("handles 0 tokens correctly", func() {
			usd, krw := cost.CalculateTokensCost("gemini-3.8-flash", 0, 0)
			Expect(usd).To(Equal(0.0))
			Expect(krw).To(Equal(0.0))
		})
	})

	Describe("CalculateSessionCost", func() {
		It("aggregates usage and provides breakdown by task and model", func() {
			usages := []db.TokenUsage{
				{
					TaskType:        "video:index",
					Model:           "gemini-3.8-flash",
					PromptTokens:    100000,
					CandidateTokens: 10000,
					TotalTokens:     110000,
				},
				{
					TaskType:        "video:segment",
					Model:           "gemini-3.8-flash",
					PromptTokens:    200000,
					CandidateTokens: 20000,
					TotalTokens:     220000,
				},
				{
					TaskType:        "video:segment",
					Model:           "gemini-3.1-pro-preview",
					PromptTokens:    50000,
					CandidateTokens: 5000,
					TotalTokens:     55000,
				},
			}

			resp := cost.CalculateSessionCost("WOD-20260407-01JQXYZ", usages)
			Expect(resp.SessionID).To(Equal("WOD-20260407-01JQXYZ"))
			Expect(resp.PromptTokens).To(Equal(int64(350000)))
			Expect(resp.CandidateTokens).To(Equal(int64(35000)))
			Expect(resp.TotalTokens).To(Equal(int64(385000)))

			// Check breakdowns
			Expect(resp.ByTaskType).To(HaveLen(2))
			Expect(resp.ByTaskType[0].Key).To(Equal("video:index"))
			Expect(resp.ByTaskType[0].PromptTokens).To(Equal(int64(100000)))
			Expect(resp.ByTaskType[1].Key).To(Equal("video:segment"))
			Expect(resp.ByTaskType[1].PromptTokens).To(Equal(int64(250000)))

			Expect(resp.ByModel).To(HaveLen(2))
			Expect(resp.ByModel[0].Key).To(Equal("gemini-3.8-flash"))
			Expect(resp.ByModel[0].PromptTokens).To(Equal(int64(300000)))
			Expect(resp.ByModel[1].Key).To(Equal("gemini-3.1-pro-preview"))
			Expect(resp.ByModel[1].PromptTokens).To(Equal(int64(50000)))

			Expect(resp.CostUSD).To(BeNumerically(">", 0))
			Expect(resp.CostKRW).To(Equal(cost.RoundKRW(resp.CostUSD * 1380.0)))
		})

		It("handles empty usages gracefully", func() {
			resp := cost.CalculateSessionCost("empty-session", nil)
			Expect(resp.SessionID).To(Equal("empty-session"))
			Expect(resp.TotalTokens).To(Equal(int64(0)))
			Expect(resp.CostUSD).To(Equal(0.0))
			Expect(resp.CostKRW).To(Equal(0.0))
			Expect(resp.ByTaskType).To(BeEmpty())
			Expect(resp.ByModel).To(BeEmpty())
		})
	})

	Describe("CalculateTotalCost and CalculateTotalCostFromAggregates", func() {
		It("calculates cumulative totals correctly", func() {
			aggregates := []cost.ModelTokenAggregate{
				{
					Model:           "gemini-3.8-flash",
					PromptTokens:    1000000,
					CandidateTokens: 1000000,
					TotalTokens:     2000000,
				},
				{
					Model:           "gemini-3.5-flash-lite",
					PromptTokens:    1000000,
					CandidateTokens: 1000000,
					TotalTokens:     2000000,
				},
			}

			resp := cost.CalculateTotalCostFromAggregates(aggregates)
			Expect(resp.PromptTokens).To(Equal(int64(2000000)))
			Expect(resp.CandidateTokens).To(Equal(int64(2000000)))
			Expect(resp.TotalTokens).To(Equal(int64(4000000)))
			// Flash 3.8 ($10.50) + Flash 3.5 Lite ($1.75) = $12.25
			Expect(resp.CostUSD).To(Equal(12.25))
			Expect(resp.CostKRW).To(Equal(12.25 * 1380.0))
		})
	})
})
