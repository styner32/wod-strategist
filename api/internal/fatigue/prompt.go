package fatigue

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/db"
)

type TargetRPEInfo struct {
	Score          int    `json:"score"`
	Label          string `json:"label"`
	PacingStrategy string `json:"pacing_strategy"`
}

type ScalingAdviceItem struct {
	Movement       string `json:"movement"`
	Recommendation string `json:"recommendation"`
	Detail         string `json:"detail"`
}

type MobilityWarmupItem struct {
	Title      string `json:"title"`
	TargetArea string `json:"target_area"`
	Duration   string `json:"duration"`
	Reason     string `json:"reason"`
}

type MuscleReadinessItem struct {
	Group        string `json:"group"`
	NameKO       string `json:"name_ko"`
	FatigueScore int    `json:"fatigue_score"`
	State        string `json:"state"`
	StateKO      string `json:"state_ko"`
	Note         string `json:"note"`
}

type HybridSensorBrief struct {
	HeartIntensity     *float64 `json:"heart_intensity,omitempty"`
	MotionIntensity    *float64 `json:"motion_intensity,omitempty"`
	HybridIntensity    float64  `json:"hybrid_intensity"`
	HybridLoadAU       *float64 `json:"hybrid_load_au,omitempty"`
	DataCoverage       float64  `json:"data_coverage"`
	CalibrationVersion string   `json:"calibration_version,omitempty"`
}

type RecentWorkoutSummary struct {
	SessionID       string             `json:"session_id"`
	WorkoutAt       time.Time          `json:"workout_at"`
	Movements       []string           `json:"movements"`
	DurationSeconds *int               `json:"duration_seconds,omitempty"`
	Effort          *int               `json:"effort,omitempty"`
	HybridSensor    *HybridSensorBrief `json:"hybrid_sensor,omitempty"`
}

type EffortReflectionInfo struct {
	Applied        bool                   `json:"applied"`
	RatedCount     int                    `json:"rated_count"`
	SensorCount    int                    `json:"sensor_count"`
	TotalRecent    int                    `json:"total_recent"`
	Reason         string                 `json:"reason,omitempty"`
	RecentWorkouts []RecentWorkoutSummary `json:"recent_workouts,omitempty"`
}

type PreWODAdviceResponse struct {
	ProfileID              uint                  `json:"profile_id"`
	OverallFatigueScore    *int                  `json:"overall_fatigue_score,omitempty"`
	OverallState           string                `json:"overall_state"`
	OverallStateKO         string                `json:"overall_state_ko"`
	MuscleReadiness        []MuscleReadinessItem `json:"muscle_readiness,omitempty"`
	TargetRPE              *TargetRPEInfo        `json:"target_rpe"`
	EffortReflection       *EffortReflectionInfo `json:"effort_reflection,omitempty"`
	EffortReflectionReason string                `json:"effort_reflection_reason,omitempty"`
	ScalingAdvice          []ScalingAdviceItem   `json:"scaling_advice"`
	MobilityWarmup         []MobilityWarmupItem  `json:"mobility_warmup"`
	OverallSummary         string                `json:"overall_summary"`
	AdviceCode             string                `json:"advice_code,omitempty"`
	EvidenceStatus         string                `json:"evidence_status"`
	Evidence               EvidenceCounts        `json:"evidence"`
	AsOf                   time.Time             `json:"as_of"`
	LastWorkoutAt          *time.Time            `json:"last_workout_at,omitempty"`
}

const maxRecentWorkoutsInPrompt = 3

// BuildPreWODAdvicePrompt constructs a Gemini prompt to generate structured pre-WOD strategy advice.
func BuildPreWODAdvicePrompt(
	readiness ProfileReadinessState,
	profile db.Profile,
	wodDescription string,
	plannedMovements []string,
	injuries []string,
	recentWorkouts ...RecentWorkoutSummary,
) string {
	var sb strings.Builder

	sb.WriteString(`당신은 세계 정상급 크로스핏 헤드 코치이자 스포츠 과학 전문가입니다.
사용자의 최근 운동 이력 기반 6대 근육군 피로도/신선도 분석 결과와 오늘 예정된 WOD(운동)를 대조하여,
오늘 운동에서 부상을 예방하고 최대의 트레이닝 효과를 낼 수 있는 맞춤형 전략 및 스케일링 조언을 생성하세요.

## 사용자 프로필
`)
	fitnessLevel := profile.FitnessLevel
	if fitnessLevel == "" {
		fitnessLevel = "intermediate"
	}
	sb.WriteString(fmt.Sprintf("- 피트니스 레벨: %s\n", fitnessLevel))

	if len(injuries) > 0 {
		sb.WriteString(fmt.Sprintf("- 주의 부상 부위: %s\n", strings.Join(injuries, ", ")))
	} else {
		sb.WriteString("- 주의 부상 부위: 없음\n")
	}

	sb.WriteString("\n## 현재 신체 부위별 잔여 피로도 및 신선도 (알고리즘 산출 결과)\n")
	scoreStr := "근거 부족"
	if readiness.OverallFatigueScore != nil {
		scoreStr = fmt.Sprintf("%d/100", *readiness.OverallFatigueScore)
	}
	sb.WriteString(fmt.Sprintf("- 전신 종합 피로도: %s (%s)\n", scoreStr, readiness.OverallStateKO))
	for _, g := range AllMuscleGroups {
		m, ok := readiness.Muscles[g]
		if ok {
			sb.WriteString(fmt.Sprintf("- %s (%s): 피로도 %d/100 [%s]\n", m.NameKO, g, m.FatigueScore, m.StateKO))
		}
	}

	if len(recentWorkouts) > 0 {
		limit := len(recentWorkouts)
		if limit > maxRecentWorkoutsInPrompt {
			limit = maxRecentWorkoutsInPrompt
		}
		sb.WriteString("\n## 최근 본 운동(WOD) 수행 및 강도 피드백 (최대 최근 3회)\n")
		for i := 0; i < limit; i++ {
			w := recentWorkouts[i]
			movStr := "종목 미확인"
			if len(w.Movements) > 0 {
				movStr = strings.Join(w.Movements, ", ")
			}
			durStr := "시간 미확정"
			if w.DurationSeconds != nil && *w.DurationSeconds > 0 {
				durStr = formatDurationKO(*w.DurationSeconds)
			}
			effortStr := "자기보고 Effort 미입력 (추측 금지)"
			if w.Effort != nil && *w.Effort >= 1 && *w.Effort <= 10 {
				effortStr = fmt.Sprintf("자기보고 Effort %d/10", *w.Effort)
			}
			line := fmt.Sprintf("- 최근 #%d (%s): 종목 [%s] | 수행시간 %s | %s",
				i+1,
				w.WorkoutAt.UTC().Format("2006-01-02 15:04 UTC"),
				movStr,
				durStr,
				effortStr,
			)
			if w.HybridSensor != nil {
				line += " | " + formatHybridSensorBriefKO(*w.HybridSensor)
			}
			sb.WriteString(line + "\n")
		}
	}

	sb.WriteString("\n## 오늘 계획된 WOD / 운동\n")
	if strings.TrimSpace(wodDescription) != "" {
		sb.WriteString(fmt.Sprintf("- WOD 설명: %s\n", wodDescription))
	}
	if len(plannedMovements) > 0 {
		sb.WriteString(fmt.Sprintf("- 포함 운동 종목: %s\n", strings.Join(plannedMovements, ", ")))
	}
	if strings.TrimSpace(wodDescription) == "" && len(plannedMovements) == 0 {
		sb.WriteString("- 특정 WOD 미정 (오늘 컨디션에 맞춘 추천 가이드 요청)\n")
	}

	sb.WriteString(`
## 코칭 지침 및 원칙
1. **피로 부위와 WOD 종목 충돌 분석**:
   - 피로도 50% 이상인 부위가 오늘 WOD의 주요 사용 근육과 겹칠 경우 적극적인 감량, 템포 조절, 또는 대체 동작(스케일링)을 제안하세요.
   - 신선(Fresh)한 부위를 활용할 수 있는 전략(예: 하체가 신선하면 상체 피로 시 하체 드라이브 활용)을 안내하세요.
2. **타겟 RPE 및 페이스 전략**:
   - 1~10 척도의 목표 RPE를 설정하고, 라운드별/세트별 구체적인 페이스 운용 팁을 제공하세요.
3. **최근 3회 운동 강도 피드백 반영 및 척도 분리 주의사항**:
   - 자기보고 Effort(1~10)가 있는 경우, 비슷한 수행시간이라도 여유 있게 마쳤는지(Effort <= 5) 매우 힘들게 마쳤는지(Effort >= 8)를 오늘 초반 페이스와 세트 분할 조언에 반영하세요.
   - RIR(남은 반복수) 척도와 앱의 Effort(1~10)는 다른 척도이므로 값 자체를 1:1로 대응시키거나 변환해 언급하지 마세요.
   - 센서 '하이브리드 활동강도(0~100)'는 심박 예비율(HRR)과 가속도 활동량(ENMO)을 결합한 별도 객관 활동 지표이며 Effort(1~10)나 Apple Effort를 대체하지 않습니다. 외부 중량 정보가 없으므로 느리고 무거운 근력 동작에서 점수가 낮게 나와도 이를 "쉬운 근력 운동"으로 해석하지 마세요.
   - **회복 단정 및 누락값 추측 금지**: 낮은 자기보고 Effort나 낮은 센서 점수를 실제 회복 완료 상태로 단정하지 마세요(6대 근육 피로도가 60 이상이면 반드시 RPE 6 이하 보수적 페이스를 우선 적용). 미입력된 Effort, 미확정 시간, 미측정 센서 값을 절대 추측하지 마세요.
   - 최근 3회 중 수동 Effort 또는 센서 보조지표가 1건 이상 있으면 JSON의 "effort_reflection_reason"에 어떤 최근 기록(종목·수행시간·강도)을 오늘 조언에 어떻게 반영했는지 한국어 1~2문장으로 설명하세요. 둘 다 없으면 빈 문자열("")로 출력하세요.
4. **스케일링 및 대체안**:
   - 피로도가 높은 부위에 무리가 가는 동작은 구체적인 대체 동작 또는 중량 조절 가이드를 제안하세요. (예: HSPU -> Push-up, Snatch 중량 15% 감량 등)
5. **프리-WOD 필수 모빌리티**:
   - 피로 부위 및 오늘 WOD에 필요한 최적의 워밍업/모빌리티 동작 1~2개를 구체적 소요 시간과 함께 추천하세요.
6. **어조**:
   - 크로스핏 코치답게 전문적이고 직관적이며 동기부여가 되는 한국어 톤을 유지하세요.

## 출력 형식
반드시 아래와 같은 유효한 JSON 형식 하나만 출력하세요. 마크다운 코드블록이나 다른 설명 문장은 일체 포함하지 마세요.

{
  "muscle_readiness": [
    {
      "group": "shoulders_push",
      "name_ko": "어깨 / 상체 밀기",
      "fatigue_score": 75,
      "state": "fatigued",
      "state_ko": "피로 주의",
      "note": "어제 스내치 및 푸시프레스로 인해 어깨와 삼두에 높은 피로 누적"
    }
  ],
  "target_rpe": {
    "score": 7,
    "label": "RPE 7 (조절된 페이스)",
    "pacing_strategy": "초반 2라운드는 80% 템포로 호흡을 유지하고, 오버헤드 동작 시 무리한 연속 반복을 피하세요."
  },
  "effort_reflection_reason": "직전 WOD에서 높은 체감 강도(Effort 9/10)가 기록되어 초반 오버페이스를 막기 위해 목표 RPE를 7로 낮추고 세트 분할을 권장했습니다.",
  "scaling_advice": [
    {
      "movement": "Handstand Push-up",
      "recommendation": "스케일링 추천",
      "detail": "어깨 피로도가 높으므로 박스 핸드스탠드 푸시업 또는 인클라인 푸시업으로 변경 권장"
    }
  ],
  "mobility_warmup": [
    {
      "title": "Thoracic Extension over Foam Roller (흉추 폼롤러 스트레칭)",
      "target_area": "Upper Back / Shoulders",
      "duration": "2분",
      "reason": "오버헤드 동작 전 흉추 가동성을 확보하여 어깨 부담 경감"
    }
  ],
  "overall_summary": "어깨 피로도가 높으므로 오늘 오버헤드 동작은 템포를 조절하고, 상대적으로 신선한 하체 힘을 적극 활용하세요."
}
`)

	return sb.String()
}

// FinalizePreWODAdvice ensures deterministic output restrictions and enforces evidence status guarantees.
func FinalizePreWODAdvice(
	resp *PreWODAdviceResponse,
	readiness ProfileReadinessState,
	recentWorkouts ...RecentWorkoutSummary,
) {
	if resp == nil {
		return
	}

	rawReason := strings.TrimSpace(resp.EffortReflectionReason)
	resp.EffortReflectionReason = ""
	if rawReason == "" && resp.EffortReflection != nil {
		rawReason = strings.TrimSpace(resp.EffortReflection.Reason)
	}

	// Always synchronize authoritative readiness state
	resp.EvidenceStatus = readiness.EvidenceStatus
	resp.Evidence = readiness.Evidence
	resp.AsOf = readiness.AsOf
	resp.OverallFatigueScore = readiness.OverallFatigueScore
	resp.OverallState = readiness.OverallState
	resp.OverallStateKO = readiness.OverallStateKO
	resp.LastWorkoutAt = readiness.LastWorkoutAt

	switch readiness.EvidenceStatus {
	case "no_history", "insufficient":
		resp.TargetRPE = nil
		resp.EffortReflection = nil
		resp.AdviceCode = "check_condition"
		resp.OverallSummary = "최근 운동 근거만으로 강도를 판단할 수 없습니다. 실제 컨디션을 확인하세요"
		resp.MuscleReadiness = nil
		resp.ScalingAdvice = []ScalingAdviceItem{}
		resp.MobilityWarmup = []MobilityWarmupItem{}

	case "partial":
		resp.TargetRPE = nil
		resp.EffortReflection = nil
		resp.AdviceCode = "partial_history"
		resp.OverallSummary = "일부 확인된 운동 이력 기반의 참고 안내입니다. 몸 상태에 맞춰 강도를 조절하세요"

		// Synchronize authoritative muscle readiness from server calculation
		syncMuscleReadiness(resp, readiness)

		// Replace free-text scaling with deterministic guidance per §11.2
		resp.ScalingAdvice = []ScalingAdviceItem{
			{
				Movement:       "전체 운동",
				Recommendation: "컨디션 조절 권장",
				Detail:         "일부 운동 이력만 반영되었으므로 고강도 무리한 반복을 피하고 컨디션에 맞춰 무게와 템포를 조절하세요.",
			},
		}
		if resp.MobilityWarmup == nil {
			resp.MobilityWarmup = []MobilityWarmupItem{}
		}

	case "complete":
		if resp.AdviceCode == "" {
			resp.AdviceCode = "standard_guidance"
		}
		// Synchronize authoritative muscle readiness from server calculation
		syncMuscleReadiness(resp, readiness)

		if resp.TargetRPE != nil {
			if resp.TargetRPE.Score < 1 {
				resp.TargetRPE.Score = 1
			} else if resp.TargetRPE.Score > 10 {
				resp.TargetRPE.Score = 10
			}
			// Low self-reported Effort or low sensor hybrid intensity must NEVER override high muscle fatigue (>= 60).
			if readiness.OverallFatigueScore != nil && *readiness.OverallFatigueScore >= 60 && resp.TargetRPE.Score > 6 {
				resp.TargetRPE.Score = 6
				resp.TargetRPE.Label = "RPE 6 (피로 관리 및 기술 중심)"
			}
			resp.TargetRPE.PacingStrategy = sanitizeCoachingText(resp.TargetRPE.PacingStrategy)
		}
		resp.OverallSummary = sanitizeCoachingText(resp.OverallSummary)

		resp.EffortReflection = buildAuthoritativeEffortReflection(recentWorkouts, readiness, rawReason)
	}
}

func formatDurationKO(seconds int) string {
	if seconds <= 0 {
		return "시간 미확정"
	}
	mins := seconds / 60
	secs := seconds % 60
	if mins == 0 {
		return fmt.Sprintf("%d초", secs)
	}
	if secs == 0 {
		return fmt.Sprintf("%d분", mins)
	}
	return fmt.Sprintf("%d분 %d초", mins, secs)
}

func formatHybridSensorBriefKO(h HybridSensorBrief) string {
	var details []string
	if h.HeartIntensity != nil {
		details = append(details, fmt.Sprintf("심박 %.0f", math.Round(*h.HeartIntensity*100)))
	}
	if h.MotionIntensity != nil {
		details = append(details, fmt.Sprintf("움직임 %.0f", math.Round(*h.MotionIntensity*100)))
	}
	if h.HybridLoadAU != nil {
		details = append(details, fmt.Sprintf("누적부하 %.1f AU", *h.HybridLoadAU))
	}
	details = append(details, fmt.Sprintf("커버리지 %.0f%%", math.Round(h.DataCoverage*100)))
	return fmt.Sprintf("센서 보조지표: 하이브리드 활동강도 %.0f/100 (%s)", math.Round(h.HybridIntensity), strings.Join(details, " · "))
}

func sanitizeCoachingText(text string) string {
	out := strings.ReplaceAll(text, "기록 경신을 보장", "참고 페이스로 조절")
	out = strings.ReplaceAll(out, "최대 강도로 도전", "목표 페이스에 맞춰 조절")
	out = strings.ReplaceAll(out, "완전히 회복되었", "컨디션을 점검하며 진행할 수 있")
	out = strings.ReplaceAll(out, "완벽히 회복되었", "컨디션을 점검하며 진행할 수 있")
	out = strings.ReplaceAll(out, "회복이 완료되었", "컨디션을 점검하며 진행할 수 있")
	out = strings.ReplaceAll(out, "쉬운 근력 운동", "중량 확인이 필요한 근력 운동")
	return out
}

func containsProhibitedReflectionClaim(reason string) bool {
	if reason == "" {
		return false
	}
	upper := strings.ToUpper(reason)
	if strings.Contains(upper, "RIR") {
		return true
	}
	prohibited := []string{
		"완전히 회복",
		"완벽히 회복",
		"회복이 완료",
		"회복을 보장",
		"회복 상태가 보장",
		"기록 경신을 보장",
		"쉬운 근력 운동",
	}
	for _, phrase := range prohibited {
		if strings.Contains(reason, phrase) {
			return true
		}
	}
	return false
}

func buildAuthoritativeEffortReflection(
	recentWorkouts []RecentWorkoutSummary,
	readiness ProfileReadinessState,
	rawReason string,
) *EffortReflectionInfo {
	if len(recentWorkouts) == 0 {
		return nil
	}

	limit := len(recentWorkouts)
	if limit > maxRecentWorkoutsInPrompt {
		limit = maxRecentWorkoutsInPrompt
	}

	cleaned := make([]RecentWorkoutSummary, 0, limit)
	ratedCount := 0
	sensorCount := 0

	for i := 0; i < limit; i++ {
		item := recentWorkouts[i]
		movCopy := make([]string, len(item.Movements))
		copy(movCopy, item.Movements)
		item.Movements = movCopy

		if item.DurationSeconds != nil {
			if *item.DurationSeconds <= 0 {
				item.DurationSeconds = nil
			} else {
				d := *item.DurationSeconds
				item.DurationSeconds = &d
			}
		}

		if item.Effort != nil {
			if *item.Effort < 1 || *item.Effort > 10 {
				item.Effort = nil
			} else {
				e := *item.Effort
				item.Effort = &e
				ratedCount++
			}
		}

		if item.HybridSensor != nil {
			hs := *item.HybridSensor
			item.HybridSensor = &hs
			sensorCount++
		}

		cleaned = append(cleaned, item)
	}

	applied := ratedCount > 0 || sensorCount > 0
	reason := ""
	if applied {
		if rawReason != "" && !containsProhibitedReflectionClaim(rawReason) {
			reason = sanitizeCoachingText(rawReason)
		} else {
			reason = BuildDeterministicEffortReflectionReason(cleaned, readiness)
		}
	}

	return &EffortReflectionInfo{
		Applied:        applied,
		RatedCount:     ratedCount,
		SensorCount:    sensorCount,
		TotalRecent:    len(cleaned),
		Reason:         reason,
		RecentWorkouts: cleaned,
	}
}

// BuildDeterministicEffortReflectionReason creates a non-speculative explanation of how recent WOD feedback shaped pacing advice.
func BuildDeterministicEffortReflectionReason(
	recent []RecentWorkoutSummary,
	readiness ProfileReadinessState,
) string {
	var (
		ratedCount      int
		sensorCount     int
		highEffortCount int
		lowEffortCount  int
		latestRated     *RecentWorkoutSummary
		latestSensor    *RecentWorkoutSummary
	)

	for i := range recent {
		w := &recent[i]
		if w.Effort != nil && *w.Effort >= 1 && *w.Effort <= 10 {
			ratedCount++
			if latestRated == nil {
				latestRated = w
			}
			if *w.Effort >= 8 {
				highEffortCount++
			} else if *w.Effort <= 5 {
				lowEffortCount++
			}
		}
		if w.HybridSensor != nil {
			sensorCount++
			if latestSensor == nil {
				latestSensor = w
			}
		}
	}

	if ratedCount == 0 && sensorCount == 0 {
		return ""
	}

	highFatigue := readiness.OverallFatigueScore != nil && *readiness.OverallFatigueScore >= 60
	if highFatigue {
		if ratedCount > 0 && lowEffortCount > 0 && highEffortCount == 0 {
			return "최근 WOD의 자기보고 강도(Effort)는 비교적 낮았으나 6대 근육 누적 피로도가 높아 회복 완료로 단정하지 않고 보수적 페이스(RPE 6 이하)를 우선 적용했습니다."
		}
		if ratedCount > 0 {
			return fmt.Sprintf("최근 %d회 WOD의 강도 피드백과 높은 6대 근육 누적 피로도를 함께 반영해 무리한 반복을 피하도록 목표 강도(RPE 6 이하)를 보수적으로 제한했습니다.", ratedCount)
		}
		return fmt.Sprintf("최근 %d회 WOD의 센서 하이브리드 활동강도와 높은 근육 누적 피로도를 함께 고려해 보수적 페이스(RPE 6 이하)로 제한했습니다.", sensorCount)
	}

	if ratedCount > 0 {
		workoutLabel := formatRecentWorkoutAnchor(*latestRated)
		switch {
		case highEffortCount > 0:
			base := fmt.Sprintf("%s에서 높은 체감 강도(Effort %d/10)가 기록되어 초반 오버페이스를 줄이고 세트 분할과 호흡 관리에 집중하도록 페이스를 조정했습니다.", workoutLabel, *latestRated.Effort)
			if sensorCount > 0 && latestSensor != nil {
				base += fmt.Sprintf(" (센서 하이브리드 활동강도 %.0f/100 보조 참고)", math.Round(latestSensor.HybridSensor.HybridIntensity))
			}
			return base
		case lowEffortCount > 0:
			base := fmt.Sprintf("%s을 비교적 여유 있는 체감 강도(Effort %d/10)로 소화했으나 실제 회복을 단정하지 않고 현재 근육 피로도 범위 안에서 안정적인 빌드업 페이스를 제안했습니다.", workoutLabel, *latestRated.Effort)
			if sensorCount > 0 && latestSensor != nil {
				base += fmt.Sprintf(" (센서 하이브리드 활동강도 %.0f/100 보조 참고)", math.Round(latestSensor.HybridSensor.HybridIntensity))
			}
			return base
		default:
			base := fmt.Sprintf("%s의 체감 강도(Effort %d/10)와 수행 기록을 바탕으로 일정한 랩타임을 유지하는 표준 페이스를 제안했습니다.", workoutLabel, *latestRated.Effort)
			if sensorCount > 0 && latestSensor != nil {
				base += fmt.Sprintf(" (센서 하이브리드 활동강도 %.0f/100 보조 참고)", math.Round(latestSensor.HybridSensor.HybridIntensity))
			}
			return base
		}
	}

	// Only verified v3 sensor summary is present (manual Effort omitted).
	if latestSensor != nil && latestSensor.HybridSensor != nil {
		workoutLabel := formatRecentWorkoutAnchor(*latestSensor)
		score := math.Round(latestSensor.HybridSensor.HybridIntensity)
		if score >= 80 {
			return fmt.Sprintf("수동 Effort는 미입력이지만 %s의 센서 하이브리드 활동강도(%.0f/100)가 높게 측정되어 초반 페이스 여유를 두도록 조정했습니다.", workoutLabel, score)
		}
		return fmt.Sprintf("수동 Effort는 미입력이며 %s의 센서 하이브리드 활동강도(%.0f/100)를 보조 참고하되 고중량 저속 동작 한계를 고려해 근육 피로도 중심의 페이스를 제안했습니다.", workoutLabel, score)
	}

	return ""
}

func formatRecentWorkoutAnchor(w RecentWorkoutSummary) string {
	movPart := "직전 WOD"
	if len(w.Movements) > 0 {
		limit := len(w.Movements)
		if limit > 2 {
			limit = 2
		}
		movPart = fmt.Sprintf("최근 %s WOD", strings.Join(w.Movements[:limit], "·"))
	}
	if w.DurationSeconds != nil && *w.DurationSeconds > 0 {
		return fmt.Sprintf("%s(%s)", movPart, formatDurationKO(*w.DurationSeconds))
	}
	return movPart
}

func syncMuscleReadiness(resp *PreWODAdviceResponse, readiness ProfileReadinessState) {
	if len(readiness.Muscles) == 0 {
		return
	}

	noteMap := make(map[string]string)
	for _, item := range resp.MuscleReadiness {
		if item.Note != "" && !strings.Contains(item.Note, "기록 경신") && !strings.Contains(item.Note, "최대 강도") {
			noteMap[item.Group] = item.Note
		}
	}

	synced := make([]MuscleReadinessItem, 0, len(AllMuscleGroups))
	for _, g := range AllMuscleGroups {
		if m, ok := readiness.Muscles[g]; ok {
			note := noteMap[g]
			if note == "" {
				note = fmt.Sprintf("%s 부하 점수: %d/100 (%s)", m.NameKO, m.FatigueScore, m.StateKO)
			}
			synced = append(synced, MuscleReadinessItem{
				Group:        g,
				NameKO:       m.NameKO,
				FatigueScore: m.FatigueScore,
				State:        m.State,
				StateKO:      m.StateKO,
				Note:         note,
			})
		}
	}
	resp.MuscleReadiness = synced
}
