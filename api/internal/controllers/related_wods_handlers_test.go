package controllers_test

import (
	"testing"
	"time"

	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
)

func TestComputeRelevanceScore(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	weight60 := 60.0

	tests := []struct {
		name           string
		createdAt      time.Time
		targetWeight   *float64
		entry          db.NormalizedMovement
		minScore       float64
		maxScore       float64
		expectedMain   float64
		expectedWeight float64
	}{
		{
			name:         "recent main movement exact weight",
			createdAt:    now.Add(-1 * 24 * time.Hour), // 1 day ago
			targetWeight: &weight60,
			entry: db.NormalizedMovement{
				Movement: "power clean",
				WeightKG: &weight60,
				IsMain:   true,
			},
			minScore:       0.90,
			maxScore:       1.00,
			expectedMain:   1.0,
			expectedWeight: 1.0,
		},
		{
			name:         "older accessory movement different weight",
			createdAt:    now.Add(-60 * 24 * time.Hour), // 60 days ago
			targetWeight: &weight60,
			entry: db.NormalizedMovement{
				Movement: "power clean",
				WeightKG: floatPtr(40.0), // 20kg diff -> weightMatch = 0
				IsMain:   false,
			},
			minScore:       0.0,
			maxScore:       0.20,
			expectedMain:   0.0,
			expectedWeight: 0.0,
		},
		{
			name:         "unknown weight neutral score",
			createdAt:    now,
			targetWeight: nil,
			entry: db.NormalizedMovement{
				Movement: "power clean",
				WeightKG: nil,
				IsMain:   true,
			},
			minScore:       0.80,
			maxScore:       0.90,
			expectedMain:   1.0,
			expectedWeight: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, parts := controllers.ComputeRelevanceScore(now, tt.createdAt, tt.targetWeight, tt.entry)
			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("ComputeRelevanceScore = %v; want between %v and %v", score, tt.minScore, tt.maxScore)
			}
			if parts.Main != tt.expectedMain {
				t.Errorf("Main part = %v; want %v", parts.Main, tt.expectedMain)
			}
			if parts.Weight != tt.expectedWeight {
				t.Errorf("Weight part = %v; want %v", parts.Weight, tt.expectedWeight)
			}
		})
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
