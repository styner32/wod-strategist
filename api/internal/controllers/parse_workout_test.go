package controllers

import (
	"testing"
)

func TestParseWorkoutBlock_Valid(t *testing.T) {
	input := "Here is the workout:\n```workout\n" +
		`{"wod_description":"Fran","movements":["Thruster","Pull-up"],"raw_text":"FRAN\n21-15-9\nThrusters 43kg\nPull-ups"}` +
		"\n```\n"

	resp, err := parseWorkoutBlock(input)
	if err != nil {
		t.Fatalf("parseWorkoutBlock failed: %v", err)
	}

	if resp.WODDescription != "Fran" {
		t.Errorf("expected WODDescription 'Fran', got %q", resp.WODDescription)
	}
	if len(resp.Movements) != 2 {
		t.Fatalf("expected 2 movements, got %d", len(resp.Movements))
	}
	if resp.Movements[0] != "Thruster" {
		t.Errorf("expected first movement 'Thruster', got %q", resp.Movements[0])
	}
	if resp.Movements[1] != "Pull-up" {
		t.Errorf("expected second movement 'Pull-up', got %q", resp.Movements[1])
	}
	if resp.RawText == "" {
		t.Error("expected non-empty RawText")
	}
}

func TestParseWorkoutBlock_NoBlock(t *testing.T) {
	input := "I cannot read the whiteboard clearly."

	_, err := parseWorkoutBlock(input)
	if err == nil {
		t.Error("expected error when no workout block present")
	}
}

func TestParseWorkoutBlock_InvalidJSON(t *testing.T) {
	input := "```workout\nnot valid json\n```\n"

	_, err := parseWorkoutBlock(input)
	if err == nil {
		t.Error("expected error for invalid JSON in workout block")
	}
}

func TestParseWorkoutBlock_EmptyWorkout(t *testing.T) {
	input := "```workout\n" +
		`{"wod_description":"","movements":[],"raw_text":""}` +
		"\n```\n"

	resp, err := parseWorkoutBlock(input)
	if err != nil {
		t.Fatalf("parseWorkoutBlock failed: %v", err)
	}

	if resp.WODDescription != "" {
		t.Errorf("expected empty WODDescription, got %q", resp.WODDescription)
	}
	if len(resp.Movements) != 0 {
		t.Errorf("expected 0 movements, got %d", len(resp.Movements))
	}
}

func TestParseWorkoutBlock_ForTimeWorkout(t *testing.T) {
	input := "```workout\n" +
		`{"wod_description":"For Time: 5 rounds of 10 Deadlifts (60kg) + 15 Box Jumps (24in)","movements":["Deadlift","Box Jump"],"raw_text":"FOR TIME\n5RDS\n10 DL 60kg\n15 BJ 24\""}` +
		"\n```\n"

	resp, err := parseWorkoutBlock(input)
	if err != nil {
		t.Fatalf("parseWorkoutBlock failed: %v", err)
	}

	if resp.WODDescription != "For Time: 5 rounds of 10 Deadlifts (60kg) + 15 Box Jumps (24in)" {
		t.Errorf("unexpected WODDescription: %q", resp.WODDescription)
	}
	if len(resp.Movements) != 2 {
		t.Fatalf("expected 2 movements, got %d", len(resp.Movements))
	}
}

func TestParseWorkoutBlock_AMRAPWorkout(t *testing.T) {
	input := "```workout\n" +
		`{"wod_description":"AMRAP 20: 5 Pull-ups, 10 Push-ups, 15 Air Squats","movements":["Pull-up","Push-up","Air Squat"],"raw_text":"AMRAP 20분\n5 풀업\n10 푸쉬업\n15 에어스쿼트"}` +
		"\n```\n"

	resp, err := parseWorkoutBlock(input)
	if err != nil {
		t.Fatalf("parseWorkoutBlock failed: %v", err)
	}

	if len(resp.Movements) != 3 {
		t.Errorf("expected 3 movements, got %d", len(resp.Movements))
	}
}
