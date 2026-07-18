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
		{"valid", "WOD-20240101-ABCD1234", false},
		{"path traversal unix", "../etc/passwd", true},
		{"path traversal windows", "..\\Windows\\System32", true},
		{"path traversal mixed", "../Windows\\System32", true},
		{"contains slash", "WOD/20240101", true},
		{"contains backslash", "WOD\\20240101", true},
		{"current dir", ".", true},
		{"parent dir", "..", true},
		{"empty string", "", true},
		{"root dir", "/", true},
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
