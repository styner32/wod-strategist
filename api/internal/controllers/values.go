package controllers

import (
	"path"
	"strings"
)

var movements = []string{
	"Burpee",
	"Power Snatch",
	"Hang Power Snatch",
	"Squat Snatch",
	"Hang Squat Snatch",
	"DB Snatch",
	"Hang DB Snatch",
	"Clean & Jerk",
	"Hang Clean & Jerk",
	"Power Clean & Jerk",
	"Hang Power Clean & Jerk",
	"Squat Clean & Jerk",
	"Hang Squat Clean & Jerk",
	"DB Clean & Jerk",
	"Hang DB Clean & Jerk",
	"Thruster",
	"Wallball Shot",
	"Double-under",
	"Deadlift",
	"Back Squat",
	"Overhead Squat",
	"Box Jump",
	"Burpee Box Jump Over",
	"Handstand Push-up",
	"Pull-up",
	"Chest to Bar",
	"Muscle-up",
	"Row",
	"Echo Bike",
	"Skierg",
	"Toes to Bar",
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
	allowedMovements = newAllowedSet(movements)
	allowedInjuries  = newAllowedSet(injuries)
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

func sanitizeObjectPart(value string, fallback string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	base := strings.TrimSpace(path.Base(normalized))
	if base == "" || base == "." || base == "/" || base == ".." {
		return fallback
	}
	return base
}
