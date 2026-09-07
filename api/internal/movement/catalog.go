package movement

import (
	"regexp"
	"strings"
)

// MovementGroup represents a category of workout movements.
type MovementGroup struct {
	Category  string   `json:"category"`
	Movements []string `json:"movements"`
}

// MovementGroups is the master list of categorized movements.
var MovementGroups = []MovementGroup{
	{Category: "Barbell", Movements: []string{
		"Power Snatch",
		"Hang Power Snatch",
		"Squat Snatch",
		"Hang Squat Snatch",
		"Clean",
		"Power Clean",
		"Hang Clean",
		"Squat Clean",
		"Clean & Jerk",
		"Hang Clean & Jerk",
		"Power Clean & Jerk",
		"Hang Power Clean & Jerk",
		"Squat Clean & Jerk",
		"Hang Squat Clean & Jerk",
		"Cluster",
		"Hang Cluster",
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
		"DB Press",
		"DB Snatch",
		"DB Clean",
		"DB Hang Clean",
		"DB Squat Clean",
		"DB Hang Cluster",
		"Hang DB Snatch",
		"DB Clean & Jerk",
		"Hang DB Clean & Jerk",
		"DB Thruster",
		"KB Swing",
		"Devil Press",
		"Manmaker",
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
	{Category: "Surfing", Movements: []string{
		"Surfing",
		"Pop-up",
		"Duck Dive",
		"Paddle",
		"Bottom Turn",
		"Cutback",
		"Top Turn",
		"Snap",
		"Floater",
		"Tube Ride",
		"Aerial",
		"Turtle Roll",
		"Take-off",
	}},
	{Category: "Yoga", Movements: []string{
		"Yoga",
		"Downward Dog",
		"Warrior I",
		"Warrior II",
		"Warrior III",
		"Tree Pose",
		"Chair Pose",
		"Triangle Pose",
		"Plank Pose",
		"Cobra Pose",
		"Child's Pose",
		"Bridge Pose",
		"Crow Pose",
		"Headstand",
		"Shoulder Stand",
		"Pigeon Pose",
		"Camel Pose",
		"Boat Pose",
		"Sun Salutation",
	}},
}

var allMovements []string

func init() {
	for _, g := range MovementGroups {
		allMovements = append(allMovements, g.Movements...)
	}
}

// All returns a flat list of all movement names across all categories.
func All() []string {
	res := make([]string, len(allMovements))
	copy(res, allMovements)
	return res
}

var spacesOrHyphens = regexp.MustCompile(`[\s-]+`)

// NormalizeKey converts a raw movement string into a normalized matching key
// (lowercased, trimmed, hyphens converted to spaces, consecutive whitespace collapsed).
func NormalizeKey(raw string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return ""
	}
	return spacesOrHyphens.ReplaceAllString(trimmed, " ")
}
