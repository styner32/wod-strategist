package controllers

import (
	"path"
	"regexp"
	"strings"

	"github.com/wod-strategist/api/internal/movement"
)

// MovementGroup represents a category of workout movements.
type MovementGroup = movement.MovementGroup

var movementGroups = movement.MovementGroups
var movements = movement.All()

const MaxMovementCount = 100
const MaxMovementNameLength = 100

var injuries = []string{
	"Neck",
	"Left Shoulder",
	"Right Shoulder",
	"Left Elbow",
	"Right Elbow",
	"Wrist",
	"Upper Back",
	"Lower Back",
	"Hip",
	"Left Hamstring",
	"Right Hamstring",
	"Left Knee",
	"Right Knee",
	"Left Ankle",
	"Right Ankle",
}

type allowedSet map[string]struct{}

var (
	allowedInjuries = newAllowedSet(injuries)
)

func newAllowedSet(values []string) allowedSet {
	set := make(allowedSet, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func (s allowedSet) containsAll(values []string) bool {
	for _, value := range values {
		if _, ok := s[value]; !ok {
			return false
		}
	}
	return true
}

// movementNameRE restricts movement names to characters safe to splice
// into an LLM prompt. Blocks newlines, control chars, backticks, angle
// brackets, and other delimiters used in prompt-injection payloads, while
// permitting every character used in the predefined movement list.
var movementNameRE = regexp.MustCompile(`^[A-Za-z0-9 &\-'/()+.]+$`)

// validateMovements checks movement list constraints without requiring
// movements to be in the predefined list (supports custom movements).
func validateMovements(values []string) (bool, string) {
	if len(values) > MaxMovementCount {
		return false, "too many movements"
	}
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return false, "movement name cannot be empty"
		}
		if len(trimmed) > MaxMovementNameLength {
			return false, "movement name too long"
		}
		if !movementNameRE.MatchString(trimmed) {
			return false, "movement name contains invalid characters"
		}
	}
	return true, ""
}

func sanitizeObjectPart(value string, fallback string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	base := strings.TrimSpace(path.Base(normalized))
	if base == "" || base == "." || base == "/" || base == ".." {
		return fallback
	}
	return base
}

// sanitizeFilename strips characters that are unsafe in HTTP
// Content-Disposition filename parameters (", \, CR, LF, NUL).
// Go's net/http already strips CR/LF from response headers since 1.20,
// but this provides defense-in-depth against double-quote breakout.
func sanitizeFilename(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '\r', '\n', '\x00':
			return -1 // drop
		default:
			return r
		}
	}, value)
}

var (
	newSessionIDPattern    = regexp.MustCompile(`^[A-Za-z0-9]+-\d{8,12}-[A-Za-z0-9]+$`)
	legacySessionIDPattern = regexp.MustCompile(`^(P[1-9][0-9]*-)?[A-Za-z0-9]+-\d{4}-\d{2}-\d{2}-\d{2}-\d{2}$`)
	testSessionIDPattern   = regexp.MustCompile(`^(session|test|feedback|limit|paginated|sess|split)[A-Za-z0-9_-]*$`)
)

// isValidSessionID checks if the session ID format is valid and safe.
// It prevents path traversal and ensures the format aligns with supported
// formats: current, legacy, or recognized development/test identifiers.
func isValidSessionID(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	// Block dots, slashes, backslashes to prevent directory traversal/path corruption
	if strings.ContainsAny(sessionID, "./\\") {
		return false
	}
	return newSessionIDPattern.MatchString(sessionID) ||
		legacySessionIDPattern.MatchString(sessionID) ||
		testSessionIDPattern.MatchString(sessionID)
}

