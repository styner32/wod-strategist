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

func TestPopulateSessionFatigue_SchemaV1_InsufficientEvidence(t *testing.T) {
	results := []db.AnalysisResult{
		{
			SessionID: "WOD-20260904-01EMPTY01",
			Status:    "COMPLETED",
			Output:    "No movements here",
		},
	}

	normalized := normalizeHighlightResultsForResponseWithSchema(results, 1)
	if normalized[0].SessionFatigue == nil {
		t.Fatal("expected SessionFatigue to be populated for schema v1, got nil")
	}
	if normalized[0].SessionFatigue.Status != "insufficient_evidence" {
		t.Errorf("expected status insufficient_evidence, got %s", normalized[0].SessionFatigue.Status)
	}
	if normalized[0].SessionFatigue.Guidance != nil {
		t.Errorf("expected guidance to be nil for insufficient_evidence, got %+v", normalized[0].SessionFatigue.Guidance)
	}
}

func TestPopulateSessionFatigue_SchemaV1_AvailableWithGuidance(t *testing.T) {
	results := []db.AnalysisResult{
		{
			SessionID:    "WOD-20260904-01AVAILABLE01",
			Status:       "COMPLETED",
			SessionScore: `{"intensity":85,"movements":{"Thruster":{"reps":45},"Pull-up":{"reps":30}}}`,
		},
	}

	normalized := normalizeHighlightResultsForResponseWithSchema(results, 1)
	if normalized[0].SessionFatigue == nil {
		t.Fatal("expected SessionFatigue to be populated, got nil")
	}
	fatigueRes := normalized[0].SessionFatigue
	if fatigueRes.Status != "available" {
		t.Errorf("expected status available, got %s", fatigueRes.Status)
	}
	if fatigueRes.Guidance == nil {
		t.Fatal("expected guidance to be populated, got nil")
	}
	if fatigueRes.Guidance.StateCode != fatigueRes.State {
		t.Errorf("expected guidance state %s, got %s", fatigueRes.State, fatigueRes.Guidance.StateCode)
	}
	if fatigueRes.Guidance.TextKO == "" || fatigueRes.Guidance.TextEN == "" {
		t.Errorf("expected non-empty guidance text, got KO=%q, EN=%q", fatigueRes.Guidance.TextKO, fatigueRes.Guidance.TextEN)
	}
}

func TestPopulateSessionFatigue_SensorFreshnessRejection(t *testing.T) {
	// If sensor version in summary does not match row's SensorVersion, summary must NOT be used
	results := []db.AnalysisResult{
		{
			SessionID:       "WOD-20260904-01STALE01",
			Status:          "COMPLETED",
			SessionScore:    `{"intensity":70,"movements":{"Row":{"meters":1000}}}`,
			SensorVersion:   2,
			SensorState:     db.SensorStateCompleted,
			SensorProcessing: db.JSONDocument(`{"request_id":"req-new","target_generation":"100"}`),
			SensorSummary:    db.JSONDocument(`{"version":1,"request_id":"req-old","source_generation":"99","calculation_version":1,"quality":{"valid_hr":true,"is_complete":true},"hr_bonus":15.0}`),
		},
	}

	normalized := normalizeHighlightResultsForResponseWithSchema(results, 1)
	fatigueRes := normalized[0].SessionFatigue
	if fatigueRes == nil {
		t.Fatal("expected SessionFatigue to be populated, got nil")
	}
	// Sensor was stale, so it should not be applied
	if fatigueRes.HeartRateAdjusted {
		t.Errorf("expected HeartRateAdjusted to be false due to stale sensor summary, got true")
	}
	if fatigueRes.SensorStatus != "none" {
		t.Errorf("expected SensorStatus to be none, got %s", fatigueRes.SensorStatus)
	}
}
