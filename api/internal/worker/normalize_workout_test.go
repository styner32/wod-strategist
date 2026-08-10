package worker

import (
	"testing"
)

func TestParseNormalizedWorkoutOutput(t *testing.T) {
	raw := "Here is the parsed result:\n\n```movements\n{\n  \"movements\": [\n    {\n      \"movement\": \"power clean\",\n      \"weight_raw\": \"135lb\",\n      \"weight_kg\": 61.2,\n      \"reps\": \"5\",\n      \"is_main\": true\n    }\n  ]\n}\n```\nHope that helps!"

	movements, err := ParseNormalizedWorkoutOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(movements) != 1 {
		t.Fatalf("expected 1 movement, got %d", len(movements))
	}

	m := movements[0]
	if m.Movement != "power clean" {
		t.Errorf("expected movement 'power clean', got %q", m.Movement)
	}
	if m.WeightRaw != "135lb" {
		t.Errorf("expected weight_raw '135lb', got %q", m.WeightRaw)
	}
	if m.WeightKG == nil || *m.WeightKG != 61.2 {
		t.Errorf("expected weight_kg 61.2, got %v", m.WeightKG)
	}
	if m.Reps != "5" {
		t.Errorf("expected reps '5', got %q", m.Reps)
	}
	if !m.IsMain {
		t.Errorf("expected is_main true, got false")
	}
}

func TestBuildNormalizedWorkoutPrompt(t *testing.T) {
	prompt := BuildNormalizedWorkoutPrompt("5x5 Power Clean 135lb", []string{"Power Clean"}, "male")
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
}
