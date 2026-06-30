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
		{"valid session ID", "WOD-20231012-ABCDEF", false},
		{"valid session ID with underscores", "WOD_20231012_ABCDEF", false},
		{"invalid session ID with unix separator", "WOD-20231012/ABCDEF", true},
		{"invalid session ID with windows separator", "WOD-20231012\\ABCDEF", true},
		{"invalid session ID with dot dot", "WOD-20231012..ABCDEF", true},
		{"invalid session ID traversal unix", "../../etc/passwd", true},
		{"invalid session ID traversal windows", "..\\..\\etc\\passwd", true},
		{"empty session ID", "", false}, // other validations usually catch this, but it doesn't contain path separators
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
