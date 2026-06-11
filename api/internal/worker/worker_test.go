package worker

import (
	"testing"
)

func TestValidateSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantErr   bool
	}{
		{"valid", "WOD-1234", false},
		{"unix path traversal", "WOD-1234/../", true},
		{"windows path traversal", "WOD-1234\\..\\", true},
		{"directory traversal", "WOD-1234..", true},
		{"unix separator", "WOD-1234/5678", true},
		{"windows separator", "WOD-1234\\5678", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateSessionID(tt.sessionID); (err != nil) != tt.wantErr {
				t.Errorf("validateSessionID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
