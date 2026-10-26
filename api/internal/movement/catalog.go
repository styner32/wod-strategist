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
		"Muscle Snatch",
		"Hang Muscle Snatch",
		"Clean",
		"Power Clean",
		"Hang Power Clean",
		"Hang Clean",
		"Squat Clean",
		"Hang Squat Clean",
		"Muscle Clean",
		"Hang Muscle Clean",
		"Clean & Jerk",
		"Hang Clean & Jerk",
		"Power Clean & Jerk",
		"Hang Power Clean & Jerk",
		"Squat Clean & Jerk",
		"Hang Squat Clean & Jerk",
		"Cluster",
		"Hang Cluster",
		"Deadlift",
		"Sumo Deadlift High Pull",
		"Romanian Deadlift",
		"Back Squat",
		"Front Squat",
		"Overhead Squat",
		"Thruster",
		"Strict Press",
		"Push Press",
		"Push Jerk",
		"Split Jerk",
		"Jerk",
		"Bench Press",
	}},
	{Category: "Dumbbell & Kettlebell", Movements: []string{
		"DB Press",
		"DB Push Press",
		"DB Push Jerk",
		"DB Snatch",
		"DB Power Snatch",
		"Hang DB Snatch",
		"DB Hang Power Snatch",
		"DB Squat Snatch",
		"DB Hang Squat Snatch",
		"DB Clean",
		"DB Power Clean",
		"DB Hang Clean",
		"DB Hang Power Clean",
		"DB Squat Clean",
		"DB Hang Squat Clean",
		"DB Deadlift",
		"DB Hang Cluster",
		"DB Clean & Jerk",
		"Hang DB Clean & Jerk",
		"DB Thruster",
		"KB Swing",
		"Devil Press",
		"Manmaker",
		"Farmer's Carry",
		"Plate Ground-to-Overhead",
		"DB Box Step-up",
		"DB Box Step-over",
		"Sandbag Box Step-up",
		"Sandbag Box Step-over",
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
		"Box Step-up",
		"Box Step-over",
		"Weighted Box Step-up",
		"Weighted Box Step-over",
		"Broad Jump",
		"Wallball Shot",
	}},
	{Category: "Cardio", Movements: []string{
		"Row",
		"Echo Bike",
		"Bike Erg",
		"Skierg",
		"Run",
		"Shuttle Run",
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

	// Barbell
	"clean and jerk":            "Clean & Jerk",
	"c&j":                       "Clean & Jerk",
	"c and j":                   "Clean & Jerk",
	"power clean and jerk":      "Power Clean & Jerk",
	"hang power clean and jerk": "Hang Power Clean & Jerk",
	"squat clean and jerk":      "Squat Clean & Jerk",
	"hang squat clean and jerk": "Hang Squat Clean & Jerk",
	"hang clean and jerk":       "Hang Clean & Jerk",
	"power clean":               "Power Clean",
	"power cleans":              "Power Clean",
	"pc":                        "Power Clean",
	"hang power clean":          "Hang Power Clean",
	"hang power cleans":         "Hang Power Clean",
	"hpc":                       "Hang Power Clean",
	"squat clean":               "Squat Clean",
	"squat cleans":              "Squat Clean",
	"sc":                        "Squat Clean",
	"hang squat clean":          "Hang Squat Clean",
	"hang squat cleans":         "Hang Squat Clean",
	"hsc":                       "Hang Squat Clean",
	"hang clean":                "Hang Power Clean",
	"hang cleans":               "Hang Power Clean",
	"muscle clean":              "Muscle Clean",
	"muscle cleans":             "Muscle Clean",
	"hang muscle clean":         "Hang Muscle Clean",
	"hang muscle cleans":        "Hang Muscle Clean",
	"power snatch":              "Power Snatch",
	"power snatches":            "Power Snatch",
	"ps":                        "Power Snatch",
	"hang power snatch":         "Hang Power Snatch",
	"hang power snatches":       "Hang Power Snatch",
	"hps":                       "Hang Power Snatch",
	"squat snatch":              "Squat Snatch",
	"squat snatches":            "Squat Snatch",
	"hang squat snatch":         "Hang Squat Snatch",
	"hang squat snatches":       "Hang Squat Snatch",
	"hss":                       "Hang Squat Snatch",
	"hang snatch":               "Hang Power Snatch",
	"hang snatches":             "Hang Power Snatch",
	"muscle snatch":             "Muscle Snatch",
	"muscle snatches":           "Muscle Snatch",
	"hang muscle snatch":        "Hang Muscle Snatch",
	"hang muscle snatches":      "Hang Muscle Snatch",
	"behind-the-neck push press": "Push Press",
	"behind the neck push press": "Push Press",
	"btn push press":            "Push Press",
	"behind-the-neck press":      "Strict Press",
	"behind the neck press":      "Strict Press",
	"overhead squat":            "Overhead Squat",
	"ohs":                       "Overhead Squat",
	"deadlift":                  "Deadlift",
	"deadlifts":                 "Deadlift",
	"dl":                        "Deadlift",
	"sumo deadlift high pull":   "Sumo Deadlift High Pull",
	"sdhp":                      "Sumo Deadlift High Pull",
	"romanian deadlift":         "Romanian Deadlift",
	"rdl":                       "Romanian Deadlift",
	"push jerk":                 "Push Jerk",
	"push jerks":                "Push Jerk",
	"pj":                        "Push Jerk",
	"split jerk":                "Split Jerk",
	"split jerks":               "Split Jerk",
	"sj":                        "Split Jerk",
	"push press":                "Push Press",
	"push presses":              "Push Press",
	"pp":                        "Push Press",
	"strict press":              "Strict Press",
	"strict presses":            "Strict Press",

	// Dumbbell & Kettlebell
	"dumbbell press":             "DB Press",
	"dumbbell push press":        "DB Push Press",
	"dumbbell push jerk":         "DB Push Jerk",
	"dumbbell snatch":            "DB Snatch",
	"dumbbell power snatch":      "DB Power Snatch",
	"dumbbell squat snatch":      "DB Squat Snatch",
	"dumbbell hang snatch":       "Hang DB Snatch",
	"dumbbell hang power snatch": "DB Hang Power Snatch",
	"dumbbell hang squat snatch": "DB Hang Squat Snatch",
	"dumbbell clean":             "DB Clean",
	"dumbbell power clean":       "DB Power Clean",
	"dumbbell squat clean":       "DB Squat Clean",
	"dumbbell hang clean":        "DB Hang Clean",
	"dumbbell hang power clean":  "DB Hang Power Clean",
	"dumbbell hang squat clean":  "DB Hang Squat Clean",
	"dumbbell deadlift":          "DB Deadlift",
	"db power clean":             "DB Power Clean",
	"db hang power clean":        "DB Hang Power Clean",
	"db hang squat clean":        "DB Hang Squat Clean",
	"db power snatch":            "DB Power Snatch",
	"db hang power snatch":       "DB Hang Power Snatch",
	"db hang squat snatch":       "DB Hang Squat Snatch",
	"db push press":              "DB Push Press",
	"db push jerk":               "DB Push Jerk",
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
	"devil's press":                    "Devil Press",
	"devils press":                     "Devil Press",
	"dumbbell box step up":             "DB Box Step-up",
	"dumbbell box step over":           "DB Box Step-over",
	"dumbbell box stepup":              "DB Box Step-up",
	"dumbbell box stepups":             "DB Box Step-up",
	"dumbbell box stepover":            "DB Box Step-over",
	"dumbbell box stepovers":           "DB Box Step-over",
	"db box stepup":                    "DB Box Step-up",
	"db box stepups":                   "DB Box Step-up",
	"db box stepover":                  "DB Box Step-over",
	"db box stepovers":                 "DB Box Step-over",
	"single db box step up":            "DB Box Step-up",
	"single dumbbell box step up":      "DB Box Step-up",
	"single arm db box step up":        "DB Box Step-up",
	"single arm dumbbell box step up":  "DB Box Step-up",
	"dual db box step up":              "DB Box Step-up",
	"dual dumbbell box step up":        "DB Box Step-up",
	"single db box step over":          "DB Box Step-over",
	"single dumbbell box step over":    "DB Box Step-over",
	"single arm db box step over":      "DB Box Step-over",
	"single arm dumbbell box step over": "DB Box Step-over",
	"dual db box step over":            "DB Box Step-over",
	"dual dumbbell box step over":      "DB Box Step-over",
	"sandbag box stepup":               "Sandbag Box Step-up",
	"sandbag box stepups":              "Sandbag Box Step-up",
	"sandbag box stepover":             "Sandbag Box Step-over",
	"sandbag box stepovers":            "Sandbag Box Step-over",

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
	"t2b":                  "Toes to Bar",
	"toes-to-bar":          "Toes to Bar",
	"hspu":                 "Handstand Push-up",
	"hspus":                "Handstand Push-up",
	"kipping hspu":         "Handstand Push-up",
	"strict hspu":          "Handstand Push-up",
	"hsw":                  "Handstand Walk",
	"handstand walking":    "Handstand Walk",
	"ghd situp":            "GHD Sit-up",
	"ghd sit-up":           "GHD Sit-up",
	"pistol squat":         "Pistol",
	"single leg squat":     "Pistol",

	// Bodyweight & Plyo
	"pushup":         "Push-up",
	"pushups":        "Push-up",
	"push up":        "Push-up",
	"situp":          "Sit-up",
	"situps":         "Sit-up",
	"sit up":         "Sit-up",
	"wall ball":      "Wallball Shot",
	"wall balls":     "Wallball Shot",
	"wallball":       "Wallball Shot",
	"wallballs":      "Wallball Shot",
	"wall ball shot": "Wallball Shot",
	"wb":             "Wallball Shot",
	"bj":                    "Box Jump",
	"bjo":                   "Box Jump Over",
	"bbjo":                  "Burpee Box Jump Over",
	"stepup":                "Box Step-up",
	"stepups":               "Box Step-up",
	"step up":               "Box Step-up",
	"box stepup":            "Box Step-up",
	"box stepups":           "Box Step-up",
	"box stepover":          "Box Step-over",
	"box stepovers":         "Box Step-over",
	"weighted step up":      "Weighted Box Step-up",
	"weighted stepup":       "Weighted Box Step-up",
	"weighted stepups":      "Weighted Box Step-up",
	"weighted box stepup":   "Weighted Box Step-up",
	"weighted box stepups":  "Weighted Box Step-up",
	"weighted box stepover": "Weighted Box Step-over",
	"shuttle run":    "Shuttle Run",
	"shuttle runs":   "Shuttle Run",
	"ground to overhead":       "Plate Ground-to-Overhead",
	"ground-to-overhead":       "Plate Ground-to-Overhead",
	"gtoh":                     "Plate Ground-to-Overhead",
	"plate ground to overhead": "Plate Ground-to-Overhead",
	"plate gtoh":               "Plate Ground-to-Overhead",
	"jerk":                     "Jerk",
	"jerks":                    "Jerk",

	// Korean aliases
	"클린 앤 저크":         "Clean & Jerk",
	"클린앤저크":           "Clean & Jerk",
	"클린 엔 저크":         "Clean & Jerk",
	"클린엔저크":           "Clean & Jerk",
	"클린저크":            "Clean & Jerk",
	"파워 클린 앤 저크":      "Power Clean & Jerk",
	"파워클린앤저크":        "Power Clean & Jerk",
	"스쿼트 클린 앤 저크":     "Squat Clean & Jerk",
	"스쿼트클린앤저크":       "Squat Clean & Jerk",
	"행 클린 앤 저크":       "Hang Clean & Jerk",
	"행클린앤저크":         "Hang Clean & Jerk",
	"행 파워 클린 앤 저크":    "Hang Power Clean & Jerk",
	"행파워클린앤저크":      "Hang Power Clean & Jerk",
	"파워 클린":          "Power Clean",
	"파워클린":           "Power Clean",
	"스쿼트 클린":         "Squat Clean",
	"스쿼트클린":          "Squat Clean",
	"행 파워 클린":        "Hang Power Clean",
	"행파워클린":          "Hang Power Clean",
	"행 스쿼트 클린":       "Hang Squat Clean",
	"행스쿼트클린":         "Hang Squat Clean",
	"행 클린":           "Hang Clean",
	"행클린":            "Hang Clean",
	"머슬 클린":          "Muscle Clean",
	"머슬클린":           "Muscle Clean",
	"클린":             "Clean",
	"파워 스내치":         "Power Snatch",
	"파워스내치":          "Power Snatch",
	"스쿼트 스내치":        "Squat Snatch",
	"스쿼트스내치":         "Squat Snatch",
	"행 파워 스내치":       "Hang Power Snatch",
	"행파워스내치":         "Hang Power Snatch",
	"행 스쿼트 스내치":      "Hang Squat Snatch",
	"행스쿼트스내치":        "Hang Squat Snatch",
	"머슬 스내치":         "Muscle Snatch",
	"머슬스내치":          "Muscle Snatch",
	"푸시 저크":          "Push Jerk",
	"푸시저크":           "Push Jerk",
	"푸쉬 저크":          "Push Jerk",
	"푸쉬저크":           "Push Jerk",
	"스플릿 저크":         "Split Jerk",
	"스플릿저크":          "Split Jerk",
	"저크":             "Jerk",
	"푸시 프레스":         "Push Press",
	"푸시프레스":          "Push Press",
	"푸쉬 프레스":         "Push Press",
	"푸쉬프레스":          "Push Press",
	"스트릭트 프레스":       "Strict Press",
	"스트릭트프레스":        "Strict Press",
	"밀리터리 프레스":       "Strict Press",
	"벤치 프레스":         "Bench Press",
	"벤치프레스":          "Bench Press",
	"쓰러스터":           "Thruster",
	"스러스터":           "Thruster",
	"클러스터":           "Cluster",
	"프론트 스쿼트":        "Front Squat",
	"프론트스쿼트":         "Front Squat",
	"프런트 스쿼트":        "Front Squat",
	"프런트스쿼트":         "Front Squat",
	"백 스쿼트":          "Back Squat",
	"백스쿼트":           "Back Squat",
	"오버헤드 스쿼트":       "Overhead Squat",
	"오버헤드스쿼트":        "Overhead Squat",
	"에어 스쿼트":         "Air Squat",
	"에어스쿼트":          "Air Squat",
	"맨몸 스쿼트":         "Air Squat",
	"데드리프트":          "Deadlift",
	"스모 데드리프트 하이풀":   "Sumo Deadlift High Pull",
	"스모데드리프트하이풀":     "Sumo Deadlift High Pull",
	"루마니안 데드리프트":     "Romanian Deadlift",
	"루마니안데드리프트":      "Romanian Deadlift",
	"덤벨 스내치":         "DB Snatch",
	"덤벨스내치":          "DB Snatch",
	"덤벨 클린":          "DB Clean",
	"덤벨클린":           "DB Clean",
	"덤벨 클린 앤 저크":     "DB Clean & Jerk",
	"덤벨클린앤저크":       "DB Clean & Jerk",
	"덤벨 쓰러스터":        "DB Thruster",
	"덤벨 스러스터":        "DB Thruster",
	"덤벨쓰러스터":         "DB Thruster",
	"케틀벨 스윙":         "KB Swing",
	"케틀벨스윙":          "KB Swing",
	"데빌 프레스":         "Devil Press",
	"데빌프레스":          "Devil Press",
	"월볼":             "Wallball Shot",
	"월볼샷":            "Wallball Shot",
	"월볼 샷":           "Wallball Shot",
	"버피":             "Burpee",
	"버피 박스 점프 오버":    "Burpee Box Jump Over",
	"버피박스점프오버":      "Burpee Box Jump Over",
	"박스 점프 오버":       "Box Jump Over",
	"박스점프오버":         "Box Jump Over",
	"박스 점프":          "Box Jump",
	"박스점프":           "Box Jump",
	"박스 스텝업":         "Box Step-up",
	"박스스텝업":          "Box Step-up",
	"스텝업":            "Box Step-up",
	"박스 스텝 오버":       "Box Step-over",
	"박스 스텝오버":        "Box Step-over",
	"박스스텝오버":         "Box Step-over",
	"덤벨 박스 스텝업":      "DB Box Step-up",
	"덤벨박스스텝업":        "DB Box Step-up",
	"덤벨 박스 스텝 오버":    "DB Box Step-over",
	"덤벨 박스 스텝오버":     "DB Box Step-over",
	"덤벨박스스텝오버":       "DB Box Step-over",
	"중량 박스 스텝업":      "Weighted Box Step-up",
	"웨이트 박스 스텝업":     "Weighted Box Step-up",
	"샌드백 박스 스텝업":     "Sandbag Box Step-up",
	"샌드백 박스 스텝오버":    "Sandbag Box Step-over",
	"샌드백 박스 스텝 오버":   "Sandbag Box Step-over",
	"푸시업":            "Push-up",
	"푸쉬업":            "Push-up",
	"팔굽혀펴기":          "Push-up",
	"싯업":             "Sit-up",
	"윗몸일으키기":         "Sit-up",
	"런지":             "Lunge",
	"풀업":             "Pull-up",
	"턱걸이":            "Pull-up",
	"체스트 투 바":        "Chest to Bar",
	"체스트투바":          "Chest to Bar",
	"바 머슬업":          "Bar Muscle-up",
	"바머슬업":           "Bar Muscle-up",
	"링 머슬업":          "Ring Muscle-up",
	"링머슬업":           "Ring Muscle-up",
	"토투바":            "Toes to Bar",
	"토 투 바":          "Toes to Bar",
	"핸드스탠드 푸시업":      "Handstand Push-up",
	"핸드스탠드 푸쉬업":      "Handstand Push-up",
	"물구나무 푸시업":       "Handstand Push-up",
	"핸드스탠드 워크":       "Handstand Walk",
	"물구나무 걷기":        "Handstand Walk",
	"로프 클라임":         "Rope Climb",
	"로프클라임":          "Rope Climb",
	"월 워크":           "Wall Walk",
	"월워크":            "Wall Walk",
	"더블 언더":          "Double-under",
	"더블언더":           "Double-under",
	"이단뛰기":           "Double-under",
	"싱글 언더":          "Single-under",
	"싱글언더":           "Single-under",
	"일단뛰기":           "Single-under",
	"줄넘기":            "Single-under",
	"셔틀런":            "Shuttle Run",
	"셔틀 런":           "Shuttle Run",
	"왕복달리기":          "Shuttle Run",
	"왕복 달리기":         "Shuttle Run",
	"로잉":             "Row",
	"에코 바이크":         "Echo Bike",
	"에코바이크":          "Echo Bike",
	"어썰트 바이크":        "Echo Bike",
	"바이크 에르그":        "Bike Erg",
	"스키 에르그":         "Skierg",
	"스키에르그":          "Skierg",
	"플레이트 그라운드 투 오버헤드": "Plate Ground-to-Overhead",
	"그라운드 투 오버헤드":      "Plate Ground-to-Overhead",
}

// These names omit the variant, implement, or cycle needed to identify a catalog movement.
var ambiguousNames = map[string]bool{
	"snatch": true, "muscle up": true, "dip": true, "squat": true,
	"press": true, "shoulder press": true, "overhead press": true, "ohp": true,
	"jump rope": true, "skipping rope": true, "ghd": true,
}

// IsAmbiguous reports names that cannot identify a catalog subtype by themselves.
func IsAmbiguous(raw string) bool {
	key := NormalizeKey(raw)
	return ambiguousNames[key] ||
		(strings.HasSuffix(key, "es") && ambiguousNames[strings.TrimSuffix(key, "es")]) ||
		(strings.HasSuffix(key, "s") && ambiguousNames[strings.TrimSuffix(key, "s")])
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
	if key == "" || IsAmbiguous(raw) {
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
