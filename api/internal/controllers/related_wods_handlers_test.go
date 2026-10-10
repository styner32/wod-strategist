package controllers_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("ComputeRelevanceScore", func() {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	weight60 := 60.0

	DescribeTable("calculates relevance score accurately",
		func(createdAt time.Time, targetWeight *float64, entry db.NormalizedMovement, minScore, maxScore, expectedMain, expectedWeight float64) {
			score, parts := controllers.ComputeRelevanceScore(now, createdAt, targetWeight, entry)
			Expect(score).To(BeNumerically(">=", minScore))
			Expect(score).To(BeNumerically("<=", maxScore))
			Expect(parts.Main).To(Equal(expectedMain))
			Expect(parts.Weight).To(Equal(expectedWeight))
		},
		Entry("recent main movement exact weight",
			now.Add(-1*24*time.Hour), // 1 day ago
			&weight60,
			db.NormalizedMovement{
				Movement: "power clean",
				WeightKG: &weight60,
				IsMain:   true,
			},
			0.90, 1.00, 1.0, 1.0,
		),
		Entry("older accessory movement different weight",
			now.Add(-60*24*time.Hour), // 60 days ago
			&weight60,
			db.NormalizedMovement{
				Movement: "power clean",
				WeightKG: floatPtr(40.0), // 20kg diff -> weightMatch = 0
				IsMain:   false,
			},
			0.0, 0.20, 0.0, 0.0,
		),
		Entry("unknown weight neutral score",
			now,
			nil,
			db.NormalizedMovement{
				Movement: "power clean",
				WeightKG: nil,
				IsMain:   true,
			},
			0.80, 0.90, 1.0, 0.5,
		),
	)
})

func floatPtr(v float64) *float64 {
	return &v
}
