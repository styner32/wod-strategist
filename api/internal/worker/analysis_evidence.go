package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
)

const MovementEvidenceRulesPrompt = `

## 종목 판정 근거 규칙
- 사용자 입력 WOD와 운동 후보는 참고용 힌트이며, 영상에 실제로 등장했다는 증거가 아닙니다.
- 대상 인물이 실제로 수행하는 동작만 판정하세요. 배경 인물의 동작은 대상 인물의 종목으로 사용하지 마세요.
- 기구 기반 종목은 대상 인물의 기구 접촉, 몸의 위치, 연속된 동작 패턴을 함께 확인하세요.
- 대상 인물 근처에 로프, 바벨, 박스 또는 다른 기구가 있다는 사실만으로 종목을 판정하지 마세요.
- 힌트와 영상이 다르면 영상에서 대상 인물에게 직접 보이는 근거를 우선하세요.
- 힌트에 없는 종목이라도 시각적 근거가 충분하면 실제 관찰한 영어 종목명을 그대로 사용하세요.
- 대상 인물이 운동 중인 것은 분명하지만 종목 근거가 부족하면 종목을 Unknown으로 처리하세요. 추측해서 힌트에 맞추지 마세요.
- 걷기, 휴식, 회복, 준비 또는 장비 세팅은 운동 종목이 아닙니다.`

const FatigueEvidenceRulesPrompt = `

## 피로 판정 근거 규칙
- 피로는 운동을 수행하는 대상 인물에게 반복 속도 저하, 케이던스 손실, 가동범위 감소 또는 자세 붕괴가 영상에서 지속적으로 보일 때만 판정하세요.
- 단일 느린 반복, 의도적으로 조절한 템포, 단순한 멈춤 또는 힘들어 보이는 표정만으로 피로를 단정하지 마세요.
- 심박수는 영상에서 피로가 먼저 확립된 경우에만 보조 근거입니다. 심박수만으로 피로를 만들거나 피로 시작 시점을 정하지 마세요.
- 걷기, 휴식, 회복, 준비 또는 장비 세팅은 심박수와 관계없이 피로 동작이나 fatigue_point가 아닙니다.
- 이전 청크의 검증된 근거가 제공되지 않았다면 피로가 "증가했다"거나 "누적됐다"는 추세를 추측하지 마세요.`

var allowedVisualFatigueEvidenceTypes = map[string]struct{}{
	"rep_slowdown":         {},
	"cadence_loss":         {},
	"range_of_motion_loss": {},
	"form_breakdown":       {},
}

func baseChunkAnalysisPrompt() string {
	return ChunkAnalysisPrompt + MovementEvidenceRulesPrompt + FatigueEvidenceRulesPrompt
}

func buildMovementHintsContext(movements []string) string {
	if len(movements) == 0 {
		return ""
	}

	return fmt.Sprintf(`

## 운동 후보 힌트 (사용자 입력, 비배타적)
아래 목록은 사용자가 입력한 후보이며 영상에 등장한다는 보장이 없습니다.
목록과 다른 종목도 시각적 근거가 있으면 그대로 식별하고, 목록에 있어도 보이지 않으면 결과·점수·하이라이트에서 제외하세요.
%s`, strings.Join(movements, ", "))
}

func movementHintsFromDocument(document db.JSONDocument) []string {
	if len(document) == 0 {
		return nil
	}

	var hints []string
	if err := json.Unmarshal(document, &hints); err != nil {
		return nil
	}

	result := make([]string, 0, len(hints))
	seen := make(map[string]struct{})
	for _, hint := range hints {
		hint = strings.TrimSpace(hint)
		key := strings.ToLower(hint)
		if hint == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, hint)
	}
	return result
}

func (w *Worker) buildPersonalMovementHintsContext(profileID uint, excludeSessionID string) string {
	if w.DB == nil || profileID == 0 {
		return ""
	}
	hints, err := db.BuildPersonalMovementHints(context.Background(), w.DB, profileID, excludeSessionID)
	if err != nil {
		if w.logger != nil {
			w.logger.Warn("Could not load personal movement hints")
		}
		return ""
	}
	return formatPersonalMovementHintsContext(hints)
}

func formatPersonalMovementHintsContext(hints []db.PersonalMovementHint) string {
	if len(hints) == 0 {
		return ""
	}

	var result strings.Builder
	result.WriteString(`

## 개인 종목 혼동 참고 (약한 힌트, 규칙 아님)
아래 항목은 이전의 반복 교정에서 나온 혼동 주의 목록입니다. 현재 세션의 정답이나 확정 종목이 아닙니다.
현재 영상에서 대상 인물의 기구 접촉, 몸 위치, 연속 동작을 독립적으로 확인하고, 시각 근거가 모순되면 이 힌트를 무시하세요.
`)
	for _, hint := range hints {
		result.WriteString(fmt.Sprintf("- 과거 '%s' 예측이 '%s'로 교정됨 (%d개 서로 다른 세션)\n",
			hint.PredictedMovement, hint.CorrectedMovement, hint.SessionCount))
	}
	return result.String()
}

func buildHeartRateContext(heartRateBPM int) string {
	if heartRateBPM <= 0 {
		return ""
	}

	return fmt.Sprintf(`

## 청크 심박수 참고값
이 청크와 함께 수신된 심박수는 %d bpm입니다. 정확한 측정 시점은 알 수 없습니다.
영상에서 반복 속도, 케이던스, 가동범위 또는 자세의 지속적인 저하가 먼저 확인된 경우에만 보조 근거로 사용하세요.
심박수만으로 피로를 판정하거나 피로 구간을 선택하지 말고, 심박수 수치를 코칭 문장에 포함하지 마세요.`, heartRateBPM)
}

type legacyAppearanceDoc struct {
	Appearance string            `json:"appearance,omitempty"`
	Persistent map[string]string `json:"persistent,omitempty"`
	Session    map[string]string `json:"session,omitempty"`
	Removable  []string          `json:"removable,omitempty"`
}

func extractAppearanceText(raw []byte) string {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return ""
	}

	var doc legacyAppearanceDoc
	if err := json.Unmarshal(raw, &doc); err == nil {
		if strings.TrimSpace(doc.Appearance) != "" {
			return strings.TrimSpace(doc.Appearance)
		}

		var parts []string
		for _, key := range []string{"top", "bottom"} {
			if v, ok := doc.Session[key]; ok && strings.TrimSpace(v) != "" {
				parts = append(parts, strings.TrimSpace(v))
			}
		}
		for _, key := range []string{"shoes", "hair", "build"} {
			if v, ok := doc.Persistent[key]; ok && strings.TrimSpace(v) != "" {
				parts = append(parts, strings.TrimSpace(v))
			}
		}
		for _, item := range doc.Removable {
			if strings.TrimSpace(item) != "" {
				parts = append(parts, strings.TrimSpace(item))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ", ")
		}
	}

	var strVal string
	if err := json.Unmarshal(raw, &strVal); err == nil && strings.TrimSpace(strVal) != "" {
		return strings.TrimSpace(strVal)
	}

	s := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") && !strings.HasPrefix(s, "\"") && s != "" {
		return s
	}

	return ""
}

func (w *Worker) buildTargetPersonContext(profileID uint, sessionID string) string {
	if w == nil || w.DB == nil {
		return ""
	}

	if profileID == 0 && sessionID != "" {
		var sess db.Session
		if err := w.DB.Where("session_id = ?", sessionID).First(&sess).Error; err == nil {
			profileID = sess.ProfileID
		}
	}

	var parts []string

	if profileID != 0 {
		var profile db.Profile
		if err := w.DB.First(&profile, profileID).Error; err == nil {
			if len(profile.Appearance) > 0 {
				if text := extractAppearanceText(profile.Appearance); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}

	if sessionID != "" {
		var hint db.SessionAppearanceHint
		if err := w.DB.Where("session_id = ?", sessionID).First(&hint).Error; err == nil {
			if len(hint.Hints) > 0 {
				if text := extractAppearanceText(hint.Hints); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}

	merged := strings.Join(parts, ", ")
	return formatTargetPersonContext(merged)
}

func formatTargetPersonContext(merged string) string {
	merged = strings.TrimSpace(merged)

	var sb strings.Builder
	sb.WriteString("\n\n## 대상 인물 확정\n")
	if merged != "" {
		sb.WriteString("[시각 특징] — 대상 확정의 근거로 사용하세요.\n")
		sb.WriteString(fmt.Sprintf("- %s\n\n", merged))

		sb.WriteString(`식별 규칙:
- 특징이 일치하는 인물이 정확히 1명이면 그 사람만 분석하세요.
- 여러 명이거나 불명확하면 화면 중앙에 가장 크게, 지속적으로 잡히는 인물로 판단하되 그 사실을 밝히세요.
- 가방, 수건, 겉옷 등 탈착 가능 장비는 벗거나 위치가 바뀌었을 가능성을 염두에 두세요.`)
	} else {
		sb.WriteString(`[시각 특징] — 별도로 지정된 외형 특징이 없습니다.
- 카메라 전면 또는 화면 중앙에서 주요 운동을 수행하는 인물을 대상 인물로 식별하세요.
- 주변 보조자나 배경에서 지나가는 인물의 동작은 분석 대상에 포함하지 마세요.`)
	}

	return sb.String()
}

func resolveReanalysisModel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "flash", "flash36", "flash35", "gemini-3.6-flash", "gemini-3.5-flash":
		return gemini.ModelFlash36
	default:
		return gemini.ModelPro31Preview
	}
}

func isNonExerciseMovement(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer("_", " ", "-", " ", "/", " ").Replace(normalized)
	normalized = strings.Join(strings.Fields(normalized), " ")

	switch normalized {
	case "", "walk", "walking", "rest", "setup", "rest setup", "recovery", "not exercise", "no exercise", "noexercise", "unknown":
		return true
	}

	return strings.HasPrefix(normalized, "rest ") ||
		strings.HasPrefix(normalized, "setup ") ||
		strings.HasPrefix(normalized, "recovery ")
}

func isExplicitNonExerciseActivity(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer("_", " ", "-", " ", "/", " ").Replace(normalized)
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "walk", "walking", "rest", "setup", "rest setup", "recovery", "not exercise", "no exercise", "noexercise":
		return true
	default:
		return false
	}
}

func sanitizeObservedSignals(raw string) string {
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return "{}"
	}

	movement, _ := values["movement"].(string)
	activityState, _ := values["activity_state"].(string)
	fatigueEstablished, _ := values["fatigue_visually_established"].(bool)

	if isNonExerciseMovement(movement) || isExplicitNonExerciseActivity(activityState) || strings.EqualFold(strings.TrimSpace(activityState), "unknown") {
		clearFatigueSignals(values)
	} else if fatigueEstablished {
		evidenceTypes := allowedFatigueEvidenceTypes(values["fatigue_evidence_types"])
		evidence := visualFatigueEvidence(values["fatigue_evidence"])
		if len(evidenceTypes) == 0 || len(evidence) == 0 {
			clearFatigueSignals(values)
		} else {
			values["fatigue_evidence_types"] = evidenceTypes
			values["fatigue_evidence"] = evidence
		}
	} else if _, present := values["fatigue_visually_established"]; present {
		clearFatigueSignals(values)
	}

	clean, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(clean)
}

func visualFatigueEvidence(value any) []string {
	result := make([]string, 0)
	for _, evidence := range stringSlice(value) {
		normalized := strings.ToLower(evidence)
		if strings.Contains(normalized, "bpm") ||
			strings.Contains(normalized, "heart rate") ||
			strings.Contains(normalized, "heartrate") ||
			strings.Contains(normalized, "심박") {
			continue
		}
		result = append(result, evidence)
	}
	return result
}

func clearFatigueSignals(values map[string]any) {
	values["fatigue_visually_established"] = false
	values["fatigue_evidence_types"] = []string{}
	values["fatigue_evidence"] = []string{}
}

func allowedFatigueEvidenceTypes(value any) []string {
	var allowed []string
	seen := make(map[string]struct{})
	for _, candidate := range stringSlice(value) {
		normalized := strings.ToLower(strings.TrimSpace(candidate))
		if _, ok := allowedVisualFatigueEvidenceTypes[normalized]; !ok {
			continue
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		allowed = append(allowed, normalized)
	}
	return allowed
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}

	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text != "" {
			result = append(result, text)
		}
	}
	return result
}

type movementFatigueAggregate struct {
	Movement       string
	PositiveChunks int
	FirstStart     *float64
	FirstEnd       *float64
	EvidenceTypes  map[string]struct{}
	Observations   []string
}

func (w *Worker) buildSessionFatigueEvidenceContext(sessionID string) string {
	if w.DB == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}

	var chunks []db.ChunkAnalysisResult
	err := w.DB.
		Where("session_id = ? AND status = ?", sessionID, "COMPLETED").
		Order("media_start_secs ASC NULLS LAST, created_at ASC, id ASC").
		Find(&chunks).Error
	if err != nil {
		return ""
	}
	return buildFatigueEvidenceContext(chunks)
}

func buildFatigueEvidenceContext(chunks []db.ChunkAnalysisResult) string {
	structuredCount := 0
	exerciseCount := 0
	nonExerciseCount := 0
	unknownCount := 0
	positiveCount := 0
	aggregates := make(map[string]*movementFatigueAggregate)

	for _, chunk := range chunks {
		var values map[string]any
		if err := json.Unmarshal([]byte(sanitizeObservedSignals(chunk.ObservedSignals)), &values); err != nil || len(values) == 0 {
			continue
		}
		structuredCount++

		movement, _ := values["movement"].(string)
		activityState, _ := values["activity_state"].(string)
		switch {
		case isExplicitNonExerciseActivity(activityState) ||
			(strings.TrimSpace(movement) != "" && !strings.EqualFold(strings.TrimSpace(movement), "unknown") && isNonExerciseMovement(movement)):
			nonExerciseCount++
		case strings.EqualFold(strings.TrimSpace(activityState), "unknown") || strings.EqualFold(strings.TrimSpace(movement), "unknown"):
			unknownCount++
		default:
			exerciseCount++
		}

		fatigueEstablished, _ := values["fatigue_visually_established"].(bool)
		if !fatigueEstablished {
			continue
		}

		positiveCount++
		movement = strings.TrimSpace(movement)
		if movement == "" {
			movement = strings.TrimSpace(chunk.ExerciseType)
		}
		if movement == "" || isNonExerciseMovement(movement) {
			continue
		}

		aggregate := aggregates[strings.ToLower(movement)]
		if aggregate == nil {
			aggregate = &movementFatigueAggregate{
				Movement:      movement,
				FirstStart:    chunk.MediaStartSecs,
				FirstEnd:      chunk.MediaEndSecs,
				EvidenceTypes: make(map[string]struct{}),
			}
			aggregates[strings.ToLower(movement)] = aggregate
		}
		aggregate.PositiveChunks++
		for _, evidenceType := range allowedFatigueEvidenceTypes(values["fatigue_evidence_types"]) {
			aggregate.EvidenceTypes[evidenceType] = struct{}{}
		}
		for _, observation := range stringSlice(values["fatigue_evidence"]) {
			if len(aggregate.Observations) >= 3 || containsString(aggregate.Observations, observation) {
				continue
			}
			aggregate.Observations = append(aggregate.Observations, observation)
		}
	}

	if structuredCount == 0 {
		return ""
	}

	var result strings.Builder
	result.WriteString("\n\n## 세션 전체 구조화 피로 근거 (모든 수신 청크 집계)\n")
	result.WriteString(fmt.Sprintf("- 구조화 청크: %d, 운동: %d, 걷기/휴식/준비/회복: %d, 불명확: %d\n", structuredCount, exerciseCount, nonExerciseCount, unknownCount))
	result.WriteString(fmt.Sprintf("- 시각적으로 피로가 확립된 운동 청크: %d\n", positiveCount))

	ordered := make([]*movementFatigueAggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		ordered = append(ordered, aggregate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].FirstStart != nil && ordered[j].FirstStart != nil && *ordered[i].FirstStart != *ordered[j].FirstStart {
			return *ordered[i].FirstStart < *ordered[j].FirstStart
		}
		return ordered[i].Movement < ordered[j].Movement
	})

	if len(ordered) == 0 {
		result.WriteString("- 허용된 시각적 피로 근거가 없습니다. 피로 이벤트나 피로 시작 시점을 생성하지 마세요.\n")
	} else {
		for _, aggregate := range ordered {
			types := make([]string, 0, len(aggregate.EvidenceTypes))
			for evidenceType := range aggregate.EvidenceTypes {
				types = append(types, evidenceType)
			}
			sort.Strings(types)
			result.WriteString(fmt.Sprintf("- %s: %d개 청크, 최초 근거 %s, 유형 %s", aggregate.Movement, aggregate.PositiveChunks, formatEvidenceInterval(aggregate.FirstStart, aggregate.FirstEnd), strings.Join(types, ", ")))
			if len(aggregate.Observations) > 0 {
				result.WriteString(", 관찰 " + strings.Join(aggregate.Observations, " / "))
			}
			result.WriteString("\n")
		}
	}

	result.WriteString("- 위 집계는 모든 수신 구조화 청크를 반영합니다. 최종 피로 결론은 이 집계와 현재 영상의 시각 근거만 사용하세요.\n")
	result.WriteString("- 심박수만으로 피로를 추가하거나 시간을 정하지 말고, 걷기/휴식/준비/회복을 피로 구간으로 만들지 마세요.\n")
	return result.String()
}

func formatEvidenceInterval(start, end *float64) string {
	if start == nil || end == nil {
		return "시간 불명"
	}
	return fmt.Sprintf("%.1f~%.1f초", *start, *end)
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
