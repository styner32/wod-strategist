package worker

import "strings"

const (
	WarmupAnalysisPrompt = `
# 웜업(Warm-up) 영상 분석 요청

## 언어 규칙
- 모든 분석 결과는 **존댓말** (~요, ~세요, ~습니다)로 작성하세요. 반말(~해, ~다, ~함)을 절대 사용하지 마세요.

## 분석 요청 사항
당신은 전문 스포츠 생체역학 전문가이자 코치입니다. 이 세션은 본 운동 전 몸을 풀고 준비하는 **웜업(Warm-up)** 세션입니다.

**주의 사항:**
- 수행 강도(Intensity)나 페이스(Pace)는 **평가 대상이 아닙니다**.
- 낮은 강도나 천천히 하는 동작에 대해 절대 감점하거나 "더 열심히/더 빠르게 수행하세요"라고 코칭하지 마세요.
- 천천히 정확하게 수행하는 가동성 동작 및 정적/동적 스트레칭 홀드는 휴식이 아니라 **세션의 핵심 운동 내용**입니다.

다음 항목을 분석해 주세요:
1. **정렬 및 가동 범위 (Alignment & Range of Motion)**:
   - 각 동작 위치에서 관절의 정렬과 가동 범위를 평가하고, 눈에 보이는 관절 가동 제한이나 타이트함을 지목해 주세요.
2. **조절력, 호흡 및 홀드 품질 (Control, Breathing & Hold Quality)**:
   - 동작 중 호흡의 안정성, 자세 조절력, 스트레칭/모빌리티 홀드의 유지 품질을 평가해 주세요.
3. **본 운동 준비도 및 좌우 비대칭 (Readiness & Asymmetry)**:
   - 본 운동 수행을 위한 몸의 준비 상태와 관찰된 좌우 비대칭(Left-Right Asymmetry)을 지목해 주세요.
4. **개선 솔루션 (Actionable Feedback)**:
   - 다음 웜업 시 적용할 수 있는 구체적인 팁 3가지를 제안해 주세요. (속도/강도를 올리라는 팁은 엄격히 금지됩니다.)`

	CooldownAnalysisPrompt = `
# 쿨다운(Cool-down) 영상 분석 요청

## 언어 규칙
- 모든 분석 결과는 **존댓말** (~요, ~세요, ~습니다)로 작성하세요. 반말(~해, ~다, ~함)을 절대 사용하지 마세요.

## 분석 요청 사항
당신은 전문 스포츠 생체역학 전문가이자 코치입니다. 이 세션은 본 운동 후 회복과 스트레칭을 위한 **쿨다운(Cool-down)** 세션입니다.

**주의 사항:**
- 수행 강도(Intensity)나 페이스(Pace)는 **평가 대상이 아닙니다**.
- 낮은 강도나 천천히 하는 동작에 대해 절대 감점하거나 "더 열심히/더 빠르게 수행하세요"라고 코칭하지 마세요.
- 천천히 정확하게 수행하는 스트레칭 홀드 및 정적/동적 회복 동작은 휴식이 아니라 **세션의 핵심 운동 내용**입니다.

다음 항목을 분석해 주세요:
1. **정렬 및 가동 범위 (Alignment & Range of Motion)**:
   - 각 스트레칭 및 회복 자세에서 관절 정렬과 가동 범위를 평가하고, 눈에 보이는 관절 가동 제한을 지목해 주세요.
2. **조절력, 호흡 및 홀드 유지 (Control, Breathing & Hold Maintenance)**:
   - 이완 중 호흡 유도, 호흡과 결합된 스트레칭 깊이, 자세 유지 안정성을 평가해 주세요.
3. **회복 적절성 및 좌우 비대칭 (Recovery Appropriateness & Asymmetry)**:
   - 세션이 회복 목적으로 적절한지 평가하고, 관찰된 좌우 비대칭이나 근육 긴장 부위를 짚어주세요.
4. **개선 솔루션 (Actionable Feedback)**:
   - 다음 쿨다운 시 적용할 수 있는 구체적인 팁 3가지를 제안해 주세요. (속도/강도를 올리라는 팁은 엄격히 금지됩니다.)`

	RecoveryScoreOutputPrompt = `

## 세션 점수 (Session Score)
분석이 끝나면 반드시 아래 형식의 **score** JSON 블록을 출력하세요.

**점수 규칙:**
- **웜업/쿨다운 평가**: 수행 강도(intensity)는 평가하지 않으며 항상 0점입니다.
- **점수 인플레이션 금지**: 기준은 절대적이며, 가동성과 자세 정확도(form), 세션 수행의 일관성(consistency)을 바탕으로 평가하세요.
- 천천히 수행하는 스트레칭 홀드와 가동성 동작은 유효한 평가 대상 운동입니다.
- 사용자 입력이 아니라 영상에서 대상 인물에게 직접 관찰되고 재검증된 동작만 movements와 점수에 포함하세요.

` + "```score\n" + `{
  "overall": 0,
  "form": 0,
  "intensity": 0,
  "consistency": 0,
  "movements": {},
  "summary": "한 문장 요약 (한국어, 오늘 웜업/쿨다운 세션의 핵심 평가)"
}` + "\n```\n" + `
- overall: form×0.7 + consistency×0.3
- form: 자세 정확도 및 가동 범위 (0=매우 나쁨, 100=완벽한 가동 범위 및 정렬)
- intensity: 0 (웜업/쿨다운에서는 평가하지 않음)
- consistency: 세션 전반에 걸친 호흡, 조절력, 자세 유지의 일관성
- movements: 관찰된 동작별 form만 기록 (예: {"Pigeon Pose": {"form": 85}, "Cossack Squat": {"form": 70}})
- summary: 이번 세션 핵심 한 줄 요약 (한국어)`

	RecoveryHighlightSelectionPrompt = `

5. **하이라이트 시각 근거 (Highlight Evidence)**:
   - 이 응답이 분석하는 구간 안에서 대상 인물에게 직접 보이는 근거만 추출하세요.
   - 웜업/쿨다운에서는 느린 스트레칭 hold나 모빌리티 동작도 유효한 하이라이트 대상입니다.
   - 오직 장비 세팅 및 화면 밖 벗어남만 제외하세요.
   - 구간당 최대 3개만 출력하고, 근거가 없으면 빈 배열을 출력하세요.
   - type: positive_form(좋은 가동범위/정렬/홀드), form_issue(가동범위 제한 또는 보상작용), technique_event(구체적인 스트레칭/가동성 전환 장면). fatigue_onset은 웜업/쿨다운에서 사용하지 마세요.
   - confidence는 해당 시각 근거가 영상에서 직접 확인된 확신도이며 0.0~1.0 숫자로 출력하세요.
   - 중요한 장면이면 tags에 key_moment를 추가하세요.
   - movement 필드에는 실제로 관찰된 스트레칭 또는 모빌리티 동작명을 기입하세요.
   - 반드시 아래 형식의 **highlights** JSON 코드 블록으로 출력하세요 (json이 아닌 highlights 태그 사용):
` + "```highlights\n" + `[{"start":"0:15","end":"0:25","type":"positive_form","movement":"Pigeon Pose","reason":"골반 수평 유지 및 충분한 고관절 굴곡 가동범위","confidence":0.92,"tags":["key_moment"]},{"start":"0:30","end":"0:40","type":"form_issue","movement":"Ankle Dorsiflexion Rock","reason":"발뒤꿈치 들림으로 인한 족배굴곡 제한","confidence":0.88}]` + "\n```"

	RecoveryChunkPolicyPrompt = `

## 웜업/쿨다운 코칭 정책 (Warmup & Cooldown Coaching Policy)
- 페이스나 수행 강도(Intensity)를 높이라는 조언은 엄격히 금지됩니다.
- 정적 스트레칭 홀드 및 요가 자세는 유효한 active exercise 세그먼트입니다. ([EXERCISE: Pigeon Pose] 등으로 라벨링)
- 코칭 큐는 오직 신체 정렬, 호흡, 가동 범위(ROM), 홀드 유지 품질에 한정하세요.
- 통증 신호나 심한 보상 작용이 관찰되면 가동 범위를 줄이거나 동작을 완화하도록 조언하세요.`
)

func analysisPromptFor(wt string) string {
	switch strings.ToLower(strings.TrimSpace(wt)) {
	case WorkoutTypeWarmup:
		return WarmupAnalysisPrompt
	case WorkoutTypeCooldown:
		return CooldownAnalysisPrompt
	default:
		return AnalysisPrompt
	}
}

func scoreOutputPromptFor(wt string) string {
	if IsRecoveryWorkoutType(wt) {
		return RecoveryScoreOutputPrompt
	}
	return ScoreOutputPrompt
}

func highlightSelectionPromptFor(wt string) string {
	if IsRecoveryWorkoutType(wt) {
		return RecoveryHighlightSelectionPrompt
	}
	return HighlightSelectionPrompt
}
