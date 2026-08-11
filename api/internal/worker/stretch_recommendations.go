package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

var stretchCatalog = []string{
	"Pigeon Pose",
	"Couch Stretch",
	"Samson (Hip Flexor Lunge) Stretch",
	"Cossack Squat Hold",
	"Ankle Dorsiflexion Rock",
	"Calf Stretch",
	"Standing Forward Fold",
	"Hamstring Floss",
	"Child's Pose",
	"Downward Dog",
	"Thread the Needle",
	"Cat-Cow",
	"Thoracic Extension over Foam Roller",
	"Doorway Pec Stretch",
	"Wall Slide",
	"Wrist Flexor/Extensor Stretch",
	"Neck Lateral Stretch",
}

type StretchRecommendation struct {
	Stretch      string `json:"stretch"`
	TargetArea   string `json:"target_area"`
	Reason       string `json:"reason"`
	DurationHint string `json:"duration_hint,omitempty"`
	Caution      string `json:"caution,omitempty"`
	Provisional  bool   `json:"provisional"`
}

var stretchesBlockRegex = regexp.MustCompile("(?is)(?:```stretches\\s*(\\[.*?\\])\\s*```|```json\\s*(\\[.*?\\])\\s*```|<stretches>\\s*(\\[.*?\\])\\s*</stretches>)")

func BuildStretchRecommendationPrompt(current []MobilityObservation, history []db.MobilityRestriction, injuries []string) string {
	var evidenceSb strings.Builder
	evidenceSb.WriteString("## 관찰된 가동성 증거 데이터\n")

	if len(history) > 0 {
		evidenceSb.WriteString("### 누적 관찰 이력 (Cross-session History):\n")
		for _, h := range history {
			evidenceSb.WriteString(fmt.Sprintf("- Joint: %s (%s), Observation: %s, SessionCount: %d, Movements: %s\n",
				h.Joint, h.Side, h.Observation, h.SessionCount, strings.Join(h.Movements, ", ")))
		}
	}

	if len(current) > 0 {
		evidenceSb.WriteString("### 오늘 세션 관찰 항목 (Today's Observations):\n")
		for _, c := range current {
			evidenceSb.WriteString(fmt.Sprintf("- Joint: %s (%s), Observation: %s, Movement: %s, Evidence: %s\n",
				c.Joint, c.Side, c.Observation, c.Movement, c.Evidence))
		}
	}

	injuryText := "없음"
	if len(injuries) > 0 {
		injuryText = strings.Join(injuries, ", ")
	}

	return fmt.Sprintf(`
# 맞춤 스트레칭 추천 생성 요청

당신은 스포츠 재활 및 모빌리티 전문가입니다.
제시된 가동성 제한 근거 데이터를 바탕으로 사용자에게 필요한 **맞춤 스트레칭 팁**을 최대 3개 추천해 주세요.

%s

## 알려진 부상 사항
%s

## 추천 규칙:
1. **엄격한 근거 기반**: 오직 위에 제시된 관찰 증거에 있는 joint/observation에 대해서만 스트레칭을 추천하세요. 증거 없는 부위 추천은 엄격히 금지됩니다.
2. **스트레칭 카탈로그 제한**: stretch 필드는 아래 지원 목록 중 하나여야 합니다:
   - Pigeon Pose
   - Couch Stretch
   - Samson (Hip Flexor Lunge) Stretch
   - Cossack Squat Hold
   - Ankle Dorsiflexion Rock
   - Calf Stretch
   - Standing Forward Fold
   - Hamstring Floss
   - Child's Pose
   - Downward Dog
   - Thread the Needle
   - Cat-Cow
   - Thoracic Extension over Foam Roller
   - Doorway Pec Stretch
   - Wall Slide
   - Wrist Flexor/Extensor Stretch
   - Neck Lateral Stretch
3. **이유(reason) 및 임시(provisional) 기입 규칙**:
   - 누적 관찰 이력(SessionCount >= 2)에 기반한 경우: "최근 N개 세션의 [운동명]에서 [관절/제한]이 반복 관찰됨"과 같이 관찰 세션 수를 명시하고, provisional: false 로 기입하세요.
   - 오늘 처음 관찰되었거나 단일 세션(SessionCount == 1)인 경우: "오늘 세션에서 처음 관찰됨 — 추이를 지켜보세요" 등의 문구를 포함하고, provisional: true 로 기입하세요.
4. **부상 주의사항(caution)**: 추천 부위가 알려진 부상 부위와 겹칠 경우 caution 필드에 "통증 시 즉시 중단" 등 경고 문구를 포함하세요.
5. **의학적 진단 금지**: 통증이나 병명을 진단하지 마세요.
6. **최대 개수**: 최대 3개만 추천하세요. 근거가 부족하면 빈 배열 []을 출력하세요.

## 출력 형식:
반드시 아래 형식의 **stretches** JSON 코드 블록으로 출력하세요:
`+"```stretches\n"+`[
  {
    "stretch": "Pigeon Pose",
    "target_area": "Hip",
    "reason": "최근 3개 세션의 스쿼트에서 고관절 굴곡 제한이 반복 관찰됨",
    "duration_hint": "각 방향 60~90초",
    "caution": "",
    "provisional": false
  }
]`+"\n```\n", evidenceSb.String(), injuryText)
}

func parseStretchRecommendations(text string) []StretchRecommendation {
	matches := stretchesBlockRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		var direct []StretchRecommendation
		if json.Unmarshal([]byte(strings.TrimSpace(text)), &direct) == nil {
			return direct
		}
		return nil
	}

	var all []StretchRecommendation
	for _, match := range matches {
		jsonStr := ""
		for i := 1; i < len(match); i++ {
			if match[i] != "" {
				jsonStr = match[i]
				break
			}
		}
		if jsonStr == "" {
			continue
		}
		var items []StretchRecommendation
		if err := json.Unmarshal([]byte(jsonStr), &items); err == nil {
			all = append(all, items...)
		}
	}
	return all
}

func sanitizeStretchRecommendations(items []StretchRecommendation, current []MobilityObservation, history []db.MobilityRestriction) []StretchRecommendation {
	if len(items) == 0 {
		return nil
	}

	evidencedJoints := make(map[string]struct{})
	for _, c := range current {
		if c.Joint != "" {
			evidencedJoints[strings.ToLower(c.Joint)] = struct{}{}
		}
	}
	for _, h := range history {
		if h.Joint != "" {
			evidencedJoints[strings.ToLower(h.Joint)] = struct{}{}
		}
	}

	catalogSet := make(map[string]string)
	for _, s := range stretchCatalog {
		catalogSet[strings.ToLower(s)] = s
	}

	var sanitized []StretchRecommendation
	for _, item := range items {
		targetKey := strings.ToLower(strings.TrimSpace(item.TargetArea))
		if _, ok := evidencedJoints[targetKey]; !ok {
			continue
		}

		stretchKey := strings.ToLower(strings.TrimSpace(item.Stretch))
		canonicalStretch, ok := catalogSet[stretchKey]
		if !ok {
			continue
		}

		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			continue
		}

		sanitized = append(sanitized, StretchRecommendation{
			Stretch:      canonicalStretch,
			TargetArea:   item.TargetArea,
			Reason:       reason,
			DurationHint: strings.TrimSpace(item.DurationHint),
			Caution:      strings.TrimSpace(item.Caution),
			Provisional:  item.Provisional,
		})

		if len(sanitized) >= 3 {
			break
		}
	}
	return sanitized
}

func (w *Worker) recommendStretches(ctx context.Context, profileID uint, sessionID string, current []MobilityObservation, injuries []string) string {
	if w.DB == nil {
		return "[]"
	}

	history, err := db.BuildMobilityHistory(ctx, w.DB, profileID, sessionID)
	if err != nil {
		w.logger.Warn("Failed to build mobility history for stretch recommendations", zap.Error(err))
	}

	// Gate: if no history restrictions and no current session mobility observations, return "[]" (0 Gemini calls!)
	if len(history) == 0 && len(current) == 0 {
		return "[]"
	}

	if w.GeminiClient == nil {
		return "[]"
	}

	prompt := BuildStretchRecommendationPrompt(current, history, injuries)
	output, usage, parseErr := w.GeminiClient.ParseText(ctx, prompt)
	if parseErr != nil {
		w.logger.Error("Gemini parseText failed for stretch recommendations", zap.Error(parseErr))
		return "[]"
	}

	w.saveTokenUsage(sessionID, profileID, "session:stretch-recommendations", usage)

	parsed := parseStretchRecommendations(output)
	sanitized := sanitizeStretchRecommendations(parsed, current, history)
	if len(sanitized) == 0 {
		return "[]"
	}

	data, marshalErr := json.Marshal(sanitized)
	if marshalErr != nil {
		return "[]"
	}
	return string(data)
}
