package controllers

import (
	"testing"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/fatigue"
)

func TestPopulateSessionFatigue_WithSessionScore(t *testing.T) {
	results := []db.AnalysisResult{
		{
			SessionID:    "WOD-20260904-01TESTSESSION01",
			Status:       "COMPLETED",
			SessionScore: `{"intensity":85,"movements":{"Thruster":{"reps":45},"Pull-up":{"reps":30}}}`,
		},
	}

	normalized := normalizeHighlightResultsForResponse(results)
	if len(normalized) != 1 {
		t.Fatalf("expected 1 result, got %d", len(normalized))
	}

	fatigueRes := normalized[0].SessionFatigue
	if fatigueRes == nil {
		t.Fatal("expected SessionFatigue to be populated, got nil")
	}

	if fatigueRes.OverallScore <= 0 || fatigueRes.OverallScore > 100 {
		t.Errorf("expected OverallScore between 1 and 100, got %d", fatigueRes.OverallScore)
	}
	if fatigueRes.State == "" || fatigueRes.StateKO == "" {
		t.Errorf("expected non-empty State and StateKO, got State=%q, StateKO=%q", fatigueRes.State, fatigueRes.StateKO)
	}

	for _, g := range fatigue.AllMuscleGroups {
		score, ok := fatigueRes.Muscles[g]
		if !ok {
			t.Errorf("expected muscle group %q in Muscles map", g)
		}
		if score < 0 || score > 100 {
			t.Errorf("expected muscle %q score between 0 and 100, got %d", g, score)
		}
	}

	// Thrusters + Pull-ups should heavily load shoulders and quads
	if fatigueRes.Muscles[fatigue.GroupShouldersPush] <= 0 {
		t.Errorf("expected shoulders_push load > 0, got %d", fatigueRes.Muscles[fatigue.GroupShouldersPush])
	}
	if fatigueRes.Muscles[fatigue.GroupQuadsSquat] <= 0 {
		t.Errorf("expected quads_squat load > 0, got %d", fatigueRes.Muscles[fatigue.GroupQuadsSquat])
	}
}

func TestPopulateSessionFatigue_FromOutputFallback(t *testing.T) {
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
	if normalized[0].SessionFatigue == nil {
		t.Fatal("expected SessionFatigue to be extracted from Output, got nil")
	}

	fatigueRes := normalized[0].SessionFatigue
	if fatigueRes.Muscles[fatigue.GroupPosteriorChain] <= 0 {
		t.Errorf("expected deadlift to load posterior_chain, got %d", fatigueRes.Muscles[fatigue.GroupPosteriorChain])
	}
}

func TestPopulateSessionFatigue_IncompleteOrEmpty(t *testing.T) {
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
	for i, res := range normalized {
		if res.SessionFatigue != nil {
			t.Errorf("result %d (%s) expected SessionFatigue to be nil, got %+v", i, res.SessionID, res.SessionFatigue)
		}
	}
}
