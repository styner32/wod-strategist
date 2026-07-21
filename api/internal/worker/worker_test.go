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
		{"valid current", "WOD-20240101-ABCD1234", false},
		{"valid legacy", "P12-WOD-2026-04-01-14-30", false},
		{"valid legacy no profile", "WOD-2026-03-30-10-34", false},
		{"valid test pattern", "session-hints", false},
		{"valid test pattern split", "split-media-session", false},
		{"invalid format no dashes", "WOD20240101ABCD1234", true},
		{"path traversal unix", "../etc/passwd", true},
		{"path traversal windows", "..\\Windows\\System32", true},
		{"path traversal mixed", "../Windows\\System32", true},
		{"contains slash", "WOD/20240101", true},
		{"contains backslash", "WOD\\20240101", true},
		{"contains dot", "WOD.20240101", true},
		{"only dot", ".", true},
		{"only dot-dot", "..", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSessionID(tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSessionID(%q) error = %v, wantErr %v", tt.sessionID, err, tt.wantErr)
			}
		})
	}
}
