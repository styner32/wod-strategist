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
		{"Clean & Jerk", "clean & jerk"},
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
