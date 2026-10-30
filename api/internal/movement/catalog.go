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
		"Barbell Lunge",
		"Back Rack Lunge",
		"Front Rack Lunge",
		"Overhead Lunge",
		"Barbell Walking Lunge",
		"Back Rack Walking Lunge",
		"Front Rack Walking Lunge",
		"Overhead Walking Lunge",
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
		"DB Lunge",
		"DB Reverse Lunge",
		"DB Front Rack Lunge",
		"DB Overhead Lunge",
		"DB Walking Lunge",
		"DB Front Rack Walking Lunge",
		"DB Overhead Walking Lunge",
		"KB Swing",
		"KB Lunge",
		"Goblet Lunge",
		"KB Front Rack Lunge",
		"KB Overhead Lunge",
		"KB Walking Lunge",
		"Goblet Walking Lunge",
		"KB Front Rack Walking Lunge",
		"KB Overhead Walking Lunge",
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
		"Walking Lunge",
		"Reverse Lunge",
		"Jumping Lunge",
		"Weighted Lunge",
		"Weighted Walking Lunge",
		"Sandbag Lunge",
		"Sandbag Walking Lunge",
		"Box Jump",
		"Box Jump Over",
		"Burpee Box Jump Over",
		"Box Step-up",
		"Box Step-over",
		"Weighted Box Step-up",
		"Weighted Box Step-over",
		"Broad Jump",
		"Burpee Broad Jump",
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

	// Barbell, Dumbbell, Kettlebell & Bodyweight Lunges + Burpee Broad Jump (EN)
	"forward lunge":                               "Lunge",
	"stationary lunge":                            "Lunge",
	"in place lunge":                              "Lunge",
	"alternating lunge":                           "Lunge",
	"alt lunge":                                   "Lunge",
	"step back lunge":                             "Reverse Lunge",
	"backward lunge":                              "Reverse Lunge",
	"jump lunge":                                  "Jumping Lunge",
	"plyo lunge":                                  "Jumping Lunge",
	"split lunge jump":                            "Jumping Lunge",
	"walking weighted lunge":                      "Weighted Walking Lunge",
	"walking sandbag lunge":                       "Sandbag Walking Lunge",
	"bb lunge":                                    "Barbell Lunge",
	"walking barbell lunge":                       "Barbell Walking Lunge",
	"bb walking lunge":                            "Barbell Walking Lunge",
	"barbell back rack lunge":                     "Back Rack Lunge",
	"walking back rack lunge":                     "Back Rack Walking Lunge",
	"barbell back rack walking lunge":             "Back Rack Walking Lunge",
	"barbell front rack lunge":                    "Front Rack Lunge",
	"walking front rack lunge":                    "Front Rack Walking Lunge",
	"barbell front rack walking lunge":            "Front Rack Walking Lunge",
	"barbell overhead lunge":                      "Overhead Lunge",
	"oh lunge":                                    "Overhead Lunge",
	"plate overhead lunge":                        "Overhead Lunge",
	"walking overhead lunge":                      "Overhead Walking Lunge",
	"barbell overhead walking lunge":              "Overhead Walking Lunge",
	"oh walking lunge":                            "Overhead Walking Lunge",
	"plate overhead walking lunge":                "Overhead Walking Lunge",
	"dumbbell lunge":                              "DB Lunge",
	"alt db lunge":                                "DB Lunge",
	"alt dumbbell lunge":                          "DB Lunge",
	"alternating db lunge":                        "DB Lunge",
	"alternating dumbbell lunge":                  "DB Lunge",
	"single db lunge":                             "DB Lunge",
	"single dumbbell lunge":                       "DB Lunge",
	"dual db lunge":                               "DB Lunge",
	"dual dumbbell lunge":                         "DB Lunge",
	"dumbbell walking lunge":                      "DB Walking Lunge",
	"walking db lunge":                            "DB Walking Lunge",
	"walking dumbbell lunge":                      "DB Walking Lunge",
	"single db walking lunge":                     "DB Walking Lunge",
	"single dumbbell walking lunge":               "DB Walking Lunge",
	"dual db walking lunge":                       "DB Walking Lunge",
	"dual dumbbell walking lunge":                 "DB Walking Lunge",
	"dumbbell reverse lunge":                      "DB Reverse Lunge",
	"dumbbell step back lunge":                    "DB Reverse Lunge",
	"db step back lunge":                          "DB Reverse Lunge",
	"alt db reverse lunge":                        "DB Reverse Lunge",
	"alternating db reverse lunge":                "DB Reverse Lunge",
	"alternating dumbbell reverse lunge":          "DB Reverse Lunge",
	"dumbbell front rack lunge":                   "DB Front Rack Lunge",
	"db rack lunge":                               "DB Front Rack Lunge",
	"dumbbell rack lunge":                         "DB Front Rack Lunge",
	"single db front rack lunge":                  "DB Front Rack Lunge",
	"single dumbbell front rack lunge":            "DB Front Rack Lunge",
	"dual db front rack lunge":                    "DB Front Rack Lunge",
	"dual dumbbell front rack lunge":              "DB Front Rack Lunge",
	"dumbbell front rack walking lunge":           "DB Front Rack Walking Lunge",
	"dumbbell walking front rack lunge":           "DB Front Rack Walking Lunge",
	"db walking front rack lunge":                 "DB Front Rack Walking Lunge",
	"walking db front rack lunge":                 "DB Front Rack Walking Lunge",
	"walking dumbbell front rack lunge":           "DB Front Rack Walking Lunge",
	"dual db front rack walking lunge":            "DB Front Rack Walking Lunge",
	"dual dumbbell front rack walking lunge":      "DB Front Rack Walking Lunge",
	"dumbbell overhead lunge":                     "DB Overhead Lunge",
	"db oh lunge":                                 "DB Overhead Lunge",
	"dumbbell oh lunge":                           "DB Overhead Lunge",
	"single arm db overhead lunge":                "DB Overhead Lunge",
	"single arm dumbbell overhead lunge":          "DB Overhead Lunge",
	"one arm db overhead lunge":                   "DB Overhead Lunge",
	"one arm dumbbell overhead lunge":             "DB Overhead Lunge",
	"dual db overhead lunge":                      "DB Overhead Lunge",
	"dual dumbbell overhead lunge":                "DB Overhead Lunge",
	"alt db overhead lunge":                       "DB Overhead Lunge",
	"alternating db overhead lunge":               "DB Overhead Lunge",
	"alternating dumbbell overhead lunge":         "DB Overhead Lunge",
	"dumbbell overhead walking lunge":             "DB Overhead Walking Lunge",
	"dumbbell walking overhead lunge":             "DB Overhead Walking Lunge",
	"db walking overhead lunge":                   "DB Overhead Walking Lunge",
	"walking db overhead lunge":                   "DB Overhead Walking Lunge",
	"walking dumbbell overhead lunge":             "DB Overhead Walking Lunge",
	"db oh walking lunge":                         "DB Overhead Walking Lunge",
	"dumbbell oh walking lunge":                   "DB Overhead Walking Lunge",
	"single arm db overhead walking lunge":        "DB Overhead Walking Lunge",
	"single arm dumbbell overhead walking lunge":  "DB Overhead Walking Lunge",
	"single arm db walking overhead lunge":        "DB Overhead Walking Lunge",
	"single arm dumbbell walking overhead lunge":  "DB Overhead Walking Lunge",
	"one arm db overhead walking lunge":           "DB Overhead Walking Lunge",
	"one arm dumbbell overhead walking lunge":     "DB Overhead Walking Lunge",
	"dual db overhead walking lunge":              "DB Overhead Walking Lunge",
	"dual dumbbell overhead walking lunge":        "DB Overhead Walking Lunge",
	"kettlebell lunge":                            "KB Lunge",
	"alt kb lunge":                                "KB Lunge",
	"alternating kb lunge":                        "KB Lunge",
	"alternating kettlebell lunge":                "KB Lunge",
	"kettlebell walking lunge":                    "KB Walking Lunge",
	"walking kb lunge":                            "KB Walking Lunge",
	"walking kettlebell lunge":                    "KB Walking Lunge",
	"kb goblet lunge":                             "Goblet Lunge",
	"kettlebell goblet lunge":                     "Goblet Lunge",
	"db goblet lunge":                             "Goblet Lunge",
	"dumbbell goblet lunge":                       "Goblet Lunge",
	"walking goblet lunge":                        "Goblet Walking Lunge",
	"kb goblet walking lunge":                     "Goblet Walking Lunge",
	"kettlebell goblet walking lunge":             "Goblet Walking Lunge",
	"db goblet walking lunge":                     "Goblet Walking Lunge",
	"dumbbell goblet walking lunge":               "Goblet Walking Lunge",
	"kettlebell front rack lunge":                 "KB Front Rack Lunge",
	"kb rack lunge":                               "KB Front Rack Lunge",
	"kettlebell rack lunge":                       "KB Front Rack Lunge",
	"dual kb front rack lunge":                    "KB Front Rack Lunge",
	"kettlebell front rack walking lunge":         "KB Front Rack Walking Lunge",
	"kettlebell walking front rack lunge":         "KB Front Rack Walking Lunge",
	"kb walking front rack lunge":                 "KB Front Rack Walking Lunge",
	"walking kb front rack lunge":                 "KB Front Rack Walking Lunge",
	"walking kettlebell front rack lunge":         "KB Front Rack Walking Lunge",
	"kettlebell overhead lunge":                   "KB Overhead Lunge",
	"kb oh lunge":                                 "KB Overhead Lunge",
	"kettlebell oh lunge":                         "KB Overhead Lunge",
	"single arm kb overhead lunge":                "KB Overhead Lunge",
	"single arm kettlebell overhead lunge":        "KB Overhead Lunge",
	"kettlebell overhead walking lunge":           "KB Overhead Walking Lunge",
	"kettlebell walking overhead lunge":           "KB Overhead Walking Lunge",
	"kb walking overhead lunge":                   "KB Overhead Walking Lunge",
	"walking kb overhead lunge":                   "KB Overhead Walking Lunge",
	"walking kettlebell overhead lunge":           "KB Overhead Walking Lunge",
	"kb oh walking lunge":                         "KB Overhead Walking Lunge",
	"single arm kb overhead walking lunge":        "KB Overhead Walking Lunge",
	"single arm kettlebell overhead walking lunge": "KB Overhead Walking Lunge",
	"bbj": "Burpee Broad Jump",

	// Lunges + Burpee Broad Jump (KR)
	"워킹 런지":          "Walking Lunge",
	"워킹런지":           "Walking Lunge",
	"리버스 런지":         "Reverse Lunge",
	"리버스런지":          "Reverse Lunge",
	"백 런지":           "Reverse Lunge",
	"백런지":            "Reverse Lunge",
	"점핑 런지":          "Jumping Lunge",
	"점핑런지":           "Jumping Lunge",
	"점프 런지":          "Jumping Lunge",
	"점프런지":           "Jumping Lunge",
	"중량 런지":          "Weighted Lunge",
	"웨이트 런지":         "Weighted Lunge",
	"중량 워킹 런지":       "Weighted Walking Lunge",
	"웨이트 워킹 런지":      "Weighted Walking Lunge",
	"샌드백 런지":         "Sandbag Lunge",
	"샌드백런지":          "Sandbag Lunge",
	"샌드백 워킹 런지":      "Sandbag Walking Lunge",
	"샌드백워킹런지":        "Sandbag Walking Lunge",
	"바벨 런지":          "Barbell Lunge",
	"바벨런지":           "Barbell Lunge",
	"바벨 워킹 런지":       "Barbell Walking Lunge",
	"바벨워킹런지":         "Barbell Walking Lunge",
	"백랙 런지":          "Back Rack Lunge",
	"백랙런지":           "Back Rack Lunge",
	"백 랙 런지":         "Back Rack Lunge",
	"백랙 워킹 런지":       "Back Rack Walking Lunge",
	"백랙워킹런지":         "Back Rack Walking Lunge",
	"백 랙 워킹 런지":      "Back Rack Walking Lunge",
	"프론트랙 런지":        "Front Rack Lunge",
	"프론트랙런지":         "Front Rack Lunge",
	"프론트 랙 런지":       "Front Rack Lunge",
	"프런트랙 런지":        "Front Rack Lunge",
	"프론트랙 워킹 런지":     "Front Rack Walking Lunge",
	"프론트랙워킹런지":       "Front Rack Walking Lunge",
	"프론트 랙 워킹 런지":    "Front Rack Walking Lunge",
	"프런트랙 워킹 런지":     "Front Rack Walking Lunge",
	"오버헤드 런지":        "Overhead Lunge",
	"오버헤드런지":         "Overhead Lunge",
	"오버헤드 워킹 런지":     "Overhead Walking Lunge",
	"오버헤드워킹런지":       "Overhead Walking Lunge",
	"덤벨 런지":          "DB Lunge",
	"덤벨런지":           "DB Lunge",
	"덤벨 워킹 런지":       "DB Walking Lunge",
	"덤벨워킹런지":         "DB Walking Lunge",
	"덤벨 리버스 런지":      "DB Reverse Lunge",
	"덤벨리버스런지":        "DB Reverse Lunge",
	"덤벨 프론트랙 런지":     "DB Front Rack Lunge",
	"덤벨프론트랙런지":       "DB Front Rack Lunge",
	"덤벨 프론트 랙 런지":    "DB Front Rack Lunge",
	"덤벨 프론트랙 워킹 런지":  "DB Front Rack Walking Lunge",
	"덤벨프론트랙워킹런지":    "DB Front Rack Walking Lunge",
	"덤벨 워킹 프론트랙 런지":  "DB Front Rack Walking Lunge",
	"덤벨 오버헤드 런지":     "DB Overhead Lunge",
	"덤벨오버헤드런지":       "DB Overhead Lunge",
	"덤벨 오버헤드 워킹 런지":  "DB Overhead Walking Lunge",
	"덤벨오버헤드워킹런지":    "DB Overhead Walking Lunge",
	"덤벨 워킹 오버헤드 런지":  "DB Overhead Walking Lunge",
	"케틀벨 런지":         "KB Lunge",
	"케틀벨런지":          "KB Lunge",
	"케틀벨 워킹 런지":      "KB Walking Lunge",
	"케틀벨워킹런지":        "KB Walking Lunge",
	"고블릿 런지":         "Goblet Lunge",
	"고블릿런지":          "Goblet Lunge",
	"고블릿 워킹 런지":      "Goblet Walking Lunge",
	"고블릿워킹런지":        "Goblet Walking Lunge",
	"케틀벨 프론트랙 런지":    "KB Front Rack Lunge",
	"케틀벨프론트랙런지":      "KB Front Rack Lunge",
	"케틀벨 프론트랙 워킹 런지": "KB Front Rack Walking Lunge",
	"케틀벨프론트랙워킹런지":   "KB Front Rack Walking Lunge",
	"케틀벨 오버헤드 런지":    "KB Overhead Lunge",
	"케틀벨오버헤드런지":      "KB Overhead Lunge",
	"케틀벨 오버헤드 워킹 런지": "KB Overhead Walking Lunge",
	"케틀벨오버헤드워킹런지":   "KB Overhead Walking Lunge",
	"케틀벨 워킹 오버헤드 런지": "KB Overhead Walking Lunge",
	"버피 브로드 점프":      "Burpee Broad Jump",
	"버피브로드점프":        "Burpee Broad Jump",
	"버피 멀리뛰기":        "Burpee Broad Jump",
}

var stationaryToWalkingLunge = map[string]string{
	"Lunge":               "Walking Lunge",
	"Weighted Lunge":      "Weighted Walking Lunge",
	"Sandbag Lunge":       "Sandbag Walking Lunge",
	"Barbell Lunge":       "Barbell Walking Lunge",
	"Back Rack Lunge":     "Back Rack Walking Lunge",
	"Front Rack Lunge":    "Front Rack Walking Lunge",
	"Overhead Lunge":      "Overhead Walking Lunge",
	"DB Lunge":            "DB Walking Lunge",
	"DB Front Rack Lunge": "DB Front Rack Walking Lunge",
	"DB Overhead Lunge":   "DB Overhead Walking Lunge",
	"KB Lunge":            "KB Walking Lunge",
	"Goblet Lunge":        "Goblet Walking Lunge",
	"KB Front Rack Lunge": "KB Front Rack Walking Lunge",
	"KB Overhead Lunge":   "KB Overhead Walking Lunge",
}

var walkingLungeMovements = map[string]bool{
	"Walking Lunge":               true,
	"Weighted Walking Lunge":      true,
	"Sandbag Walking Lunge":       true,
	"Barbell Walking Lunge":       true,
	"Back Rack Walking Lunge":     true,
	"Front Rack Walking Lunge":    true,
	"Overhead Walking Lunge":      true,
	"DB Walking Lunge":            true,
	"DB Front Rack Walking Lunge": true,
	"DB Overhead Walking Lunge":   true,
	"KB Walking Lunge":            true,
	"Goblet Walking Lunge":        true,
	"KB Front Rack Walking Lunge": true,
	"KB Overhead Walking Lunge":   true,
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

// IsWalkingLunge reports whether raw resolves to one of the canonical walking lunge movements.
func IsWalkingLunge(raw string) bool {
	c := Canonical(raw)
	return c != "" && walkingLungeMovements[c]
}

// IsTraveling reports whether raw resolves to a floor-traversing movement without a fixed station.
func IsTraveling(raw string) bool {
	c := Canonical(raw)
	if c == "" {
		return false
	}
	if walkingLungeMovements[c] {
		return true
	}
	switch c {
	case "Burpee Broad Jump", "Broad Jump", "Handstand Walk", "Farmer's Carry":
		return true
	default:
		return false
	}
}

// IsTravelingPolicyMovement reports whether raw resolves to a movement governed by the
// conditional traveling-lunge / burpee-broad-jump observation policy.
func IsTravelingPolicyMovement(raw string) bool {
	c := Canonical(raw)
	return walkingLungeMovements[c] || c == "Burpee Broad Jump"
}

var (
	explicitDistanceUnitRegex = regexp.MustCompile(`(?i)(?:\b\d+(?:\.\d+)?\s*(?:ft|feet|foot|yd|yds|yard|yards|meter|meters)\b|\d+(?:\.\d+)?\s*(?:미터|피트))`)
	meterTokenRegex           = regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?\s*m\b`)
	timeHeaderPrefixRegex     = regexp.MustCompile(`(?i)(?:\bamrap|\bemom|\be\d+mom|\botm|\btime\s*cap|\bcap|\bevery|\brest|\bin|\bfor)\s*[:\-]?\s*$`)
	clauseSplitRegex          = regexp.MustCompile(`[\r\n;:+,]+`)
	parenOrAtLoadRegex        = regexp.MustCompile(`(?:\([^)]*\)|@[^\r\n,;:+]*)`)
	prescriptionNumberRegex   = regexp.MustCompile(`(?i)(?:\b\d+(?:/\d+)?(?:\.\d+)?\s*(?:ft|feet|foot|yd|yds|yard|yards|meter|meters|m|lb|lbs|kg|kgs|reps?|cal|cals|calories?|in|inch|inches)?\b|\d+(?:\.\d+)?\s*(?:미터|피트|회|개))`)
	nonWordPunctRegex         = regexp.MustCompile(`[#"'\[\]/\\]+`)
)

// HasLungeDistancePrescription reports whether a single prescription clause contains a
// horizontal distance unit (e.g. 200ft, 25m, 50yd) rather than a time/rep/weight token.
func HasLungeDistancePrescription(clause string) bool {
	trimmed := strings.TrimSpace(clause)
	if trimmed == "" {
		return false
	}
	if explicitDistanceUnitRegex.MatchString(trimmed) {
		return true
	}
	for _, loc := range meterTokenRegex.FindAllStringIndex(trimmed, -1) {
		prefix := trimmed[:loc[0]]
		if !timeHeaderPrefixRegex.MatchString(prefix) {
			return true
		}
	}
	return false
}

func resolveLungeBaseFromClause(clause string) string {
	norm := NormalizeKey(clause)
	if norm == "" || strings.Contains(norm, "stretch") || strings.Contains(clause, "스트레칭") {
		return ""
	}
	if c := Canonical(clause); c != "" {
		if walkingLungeMovements[c] {
			return c
		}
		if _, ok := stationaryToWalkingLunge[c]; ok {
			return c
		}
	}
	cleaned := parenOrAtLoadRegex.ReplaceAllString(clause, " ")
	cleaned = prescriptionNumberRegex.ReplaceAllString(cleaned, " ")
	cleaned = nonWordPunctRegex.ReplaceAllString(cleaned, " ")
	if c := Canonical(cleaned); c != "" {
		if walkingLungeMovements[c] {
			return c
		}
		if _, ok := stationaryToWalkingLunge[c]; ok {
			return c
		}
	}
	cleanedNorm := " " + NormalizeKey(cleaned) + " "
	bestLen := 0
	bestCanon := ""
	check := func(phrase, canon string) {
		if !walkingLungeMovements[canon] {
			if _, ok := stationaryToWalkingLunge[canon]; !ok {
				return
			}
		}
		for _, candidate := range []string{phrase, phrase + "s", phrase + "es"} {
			if strings.Contains(cleanedNorm, " "+candidate+" ") &&
				(len(candidate) > bestLen || (len(candidate) == bestLen && (bestCanon == "" || canon < bestCanon))) {
				bestLen = len(candidate)
				bestCanon = canon
			}
		}
	}
	for k, c := range canonicalLookup {
		check(k, c)
	}
	for k, c := range aliasLookup {
		check(k, c)
	}
	return bestCanon
}

// PromoteDistanceLunge returns the canonical * Walking Lunge name when rawMovement (or an
// accompanying prescription clause such as NormalizedMovement.Reps) prescribes a distance lunge.
// Returns "" when no distance-lunge promotion applies, leaving non-promoted inputs untouched.
func PromoteDistanceLunge(rawMovement string, clauseContexts ...string) string {
	base := resolveLungeBaseFromClause(rawMovement)
	if base == "" {
		return ""
	}
	hasDistance := HasLungeDistancePrescription(rawMovement)
	if !hasDistance {
		for _, ctx := range clauseContexts {
			if HasLungeDistancePrescription(ctx) {
				hasDistance = true
				break
			}
		}
	}
	if !hasDistance {
		return ""
	}
	if walkingLungeMovements[base] {
		return base
	}
	return stationaryToWalkingLunge[base]
}

// PromoteDistanceLungeInWOD promotes rawMovement to its canonical * Walking Lunge counterpart
// when rawMovement itself or a matching lunge clause in texts carries a distance prescription.
// Returns "" if rawMovement is not a distance-prescribed lunge.
func PromoteDistanceLungeInWOD(rawMovement string, texts ...string) string {
	if promoted := PromoteDistanceLunge(rawMovement); promoted != "" {
		return promoted
	}
	base := resolveLungeBaseFromClause(rawMovement)
	if base == "" {
		return ""
	}
	targetWalking := base
	if !walkingLungeMovements[base] {
		var ok bool
		targetWalking, ok = stationaryToWalkingLunge[base]
		if !ok {
			return ""
		}
	}
	for _, text := range texts {
		for _, clause := range clauseSplitRegex.Split(text, -1) {
			if !HasLungeDistancePrescription(clause) {
				continue
			}
			clauseBase := resolveLungeBaseFromClause(clause)
			if clauseBase == base || clauseBase == targetWalking {
				return targetWalking
			}
		}
	}
	return ""
}

// HasTravelingPolicyContext reports whether any explicit movement hint or WOD description
// clause references a traveling lunge (* Walking Lunge) or Burpee Broad Jump.
func HasTravelingPolicyContext(movements []string, wodDescription string) bool {
	for _, m := range movements {
		if IsTravelingPolicyMovement(m) || PromoteDistanceLungeInWOD(m, wodDescription) != "" {
			return true
		}
	}
	if strings.TrimSpace(wodDescription) == "" {
		return false
	}
	for _, clause := range clauseSplitRegex.Split(wodDescription, -1) {
		norm := " " + NormalizeKey(clause) + " "
		if strings.Contains(norm, " burpee broad jump ") || strings.Contains(norm, " burpee broad jumps ") || strings.Contains(norm, " bbj ") || strings.Contains(clause, "버피 브로드 점프") || strings.Contains(clause, "버피브로드점프") || strings.Contains(clause, "버피 멀리뛰기") {
			return true
		}
		if base := resolveLungeBaseFromClause(clause); base != "" {
			if walkingLungeMovements[base] || HasLungeDistancePrescription(clause) {
				return true
			}
		}
	}
	return false
}
