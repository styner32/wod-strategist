package movement_test

import (
	"testing"

	"github.com/wod-strategist/api/internal/movement"
)

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Power Clean", "power clean"},
		{" Power-Clean ", "power clean"},
		{"power   clean", "power clean"},
		{"Pull-up", "pull up"},
		{"  Double-under  ", "double under"},
		{"", ""},
		{"   ", ""},
		{"Clean & Jerk", "clean and jerk"},
		{"Clean and Jerk", "clean and jerk"},
		{"Clean&Jerk", "clean and jerk"},
	}

	for _, tt := range tests {
		got := movement.NormalizeKey(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeKey(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestAll(t *testing.T) {
	all := movement.All()
	if len(all) == 0 {
		t.Fatal("expected non-empty movement list")
	}
	if all[0] != "Power Snatch" {
		t.Errorf("expected first movement to be Power Snatch, got %q", all[0])
	}
}

func TestCanonical(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Exact canonical
		{"Deadlift", "Deadlift"},
		{"deadlift", "Deadlift"},
		{"Double-under", "Double-under"},
		{"Row", "Row"},

		// Aliases
		{"Rowing", "Row"},
		{"rowing", "Row"},
		{"rower", "Row"},
		{"running", "Run"},
		{"Double Unders", "Double-under"},
		{"Double-Unders", "Double-under"},
		{"double under", "Double-under"},
		{"du", "Double-under"},
		{"dus", "Double-under"},
		{"Single Unders", "Single-under"},
		{"su", "Single-under"},
		{"c&j", "Clean & Jerk"},
		{"Clean and Jerk", "Clean & Jerk"},
		{"hspu", "Handstand Push-up"},
		{"pullup", "Pull-up"},
		{"pullups", "Pull-up"},
		{"pushup", "Push-up"},
		{"situp", "Sit-up"},
		{"wall ball", "Wallball Shot"},
		{"kettlebell swing", "KB Swing"},
		{"farmers carry", "Farmer's Carry"},

		// Plural fallbacks
		{"Deadlifts", "Deadlift"},
		{"deadlifts", "Deadlift"},
		{"Push Presses", "Push Press"},
		{"Burpees", "Burpee"},
		{"Air Squats", "Air Squat"},
		{"Box Jump Overs", "Box Jump Over"},
		{"Burpee Box Jump Overs", "Burpee Box Jump Over"},

		// Unknown / empty
		{"", ""},
		{"   ", ""},
		{"Invented Squat", ""},
		{"Some Random Unknown Movement", ""},
	}

	for _, tt := range tests {
		got := movement.Canonical(tt.input)
		if got != tt.expected {
			t.Errorf("Canonical(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
