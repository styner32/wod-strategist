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

var (
	allMovements    []string
	canonicalLookup map[string]string
	aliasLookup     map[string]string
)

var rawAliases = map[string]string{
	// Cardio & endurance
	"rowing":          "Row",
	"rower":           "Row",
	"concept2 row":    "Row",
	"concept 2 row":   "Row",
	"running":         "Run",
	"jogging":         "Run",
	"sprint":          "Run",
	"sprinting":       "Run",
	"assault bike":    "Echo Bike",
	"air bike":        "Echo Bike",
	"airdyne":         "Echo Bike",
	"rogue echo bike": "Echo Bike",
	"bikeerg":         "Bike Erg",
	"concept2 bike":   "Bike Erg",
	"concept 2 bike":  "Bike Erg",
	"skierg":          "Skierg",
	"ski erg":         "Skierg",
	"concept2 ski":    "Skierg",
	"concept 2 ski":   "Skierg",

	// Jump Rope
	"double unders": "Double-under",
	"double under":  "Double-under",
	"double-unders": "Double-under",
	"double-under":  "Double-under",
	"du":            "Double-under",
	"dus":           "Double-under",
	"single unders": "Single-under",
	"single under":  "Single-under",
	"single-unders": "Single-under",
	"single-under":  "Single-under",
	"su":            "Single-under",
	"sus":           "Single-under",
	"jump rope":     "Single-under",
	"skipping rope": "Single-under",

	// Barbell
	"clean and jerk":            "Clean & Jerk",
	"c&j":                       "Clean & Jerk",
	"c and j":                   "Clean & Jerk",
	"power clean and jerk":      "Power Clean & Jerk",
	"hang power clean and jerk": "Hang Power Clean & Jerk",
	"squat clean and jerk":      "Squat Clean & Jerk",
	"hang squat clean and jerk": "Hang Squat Clean & Jerk",
	"hang clean and jerk":       "Hang Clean & Jerk",
	"snatch":                    "Power Snatch",
	"overhead squat":            "Overhead Squat",
	"ohs":                       "Overhead Squat",
	"press":                     "Strict Press",
	"shoulder press":            "Strict Press",
	"overhead press":            "Strict Press",
	"ohp":                       "Strict Press",

	// Dumbbell & Kettlebell
	"dumbbell press":             "DB Press",
	"dumbbell snatch":            "DB Snatch",
	"dumbbell clean":             "DB Clean",
	"dumbbell hang clean":        "DB Hang Clean",
	"dumbbell squat clean":       "DB Squat Clean",
	"dumbbell hang cluster":      "DB Hang Cluster",
	"hang dumbbell snatch":       "Hang DB Snatch",
	"dumbbell clean & jerk":      "DB Clean & Jerk",
	"dumbbell clean and jerk":    "DB Clean & Jerk",
	"hang dumbbell clean & jerk": "Hang DB Clean & Jerk",
	"hang db clean and jerk":     "Hang DB Clean & Jerk",
	"dumbbell thruster":          "DB Thruster",
	"kettlebell swing":           "KB Swing",
	"kb swings":                  "KB Swing",
	"american kettlebell swing":  "KB Swing",
	"russian kettlebell swing":   "KB Swing",
	"farmers carry":              "Farmer's Carry",
	"farmer carry":               "Farmer's Carry",
	"farmer walk":                "Farmer's Carry",
	"farmer walks":               "Farmer's Carry",
	"farmer's walk":              "Farmer's Carry",
	"devil's press":              "Devil Press",
	"devils press":               "Devil Press",

	// Gymnastics
	"pullup":               "Pull-up",
	"pullups":              "Pull-up",
	"pull up":              "Pull-up",
	"c2b":                  "Chest to Bar",
	"c2b pull up":          "Chest to Bar",
	"chest to bar pull up": "Chest to Bar",
	"chest to bar pull-up": "Chest to Bar",
	"bmu":                  "Bar Muscle-up",
	"rmu":                  "Ring Muscle-up",
	"muscle up":            "Bar Muscle-up",
	"muscle-up":            "Bar Muscle-up",
	"t2b":                  "Toes to Bar",
	"toes-to-bar":          "Toes to Bar",
	"hspu":                 "Handstand Push-up",
	"hspus":                "Handstand Push-up",
	"kipping hspu":         "Handstand Push-up",
	"strict hspu":          "Handstand Push-up",
	"hsw":                  "Handstand Walk",
	"handstand walking":    "Handstand Walk",
	"ghd":                  "GHD Sit-up",
	"ghd situp":            "GHD Sit-up",
	"ghd sit-up":           "GHD Sit-up",
	"pistol squat":         "Pistol",
	"single leg squat":     "Pistol",
	"dips":                 "Ring Dip",
	"dip":                  "Ring Dip",

	// Bodyweight & Plyo
	"pushup":         "Push-up",
	"pushups":        "Push-up",
	"push up":        "Push-up",
	"situp":          "Sit-up",
	"situps":         "Sit-up",
	"sit up":         "Sit-up",
	"squat":          "Air Squat",
	"wall ball":      "Wallball Shot",
	"wall balls":     "Wallball Shot",
	"wallball":       "Wallball Shot",
	"wallballs":      "Wallball Shot",
	"wall ball shot": "Wallball Shot",
	"wb":             "Wallball Shot",
	"bj":             "Box Jump",
	"bjo":            "Box Jump Over",
	"bbjo":           "Burpee Box Jump Over",
}

func init() {
	canonicalLookup = make(map[string]string)
	aliasLookup = make(map[string]string)

	for _, g := range MovementGroups {
		for _, m := range g.Movements {
			allMovements = append(allMovements, m)
			canonicalLookup[NormalizeKey(m)] = m
		}
	}

	for alias, canonical := range rawAliases {
		normAlias := NormalizeKey(alias)
		if normAlias != "" {
			aliasLookup[normAlias] = canonical
		}
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
// (lowercased, trimmed, '&' converted to 'and', hyphens converted to spaces, consecutive whitespace collapsed).
func NormalizeKey(raw string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "&", " and ")
	return spacesOrHyphens.ReplaceAllString(trimmed, " ")
}

// Canonical maps any raw movement string or alias into its canonical catalog name.
// It checks exact normalized matches, explicit aliases, and regular English plural forms.
// Returns an empty string if no canonical match is found.
func Canonical(raw string) string {
	key := NormalizeKey(raw)
	if key == "" {
		return ""
	}

	// 1. Direct match with canonical movements
	if canonical, ok := canonicalLookup[key]; ok {
		return canonical
	}

	// 2. Explicit alias lookup
	if canonical, ok := aliasLookup[key]; ok {
		return canonical
	}

	// 3. Regular plural fallback: "es" or "s"
	if strings.HasSuffix(key, "es") {
		singular := strings.TrimSuffix(key, "es")
		if canonical, ok := canonicalLookup[singular]; ok {
			return canonical
		}
		if canonical, ok := aliasLookup[singular]; ok {
			return canonical
		}
	}
	if strings.HasSuffix(key, "s") {
		singular := strings.TrimSuffix(key, "s")
		if canonical, ok := canonicalLookup[singular]; ok {
			return canonical
		}
		if canonical, ok := aliasLookup[singular]; ok {
			return canonical
		}
	}

	return ""
}
