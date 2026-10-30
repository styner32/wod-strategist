package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/movement"
	"go.uber.org/zap"
)

var (
	movementsBlockRegex         = regexp.MustCompile("```movements\\s*([\\s\\S]*?)\\s*```")
	clauseParenWeightRegex       = regexp.MustCompile(`(?i)\(([^)]*(?:lb|lbs|kg|kgs|\d+\s*/\s*\d+)[^)]*)\)|@\s*([^\s,;:+|]+(?:\s*(?:lb|lbs|kg|kgs))?)`)
	clauseAnyParenOrAtRegex      = regexp.MustCompile(`(?:\([^)]*\)|@[^\r\n,;:+|]*)`)
	clauseLeadingPrescriptionRe = regexp.MustCompile(`(?i)^\s*(\d+(?:[-/x]\d+)*(?:\.\d+)?\s*(?:ft|feet|foot|yd|yds|yard|yards|meter|meters|metres|m|cal|cals|calories?|reps?|회|개|미터|피트|야드)?)\s+(.+?)\s*$`)
	clauseTrailingPrescriptionRe = regexp.MustCompile(`(?i)^\s*(.+?)\s+(\d+(?:[-/x]\d+)*(?:\.\d+)?\s*(?:ft|feet|foot|yd|yds|yard|yards|meter|meters|metres|m|cal|cals|calories?|reps?|회|개|미터|피트|야드)?)\s*$`)
	singleWeightValueRegex      = regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*(lb|lbs|kg|kgs)\s*$`)
)

// BuildNormalizedWorkoutPrompt creates the prompt for normalizing WOD description into structured movements.
func BuildNormalizedWorkoutPrompt(wodDescription string, hints []string, gender string) string {
	catalogList := strings.Join(movement.All(), ", ")

	var hintsText string
	if len(hints) > 0 {
		hintsText = fmt.Sprintf("\n사용자 지정 입력 힌트: %s\n", strings.Join(hints, ", "))
	}

	var genderText string
	switch strings.ToLower(gender) {
	case "male":
		genderText = "남성 (male)"
	case "female":
		genderText = "여성 (female)"
	default:
		genderText = "미지정 (unspecified)"
	}

	prompt := `당신은 크로스핏 WOD 텍스트 분석 및 운동 정규화 전문가입니다.
아래 제공된 운동 설명(wod_description)을 해석하여 개별 운동 목록을 표준 구조로 추출하세요.

## 공식 운동 카탈로그
[` + catalogList + `]
` + hintsText + `
## 규칙
1. **운동명 (movement)**: 공식 카탈로그 이름과 정확히 일치시켜야 합니다. 카탈로그에 없는 경우 약어를 교정하거나 원문 이름을 유지하세요.
   - 한 줄에 "/", "+", ",", "·", "|" 등으로 여러 운동이 함께 적힌 경우(예: "400m Run / 20 DB Lunges", "500m Row + 15 Thrusters")에는 합치지 말고 반드시 각 운동을 별도의 배열 항목으로 분리하세요. 단, "50/35lb", "70/45kg", "20/15 cal"처럼 무게·횟수·칼로리 비율을 나타내는 "/"는 운동 구분자가 아닙니다.
   - 거리 단위(ft, m, yd, 미터, 피트 등 — 예: "200ft dumbbell overhead lunge", "50m front rack lunge")가 해당 런지 절에 직접 처방된 경우에만 보행형 카탈로그 이름("* Walking Lunge", 예: "DB Overhead Walking Lunge", "Front Rack Walking Lunge", "Walking Lunge")으로 변환하세요. 앞이나 뒤에 함께 적힌 다른 운동의 거리(예: "400m Run / 20 DB Lunges"의 "400m")는 런지 거리가 아니므로 거리 단위 없이 횟수만 처방된 런지는 반드시 제자리/교대형 이름(예: "DB Lunge", "DB Overhead Lunge", "Lunge")을 사용하세요.
   - 약어 및 오타 교정 규칙:
     - "Thuster" → "Thruster"
     - "PU" → "Pull-up"
     - "DL" → "Deadlift"
     - "KB" → "Kettlebell"
     - "BJ" → "Box Jump"
     - "HSPU" / "HSPUs" → "Handstand Push-up"
     - "C2B" → "Chest to Bar"
     - "T2B" → "Toes to Bar"
     - "MU" → "Muscle-up"
     - "DU" → "Double Under"
     - "SU" → "Single Under"
     - "WB" → "Wallball Shot"
     - "S2OH" → "Shoulder to Overhead"
     - "G2OH" → "Ground to Overhead"
     - "PC" → "Power Clean"
     - "SC" → "Squat Clean"
     - "HPC" → "Hang Power Clean"
     - "HSC" → "Hang Squat Clean"
     - "HC" → "Hang Power Clean"
     - "PS" → "Power Snatch"
     - "HPS" → "Hang Power Snatch"
     - "HSS" → "Hang Squat Snatch"
     - "HS" → "Hang Power Snatch"
     - "PP" → "Push Press"
     - "PJ" → "Push Jerk"
     - "SJ" → "Split Jerk"
     - "FS" → "Front Squat"
     - "BS" → "Back Squat"
     - "OHS" → "Overhead Squat"
     - "SDHP" → "Sumo Deadlift High Pull"
     - "RDL" → "Romanian Deadlift"
2. **무게 (weight_raw, weight_kg)**:
   - "weight_raw": 원본에 기술된 무게 텍스트 그대로 (예: "135lb", "70/45kg").
   - "weight_kg":
     - 단일 무게인 경우 kg 단위 float로 변환 (lb는 1lb = 0.45359237kg로 변환하여 소수점 첫째자리 환산, 예: 135lb → 61.2).
     - "70/45kg" 또는 "135/95lb" 같이 남/녀 구분 무게(Rx male / female)가 표시된 경우 (사용자 프로필 성별: ` + genderText + `):
       - 사용자 성별이 남성(male)이면 남성 무게(첫 번째 무게, 예: 70kg / 135lb)를 선택해 kg 단위 float로 변환하세요.
       - 사용자 성별이 여성(female)이면 여성 무게(두 번째 무게, 예: 45kg / 95lb)를 선택해 kg 단위 float로 변환하세요.
       - 사용자 성별이 미지정이거나 알 수 없는 경우 weight_kg는 null로 설정하세요.
3. **반복수 (reps)**: 원본의 반복수/라운드 구조 텍스트 그대로 (예: "21-15-9", "5x5", "10", "15").
4. **주요 운동 여부 (is_main)**:
   - **반드시 전체 추출된 운동 중 정확히 1개의 운동만 "is_main": true**로 설정해야 합니다 (나머지는 false).
   - 스트렝스/선행 리프팅 동작이 있는 경우 해당 동작을 main으로 지정합니다.
   - 단일 메트콘인 경우 가장 주력이 되는/고부하 리프팅 동작을 main으로 지정합니다.
   - Run, Row, Bike 등 유산소/단일 구조 동작은 다른 리프팅 동작이 함께 있는 한 main이 될 수 없습니다. (단, 유산소만 있는 WOD인 경우는 예외)
   - 판단이 애매한 경우 첫 번째 운동을 main으로 지정합니다.
5. 운동을 찾을 수 없는 경우 빈 배열 []을 반환합니다.

## 분석 대상 WOD
` + wodDescription + `

## 출력 형식
반드시 아래 JSON 구조를 ` + "```movements```" + ` 코드 블록 안에 작성하세요:

` + "```movements" + `
{
  "movements": [
    {
      "movement": "power clean",
      "weight_raw": "135lb",
      "weight_kg": 61.2,
      "reps": "5",
      "is_main": true
    }
  ]
}
` + "```"

	return prompt
}

type movementsJSONWrapper struct {
	Movements []db.NormalizedMovement `json:"movements"`
}

// ParseNormalizedWorkoutOutput parses a fenced ```movements ``` JSON block.
func ParseNormalizedWorkoutOutput(raw string) ([]db.NormalizedMovement, error) {
	match := movementsBlockRegex.FindStringSubmatch(raw)
	if len(match) < 2 {
		// Fallback: try parsing direct JSON if not enclosed in backticks
		var wrapper movementsJSONWrapper
		if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &wrapper); err == nil && len(wrapper.Movements) > 0 {
			items := splitCombinedNormalizedMovements(wrapper.Movements)
			promoteDistanceLungesInNormalizedMovements(items, "")
			return items, nil
		}
		return nil, fmt.Errorf("no fenced movements JSON block found in output")
	}

	jsonContent := strings.TrimSpace(match[1])
	var wrapper movementsJSONWrapper
	if err := json.Unmarshal([]byte(jsonContent), &wrapper); err != nil {
		return nil, fmt.Errorf("failed to unmarshal movements JSON: %w", err)
	}

	items := splitCombinedNormalizedMovements(wrapper.Movements)
	promoteDistanceLungesInNormalizedMovements(items, "")
	return items, nil
}

func isCardioMonostructural(canon string) bool {
	switch canon {
	case "Run", "Shuttle Run", "Row", "Echo Bike", "Bike Erg", "Skierg", "Double-under", "Single-under":
		return true
	default:
		return false
	}
}

func isUnweightedMovement(canon string) bool {
	if isCardioMonostructural(canon) {
		return true
	}
	switch canon {
	case "Burpee", "Burpee Broad Jump", "Broad Jump", "Pull-up", "Chest to Bar", "Bar Muscle-up", "Ring Muscle-up",
		"Toes to Bar", "Handstand Push-up", "Handstand Walk", "Rope Climb", "Ring Dip", "Pistol", "Wall Walk",
		"GHD Sit-up", "Push-up", "Air Squat", "Sit-up", "Lunge", "Walking Lunge", "Reverse Lunge", "Jumping Lunge",
		"Box Jump", "Box Jump Over", "Burpee Box Jump Over", "Box Step-up", "Box Step-over":
		return true
	default:
		return false
	}
}

func parseSingleClauseMovement(clause string) (db.NormalizedMovement, bool) {
	var weightRaw string
	var weightKG *float64
	if wm := clauseParenWeightRegex.FindStringSubmatch(clause); len(wm) > 0 {
		weightRaw = strings.TrimSpace(wm[1])
		if weightRaw == "" {
			weightRaw = strings.TrimSpace(wm[2])
		}
		if sm := singleWeightValueRegex.FindStringSubmatch(weightRaw); len(sm) == 3 {
			if val, err := strconv.ParseFloat(sm[1], 64); err == nil {
				if strings.HasPrefix(strings.ToLower(sm[2]), "lb") {
					val = math.Round(val*0.45359237*10) / 10
				} else {
					val = math.Round(val*10) / 10
				}
				weightKG = &val
			}
		}
	}

	body := strings.TrimSpace(clauseAnyParenOrAtRegex.ReplaceAllString(clause, " "))
	if body == "" {
		return db.NormalizedMovement{}, false
	}

	var reps string
	var movName string

	if promoted := movement.PromoteDistanceLunge(body); promoted != "" {
		movName = promoted
		if lm := clauseLeadingPrescriptionRe.FindStringSubmatch(body); len(lm) == 3 {
			reps = strings.TrimSpace(lm[1])
		} else if tm := clauseTrailingPrescriptionRe.FindStringSubmatch(body); len(tm) == 3 {
			reps = strings.TrimSpace(tm[2])
		}
	} else if canon := movement.Canonical(body); canon != "" {
		movName = canon
	} else if lm := clauseLeadingPrescriptionRe.FindStringSubmatch(body); len(lm) == 3 {
		if promoted := movement.PromoteDistanceLunge(body); promoted != "" {
			movName = promoted
			reps = strings.TrimSpace(lm[1])
		} else if canon := movement.Canonical(lm[2]); canon != "" {
			movName = canon
			reps = strings.TrimSpace(lm[1])
		}
	}
	if movName == "" {
		if tm := clauseTrailingPrescriptionRe.FindStringSubmatch(body); len(tm) == 3 {
			if canon := movement.Canonical(tm[1]); canon != "" {
				movName = canon
				reps = strings.TrimSpace(tm[2])
			}
		}
	}
	if movName == "" {
		return db.NormalizedMovement{}, false
	}

	return db.NormalizedMovement{
		Movement:  movName,
		Reps:      reps,
		WeightRaw: weightRaw,
		WeightKG:  weightKG,
	}, true
}

func splitCombinedNormalizedMovements(items []db.NormalizedMovement) []db.NormalizedMovement {
	if len(items) == 0 {
		return items
	}
	out := make([]db.NormalizedMovement, 0, len(items))
	for _, item := range items {
		clauses := movement.SplitWorkoutClauses(item.Movement)
		if len(clauses) <= 1 {
			out = append(out, item)
			continue
		}
		parsed := make([]db.NormalizedMovement, 0, len(clauses))
		allValid := true
		hasInlineWeight := false
		for _, clause := range clauses {
			p, ok := parseSingleClauseMovement(clause)
			if !ok {
				allValid = false
				break
			}
			if p.WeightRaw != "" {
				hasInlineWeight = true
			}
			parsed = append(parsed, p)
		}
		if !allValid || len(parsed) < 2 {
			out = append(out, item)
			continue
		}
		if !hasInlineWeight && (item.WeightRaw != "" || item.WeightKG != nil) {
			for i := range parsed {
				if !isUnweightedMovement(parsed[i].Movement) {
					parsed[i].WeightRaw = item.WeightRaw
					parsed[i].WeightKG = item.WeightKG
				}
			}
		}
		if item.Reps != "" {
			allEmptyReps := true
			for i := range parsed {
				if parsed[i].Reps != "" {
					allEmptyReps = false
					break
				}
			}
			if allEmptyReps {
				for i := range parsed {
					parsed[i].Reps = item.Reps
				}
			}
		}
		if item.IsMain {
			mainIdx := 0
			for i := range parsed {
				if !isCardioMonostructural(parsed[i].Movement) {
					mainIdx = i
					break
				}
			}
			parsed[mainIdx].IsMain = true
		}
		out = append(out, parsed...)
	}
	return out
}

func promoteDistanceLungesInNormalizedMovements(items []db.NormalizedMovement, wodDescription string) {
	for i := range items {
		if promoted := movement.PromoteDistanceLunge(items[i].Movement, items[i].Reps); promoted != "" {
			items[i].Movement = promoted
			continue
		}
		if wodDescription != "" {
			if promoted := movement.PromoteDistanceLungeInWOD(items[i].Movement, wodDescription); promoted != "" {
				items[i].Movement = promoted
			}
		}
	}
}

func (w *Worker) lookupGender(profileID uint) string {
	if profileID > 0 && w.DB != nil {
		var profile db.Profile
		if err := w.DB.First(&profile, profileID).Error; err == nil && profile.Gender != nil {
			return *profile.Gender
		}
	}
	return ""
}

// normalizeWorkout runs text-based Gemini parsing to extract normalized workout movements.
// On any error or empty result, it logs a warning and returns "" without failing analysis.
func (w *Worker) normalizeWorkout(ctx context.Context, sessionID string, profileID uint, wodDescription string, hints []string, observe ...func(string, *gemini.TokenUsage)) string {
	trimmedDesc := strings.TrimSpace(wodDescription)
	if trimmedDesc == "" {
		return ""
	}

	if w.DB != nil && sessionID != "" {
		var existing db.AnalysisResult
		if err := w.DB.WithContext(ctx).
			Where("session_id = ? AND profile_id = ? AND normalized_workout <> '' AND normalized_workout <> '[]'", sessionID, profileID).
			Order("created_at DESC, id DESC").
			First(&existing).Error; err == nil {
			if strings.TrimSpace(existing.WODDescription) == trimmedDesc {
				return existing.NormalizedWorkout
			}
		}
	}

	if w.GeminiClient == nil {
		w.logger.Warn("Gemini client not configured for workout normalization", zap.String("session_id", sessionID))
		return ""
	}

	gender := w.lookupGender(profileID)
	prompt := BuildNormalizedWorkoutPrompt(trimmedDesc, hints, gender)
	raw, usage, err := w.GeminiClient.ParseText(ctx, prompt)
	if len(observe) > 0 && observe[0] != nil {
		observe[0]("session:normalize-workout", usage)
	} else {
		w.saveTokenUsage(sessionID, profileID, "session:normalize-workout", usage)
	}
	if err != nil {
		w.logger.Warn("Failed to parse text for workout normalization",
			zap.String("session_id", sessionID),
			zap.Error(err))
		return ""
	}

	movements, err := ParseNormalizedWorkoutOutput(raw)
	if err != nil {
		w.logger.Warn("Failed to parse output JSON for workout normalization",
			zap.String("session_id", sessionID),
			zap.String("raw_output", raw),
			zap.Error(err))
		return ""
	}

	if len(movements) == 0 {
		return ""
	}
	promoteDistanceLungesInNormalizedMovements(movements, trimmedDesc)

	jsonBytes, err := json.Marshal(movements)
	if err != nil {
		w.logger.Warn("Failed to marshal normalized movements to JSON",
			zap.String("session_id", sessionID),
			zap.Error(err))
		return ""
	}

	return string(jsonBytes)
}
