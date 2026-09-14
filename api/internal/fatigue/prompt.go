package fatigue

import (
	"fmt"
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

type PreWODAdviceResponse struct {
	ProfileID           uint                  `json:"profile_id"`
	OverallFatigueScore *int                  `json:"overall_fatigue_score,omitempty"`
	OverallState        string                `json:"overall_state"`
	OverallStateKO      string                `json:"overall_state_ko"`
	MuscleReadiness     []MuscleReadinessItem `json:"muscle_readiness,omitempty"`
	TargetRPE           *TargetRPEInfo        `json:"target_rpe"`
	ScalingAdvice       []ScalingAdviceItem   `json:"scaling_advice"`
	MobilityWarmup      []MobilityWarmupItem  `json:"mobility_warmup"`
	OverallSummary      string                `json:"overall_summary"`
	AdviceCode          string                `json:"advice_code,omitempty"`
	EvidenceStatus      string                `json:"evidence_status"`
	Evidence            EvidenceCounts        `json:"evidence"`
	AsOf                time.Time             `json:"as_of"`
	LastWorkoutAt       *time.Time            `json:"last_workout_at,omitempty"`
}

// BuildPreWODAdvicePrompt constructs a Gemini prompt to generate structured pre-WOD strategy advice.
func BuildPreWODAdvicePrompt(
	readiness ProfileReadinessState,
	profile db.Profile,
	wodDescription string,
	plannedMovements []string,
	injuries []string,
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
3. **스케일링 및 대체안**:
   - 피로도가 높은 부위에 무리가 가는 동작은 구체적인 대체 동작 또는 중량 조절 가이드를 제안하세요. (예: HSPU -> Push-up, Snatch 중량 15% 감량 등)
4. **프리-WOD 필수 모빌리티**:
   - 피로 부위 및 오늘 WOD에 필요한 최적의 워밍업/모빌리티 동작 1~2개를 구체적 소요 시간과 함께 추천하세요.
5. **어조**:
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
) {
	if resp == nil {
		return
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
		resp.AdviceCode = "check_condition"
		resp.OverallSummary = "최근 운동 근거만으로 강도를 판단할 수 없습니다. 실제 컨디션을 확인하세요"
		resp.MuscleReadiness = nil
		resp.ScalingAdvice = []ScalingAdviceItem{}
		resp.MobilityWarmup = []MobilityWarmupItem{}

	case "partial":
		resp.TargetRPE = nil
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
			resp.TargetRPE.PacingStrategy = strings.ReplaceAll(resp.TargetRPE.PacingStrategy, "기록 경신을 보장", "참고 페이스로 조절")
			resp.TargetRPE.PacingStrategy = strings.ReplaceAll(resp.TargetRPE.PacingStrategy, "최대 강도로 도전", "목표 페이스에 맞춰 조절")
		}
	}
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
