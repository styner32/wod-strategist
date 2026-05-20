package worker

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/db"
)

// ScoreOutputPrompt instructs the model to emit a structured score JSON block at the end
// of the analysis. Scores are anchored to absolute CrossFit standards for the user's fitness
// level — NOT relative to the user's own past performance (to prevent grade inflation).
const ScoreOutputPrompt = `

## 세션 점수 (Session Score)
분석이 끝나면 반드시 아래 형식의 **score** JSON 블록을 출력하세요.

**점수 규칙:**
- **절대 기준**: 이 사용자의 피트니스 레벨(초급/중급/고급) 기준 CrossFit 표준으로 평가하세요.
- **WOD 타입 반영**: "For Time"이면 강도(intensity)를 높게 가중합니다. EMOM·스킬 WOD이면 자세(form)를 높게 가중합니다.
- **점수 인플레이션 금지**: 사용자가 최선을 다했다는 이유로 점수를 올리지 마세요. 기준은 항상 절대적입니다.

` + "```score\n" + `{
  "overall": 0,
  "form": 0,
  "intensity": 0,
  "consistency": 0,
  "movements": {},
  "summary": "한 문장 요약 (한국어, 오늘 세션의 핵심 평가)"
}` + "\n```\n" + `
- overall: form×0.5 + intensity×0.3 + consistency×0.2 (스킬 WOD는 form×0.7 + 나머지 0.3)
- form: 자세 정확도 및 가동 범위 (0=매우 나쁨, 100=완벽한 표준 기술)
- intensity: 수행 강도와 페이스 (해당 레벨 기준 얼마나 열심히 하는가)
- consistency: 세트·라운드 간 동작의 일관성
- movements: 관찰된 종목별 form + intensity (예: {"Snatch": {"form": 65, "intensity": 80}})
- summary: 이번 세션 핵심 한 줄 요약 (한국어)`

// scoreBlockRegex matches ```score ... ``` fenced blocks in model output.
var scoreBlockRegex = regexp.MustCompile("(?is)```score\\s*(\\{.*?\\})\\s*```")

// SessionScore is the parsed structure of the score JSON block.
type SessionScore struct {
	Overall     int                        `json:"overall"`
	Form        int                        `json:"form"`
	Intensity   int                        `json:"intensity"`
	Consistency int                        `json:"consistency"`
	Movements   map[string]map[string]int  `json:"movements"`
	Summary     string                     `json:"summary"`
}

// parseSessionScore extracts the ```score {...} ``` JSON block from model output.
// Returns "{}" if no block is found or the JSON is malformed.
func parseSessionScore(output string) string {
	match := scoreBlockRegex.FindStringSubmatch(output)
	if len(match) < 2 {
		return "{}"
	}
	raw := strings.TrimSpace(match[1])
	// Validate it's parseable JSON with the expected structure
	var score SessionScore
	if err := json.Unmarshal([]byte(raw), &score); err != nil {
		return "{}"
	}
	// Re-marshal to ensure compact, consistent output
	out, err := json.Marshal(score)
	if err != nil {
		return "{}"
	}
	return string(out)
}

// buildWODContext returns a Korean prompt section describing the WOD if wod_description is set.
// For named WODs (Fran, Grace, etc.) it adds known benchmark context.
// For custom WODs it adds round/rep structure context.
// Returns "" if wod_description is empty.
func buildWODContext(wodDescription string) string {
	if strings.TrimSpace(wodDescription) == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## 오늘의 WOD\n")
	sb.WriteString(wodDescription)

	// Named WOD benchmark hints
	lower := strings.ToLower(strings.TrimSpace(wodDescription))
	switch lower {
	case "fran":
		sb.WriteString(`
→ Fran: 21-15-9 Thrusters (43/30kg) + Pull-ups, For Time
→ 엘리트 기준: ~2분, 중급: ~5-7분, 초급: ~10분 이하
→ 짧고 강도 높은 구성 — 강도(intensity)와 페이스 유지를 중점 평가하세요.`)
	case "grace":
		sb.WriteString(`
→ Grace: 30 Clean & Jerks (61/43kg), For Time
→ 엘리트 기준: ~1분 30초, 중급: ~4-6분, 초급: ~8분 이하
→ 단순 반복 구성 — 세트 간 케이던스 일관성을 중점 평가하세요.`)
	case "helen":
		sb.WriteString(`
→ Helen: 3 rounds — 400m Run + 21 KB Swings (24/16kg) + 12 Pull-ups, For Time
→ 엘리트 기준: ~8분, 중급: ~12-15분, 초급: ~20분 이하
→ 라운드별 페이스 유지와 피로 누적을 추적하세요.`)
	case "murph":
		sb.WriteString(`
→ Murph: 1 Mile Run + 100 Pull-ups + 200 Push-ups + 300 Air Squats + 1 Mile Run, For Time (조끼 선택)
→ 볼륨이 매우 높음 — 자세 붕괴 시점과 파티셔닝 전략을 평가하세요.`)
	case "cindy":
		sb.WriteString(`
→ Cindy: AMRAP 20 — 5 Pull-ups + 10 Push-ups + 15 Air Squats
→ AMRAP 구성 — 완료 라운드 수와 라운드 후반 페이스를 평가하세요.`)
	case "annie":
		sb.WriteString(`
→ Annie: 50-40-30-20-10 Double-Unders + Sit-ups, For Time
→ 점프로프 기술과 코어 내구성을 중점 평가하세요.`)
	default:
		// Custom WOD — infer type from description
		lowerDesc := strings.ToLower(wodDescription)
		switch {
		case strings.Contains(lowerDesc, "for time") && containsRoundsPattern(lowerDesc):
			rounds := extractRoundsHint(wodDescription)
			sb.WriteString(fmt.Sprintf(`
→ For Time 구성 (다중 라운드) — 강도와 라운드별 자세 일관성을 추적하세요.
→ %s후반 라운드에서 자세 붕괴가 시작되는 시점에 주목하세요.`, rounds))
		case strings.Contains(lowerDesc, "for time"):
			sb.WriteString(`
→ For Time 구성 — 강도와 페이스를 중점 평가하세요.`)
		case strings.Contains(lowerDesc, "amrap"):
			sb.WriteString(`
→ AMRAP 구성 — 완료 라운드/반복 수와 페이스 유지를 평가하세요.`)
		case strings.Contains(lowerDesc, "emom"):
			sb.WriteString(`
→ EMOM 구성 — 매 인터벌 내 자세 완성도와 미션 완료 여부를 평가하세요.
→ 자세(form)를 가장 높게 가중하세요.`)
		case strings.Contains(lowerDesc, "1rm") || strings.Contains(lowerDesc, "max") || strings.Contains(lowerDesc, "skill"):
			sb.WriteString(`
→ 스킬/강도(Strength/Skill) WOD — 자세(form)를 최우선으로 평가하세요. 페이스는 부차적입니다.`)
		default:
			sb.WriteString(`
→ WOD 구성을 참고하여 적절한 평가 기준을 적용하세요.`)
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// containsRoundsPattern returns true if the description mentions rounds.
func containsRoundsPattern(s string) bool {
	return strings.Contains(s, "round") || strings.Contains(s, "라운드") || strings.Contains(s, "rds") || strings.Contains(s, "rnd")
}

// extractRoundsHint returns a short rounds hint string for multi-round WODs.
func extractRoundsHint(description string) string {
	for _, suffix := range []string{" rounds", " rds", " rnds", " 라운드"} {
		for _, n := range []string{"3", "4", "5", "6", "7", "8", "9", "10"} {
			if strings.Contains(strings.ToLower(description), n+suffix) ||
				strings.Contains(strings.ToLower(description), n+" "+strings.TrimSpace(suffix)) {
				return n + suffix + " 구성 — "
			}
		}
	}
	return ""
}

// recentScoreRow holds the data needed to build one history table row.
type recentScoreRow struct {
	date        string
	wodDesc     string
	overall     int
	form        int
	intensity   int
	consistency int
}

// buildHistoryContext queries the last [limit] completed, non-archived sessions for profileID
// that have a non-empty session_score. Returns a compact Korean markdown table for prompt
// injection, or "" if no history is available.
func (w *Worker) buildHistoryContext(profileID uint, limit int) string {
	if w.DB == nil || profileID == 0 {
		return ""
	}

	var results []db.AnalysisResult
	err := w.DB.
		Where("profile_id = ? AND status = ? AND archived_at IS NULL AND session_score != '{}'",
			profileID, "COMPLETED").
		Order("created_at DESC").
		Limit(limit).
		Find(&results).Error
	if err != nil || len(results) == 0 {
		return ""
	}

	var rows []recentScoreRow
	for _, r := range results {
		var score SessionScore
		if jsonErr := json.Unmarshal([]byte(r.SessionScore), &score); jsonErr != nil {
			continue
		}
		if score.Overall == 0 && score.Form == 0 {
			continue // skip zero scores (likely parse failures)
		}
		date := r.CreatedAt.In(time.FixedZone("KST", 9*60*60)).Format("01/02")
		wod := r.WODDescription
		if wod == "" {
			wod = "WOD"
		}
		if len(wod) > 20 {
			wod = wod[:20] + "…"
		}
		rows = append(rows, recentScoreRow{
			date:        date,
			wodDesc:     wod,
			overall:     score.Overall,
			form:        score.Form,
			intensity:   score.Intensity,
			consistency: score.Consistency,
		})
	}

	if len(rows) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## 최근 세션 성과 기록 (참고용)\n")
	sb.WriteString("| 날짜 | WOD | 종합 | 자세 | 강도 | 일관성 |\n")
	sb.WriteString("|------|-----|:----:|:----:|:----:|:------:|\n")
	for _, row := range rows {
		sb.WriteString(fmt.Sprintf("| %s | %s | %d | %d | %d | %d |\n",
			row.date, row.wodDesc, row.overall, row.form, row.intensity, row.consistency))
	}
	sb.WriteString("\n이 점수들을 참고하여 오늘 세션과 비교 분석하세요. 단, 오늘 점수는 절대 기준으로 새로 산정하세요.\n")
	return sb.String()
}
