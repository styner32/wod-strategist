package worker

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestValidateSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantErr   bool
	}{
		{"valid", "session-123", false},
		{"path traversal unix", "../session", true},
		{"path traversal windows", "..\\session", true},
		{"absolute path unix", "/session", true},
		{"absolute path windows", "\\session", true},
		{"dot dot", "foo..bar", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSessionID(tt.sessionID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
