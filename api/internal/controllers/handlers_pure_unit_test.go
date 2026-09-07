package controllers

import (
	"testing"
)

func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"pull-up", "pull-up"},
		{"50% snatch", `50\% snatch`},
		{"dead_lift", `dead\_lift`},
		{`back\squat`, `back\\squat`},
		{`100%_clean\jerk`, `100\%\_clean\\jerk`},
		{"", ""},
	}

	for _, tt := range tests {
		got := escapeLikePattern(tt.input)
		if got != tt.expected {
			t.Errorf("escapeLikePattern(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
