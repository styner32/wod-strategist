package controllers

import (
	"testing"
)

func TestSanitizeAppearanceValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{input: "  Red Nike Metcon  ", expected: "Red Nike Metcon"},
		{input: "Line1\nLine2`backtick`", expected: "Line1 Line2backtick"},
	}

	for _, tt := range tests {
		got := sanitizeAppearanceValue(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeAppearanceValue(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeAppearance(t *testing.T) {
	in := &AppearanceInput{
		Appearance: "  Black t-shirt, grey shorts\nred shoes  ",
	}

	out := normalizeAppearance(in)

	if out.Appearance != "Black t-shirt, grey shorts red shoes" {
		t.Errorf("Expected sanitized appearance string, got %s", out.Appearance)
	}
}
