package controllers

import (
	"path"
	"regexp"
	"strings"
)

// MovementGroup represents a category of workout movements.
type MovementGroup struct {
	Category  string   `json:"category"`
	Movements []string `json:"movements"`
}

var movementGroups = []MovementGroup{
	{Category: "Barbell", Movements: []string{
		"Power Snatch",
		"Hang Power Snatch",
		"Squat Snatch",
		"Hang Squat Snatch",
		"Clean & Jerk",
		"Hang Clean & Jerk",
		"Power Clean & Jerk",
		"Hang Power Clean & Jerk",
		"Squat Clean & Jerk",
		"Hang Squat Clean & Jerk",
		"Deadlift",
		"Back Squat",
		"Front Squat",
		"Overhead Squat",
		"Thruster",
		"Strict Press",
		"Push Press",
		"Push Jerk",
		"Bench Press",
	}},
	{Category: "Dumbbell & Kettlebell", Movements: []string{
		"DB Snatch",
		"Hang DB Snatch",
		"DB Clean & Jerk",
		"Hang DB Clean & Jerk",
		"DB Thruster",
		"KB Swing",
		"Devil Press",
		"Farmer's Carry",
	}},
	{Category: "Gymnastics", Movements: []string{
		"Pull-up",
		"Chest to Bar",
		"Bar Muscle-up",
		"Ring Muscle-up",
		"Toes to Bar",
		"Handstand Push-up",
		"Handstand Walk",
		"Rope Climb",
		"Ring Dip",
		"Pistol",
		"Wall Walk",
		"GHD Sit-up",
	}},
	{Category: "Bodyweight & Plyo", Movements: []string{
		"Burpee",
		"Push-up",
		"Air Squat",
		"Sit-up",
		"Lunge",
		"Box Jump",
		"Box Jump Over",
		"Burpee Box Jump Over",
		"Broad Jump",
		"Wallball Shot",
	}},
	{Category: "Cardio", Movements: []string{
		"Row",
		"Echo Bike",
		"Bike Erg",
		"Skierg",
		"Run",
		"Double-under",
		"Single-under",
	}},
}

// movements is the flat list derived from movementGroups (for GET /movements backward compat).
var movements []string

func init() {
	for _, g := range movementGroups {
		movements = append(movements, g.Movements...)
	}
}

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
	if len(values) >= 100 {
		return false, "too many movements"
	}
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return false, "movement name cannot be empty"
		}
		if len(trimmed) > 100 {
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
