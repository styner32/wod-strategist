package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

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

func (w *Worker) loadStretchCatalog(ctx context.Context) (names []string, resolver map[string]string) {
	resolver = make(map[string]string)
	nameSet := make(map[string]struct{})

	for _, s := range stretchCatalog {
		key := db.NormalizeStretchKey(s)
		if key != "" {
			resolver[key] = s
		}
		if _, exists := nameSet[s]; !exists {
			names = append(names, s)
			nameSet[s] = struct{}{}
		}
	}

	if w.DB == nil {
		return names, resolver
	}

	stretches, err := db.ListStretches(ctx, w.DB)
	if err != nil {
		if w.logger != nil {
			w.logger.Warn("Failed to load stretch catalog from DB, using fallback catalog", zap.Error(err))
		}
		return names, resolver
	}

	stretchNameByID := make(map[uint64]string)
	for _, s := range stretches {
		if _, exists := nameSet[s.Name]; !exists {
			names = append(names, s.Name)
			nameSet[s.Name] = struct{}{}
		}
		stretchNameByID[s.ID] = s.Name
		key := db.NormalizeStretchKey(s.Name)
		if key != "" {
			resolver[key] = s.Name
		}
	}

	aliases, err := db.ListStretchAliases(ctx, w.DB)
	if err != nil && w.logger != nil {
		w.logger.Warn("Failed to load stretch aliases from DB, continuing with stretches only", zap.Error(err))
	}

	for _, a := range aliases {
		key := db.NormalizeStretchKey(a.Alias)
		if key != "" {
			if canonicalName, found := stretchNameByID[a.StretchID]; found {
				resolver[key] = canonicalName
			}
		}
	}

	return names, resolver
}

func BuildStretchRecommendationPrompt(current []MobilityObservation, history []db.MobilityRestriction, injuries []string, catalogNames []string) string {
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

	var catalogSb strings.Builder
	for _, name := range catalogNames {
		catalogSb.WriteString(fmt.Sprintf("   - %s\n", name))
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
2. **스트레칭 카탈로그 우선 및 명명 규칙**:
   - 가급적 아래 카탈로그 목록에 있는 기존 스트레칭 명칭을 우선하여 똑같이 사용하세요:
%s   - 기존 카탈로그에 해당 관절/제한에 적합한 스트레칭이 없는 경우에만 **새로운 스트레칭**을 제안할 수 있습니다.
   - 새로운 스트레칭 명칭은 반드시 **영문 Title Case** 및 **간결한 표준 Naming Convention**을 따르세요:
     (예: [Target/Joint] [Type] Stretch 또는 [Movement] Hold/Rock/Pose — `+"`Hip 90/90 Stretch`"+`, `+"`Adductor Groin Stretch`"+`, `+"`Lat Doorframe Stretch`"+`). 길거나 서술적인 명칭, 한국어 포함 명칭은 금지됩니다.
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
]`+"\n```\n", evidenceSb.String(), injuryText, catalogSb.String())
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

// significantTokens splits normalized key into significant matching tokens
func significantTokens(key string) []string {
	words := strings.Fields(key)
	var tokens []string
	for _, w := range words {
		w = strings.Trim(w, "(),/-")
		if w == "" || w == "stretch" || w == "pose" || w == "hold" || w == "exercise" || w == "over" {
			continue
		}
		if len(w) > 4 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		tokens = append(tokens, w)
	}
	return tokens
}

// resolveStretchName matches rawStretch against resolver using exact match, alias match, or fuzzy token similarity.
func resolveStretchName(rawStretch string, resolver map[string]string) (string, bool) {
	key := db.NormalizeStretchKey(rawStretch)
	if key == "" {
		return "", false
	}

	// 1. Exact normalized match
	if canonical, ok := resolver[key]; ok {
		return canonical, true
	}

	// 2. Fuzzy / similarity token overlap match
	rawTokens := significantTokens(key)
	if len(rawTokens) == 0 {
		return "", false
	}

	var bestCanonical string
	var bestScore float64

	for catKey, canonical := range resolver {
		catTokens := significantTokens(catKey)
		if len(catTokens) == 0 {
			continue
		}

		matchCount := 0
		for _, rt := range rawTokens {
			for _, ct := range catTokens {
				if rt == ct || (len(rt) >= 4 && len(ct) >= 4 && (strings.HasPrefix(rt, ct) || strings.HasPrefix(ct, rt))) {
					matchCount++
					break
				}
			}
		}

		if matchCount == 0 {
			continue
		}

		minLen := len(rawTokens)
		if len(catTokens) < minLen {
			minLen = len(catTokens)
		}
		score := float64(matchCount) / float64(minLen)

		if score > bestScore && (matchCount == minLen || score >= 0.75) {
			bestScore = score
			bestCanonical = canonical
		}
	}

	if bestCanonical != "" && bestScore >= 0.75 {
		return bestCanonical, true
	}

	return "", false
}

// formatStretchName formats a raw stretch name to Title Case following standard naming conventions.
func formatStretchName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	words := strings.Fields(trimmed)
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		lower := strings.ToLower(w)
		if lower == "of" || lower == "in" || lower == "on" || lower == "at" || lower == "by" || lower == "for" || lower == "to" || lower == "over" || lower == "with" {
			if i > 0 {
				words[i] = lower
				continue
			}
		}
		words[i] = capitalizeWord(w)
	}

	return strings.Join(words, " ")
}

func capitalizeWord(w string) string {
	if len(w) == 0 {
		return ""
	}
	if strings.Contains(w, "-") {
		parts := strings.Split(w, "-")
		for i, p := range parts {
			parts[i] = capitalizeWord(p)
		}
		return strings.Join(parts, "-")
	}
	if strings.Contains(w, "/") {
		parts := strings.Split(w, "/")
		for i, p := range parts {
			parts[i] = capitalizeWord(p)
		}
		return strings.Join(parts, "/")
	}

	prefix := ""
	suffix := ""
	if strings.HasPrefix(w, "(") {
		prefix = "("
		w = w[1:]
	}
	if strings.HasSuffix(w, ")") {
		suffix = ")"
		w = w[:len(w)-1]
	}

	if len(w) == 0 {
		return prefix + suffix
	}

	runes := []rune(strings.ToLower(w))
	runes[0] = unicode.ToUpper(runes[0])
	return prefix + string(runes) + suffix
}

func isValidNewStretchName(name string) bool {
	runeCount := utf8.RuneCountInString(name)
	if runeCount < 3 || runeCount > 120 {
		return false
	}

	// Reject non-Latin characters (e.g. Korean Hangul)
	for _, r := range name {
		if r > 0x024F && !(r >= 0x2000 && r <= 0x206F) {
			return false
		}
	}

	words := strings.Fields(name)
	if len(words) > 8 {
		return false
	}

	return true
}

func (w *Worker) sanitizeAndPersistStretchRecommendations(ctx context.Context, items []StretchRecommendation, current []MobilityObservation, history []db.MobilityRestriction, resolver map[string]string) []StretchRecommendation {
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

	var sanitized []StretchRecommendation
	for _, item := range items {
		targetKey := strings.ToLower(strings.TrimSpace(item.TargetArea))
		if _, ok := evidencedJoints[targetKey]; !ok {
			continue
		}

		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			continue
		}

		canonicalStretch, matched := resolveStretchName(item.Stretch, resolver)
		if !matched {
			formattedNewName := formatStretchName(item.Stretch)
			if !isValidNewStretchName(formattedNewName) {
				continue
			}
			canonicalStretch = formattedNewName

			// Auto-persist valid new stretch into DB catalog
			if w != nil && w.DB != nil {
				newKey := db.NormalizeStretchKey(canonicalStretch)
				if newKey != "" {
					_ = w.DB.WithContext(ctx).Exec(`
						INSERT INTO stretches (name, normalized_key, target_area, description, duration_hint, caution, image_object, video_object)
						VALUES (?, ?, ?, ?, ?, ?, '', '')
						ON CONFLICT (normalized_key) DO NOTHING
					`, canonicalStretch, newKey, item.TargetArea, item.Reason, item.DurationHint, item.Caution)
					resolver[newKey] = canonicalStretch
				}
			}
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

func sanitizeStretchRecommendations(items []StretchRecommendation, current []MobilityObservation, history []db.MobilityRestriction, resolver map[string]string) []StretchRecommendation {
	w := &Worker{}
	return w.sanitizeAndPersistStretchRecommendations(context.Background(), items, current, history, resolver)
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

	catalogNames, resolver := w.loadStretchCatalog(ctx)

	prompt := BuildStretchRecommendationPrompt(current, history, injuries, catalogNames)
	output, usage, parseErr := w.GeminiClient.ParseText(ctx, prompt)
	if parseErr != nil {
		w.logger.Error("Gemini parseText failed for stretch recommendations", zap.Error(parseErr))
		return "[]"
	}

	w.saveTokenUsage(sessionID, profileID, "session:stretch-recommendations", usage)

	parsed := parseStretchRecommendations(output)
	sanitized := w.sanitizeAndPersistStretchRecommendations(ctx, parsed, current, history, resolver)
	if len(sanitized) == 0 {
		return "[]"
	}

	data, marshalErr := json.Marshal(sanitized)
	if marshalErr != nil {
		return "[]"
	}
	return string(data)
}
