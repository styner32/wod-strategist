package server

import (
	"testing"
)

func TestValidateMovements(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		want      bool
	}{
		{
			name:      "Valid movement",
			requested: []string{"Burpee"},
			want:      true,
		},
		{
			name:      "Invalid movement",
			requested: []string{"InvalidMove"},
			want:      false,
		},
		{
			name:      "Mixed valid and invalid (should be false)",
			requested: []string{"InvalidMove", "Burpee"},
			want:      false,
		},
		{
			name:      "Empty request",
			requested: []string{},
			want:      true,
		},
		{
			name:      "Multiple valid",
			requested: []string{"Burpee", "Row"},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateMovements(tt.requested); got != tt.want {
				t.Errorf("validateMovements() = %v, want %v", got, tt.want)
			}
		})
	}
}
