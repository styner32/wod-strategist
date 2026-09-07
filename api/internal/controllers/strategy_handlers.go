package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/fatigue"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

var jsonBlockRegex = regexp.MustCompile(`(?s)\{.*\}`)

// GetPreWODAdvice handles POST /api/v1/strategies/pre-wod-advice.
//
// @Summary      Get Pre-WOD Strategy & Scaling Advice
// @Description  Calculates 6-muscle group readiness from recent workouts and returns tailored coaching advice for today's WOD.
// @Tags         strategy
// @Accept       json
// @Produce      json
// @Param        request body PreWODAdviceRequest true "Pre-WOD Advice Request"
// @Success      200 {object} PreWODAdviceResponse
// @Failure      400 {object} ErrorResponse
// @Failure      403 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /strategies/pre-wod-advice [post]
func (ctl *Controller) GetPreWODAdvice(c *gin.Context) {
	var req PreWODAdviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: profile_id is required"})
		return
	}

	if !ctl.assertOwnsProfile(c, req.ProfileID) {
		return
	}

	ctx := c.Request.Context()

	// 1. Fetch Profile for Fitness Level & Injuries
	profile, err := ctl.profiles.FindByID(ctx, req.ProfileID)
	if err != nil || profile == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile not found"})
		return
	}

	var injuries []string
	if profile.Injuries != nil && *profile.Injuries != "" {
		_ = json.Unmarshal([]byte(*profile.Injuries), &injuries)
	}

	// 2. Fetch recent non-archived completed sessions from past 7 days
	now := time.Now()
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)

	var pastResults []db.AnalysisResult
	err = ctl.db.WithContext(ctx).
		Where("profile_id = ? AND status = 'COMPLETED' AND archived_at IS NULL AND created_at >= ?", req.ProfileID, sevenDaysAgo).
		Order("created_at DESC").
		Limit(20).
		Find(&pastResults).Error
	if err != nil {
		logger.Log.Error("failed to query past sessions for pre-wod advice", zap.Uint("profile_id", req.ProfileID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to calculate muscle readiness"})
		return
	}

	// 3. Build SessionLoadRecords (computed on-the-fly from session scores)
	records := make([]fatigue.SessionLoadRecord, 0, len(pastResults))
	for _, res := range pastResults {
		muscleLoads := fatigue.ComputeSessionMuscleLoads(res.SessionScore, nil, 0)
		records = append(records, fatigue.SessionLoadRecord{
			SessionID:   res.SessionID,
			CreatedAt:   res.CreatedAt,
			MuscleLoads: muscleLoads,
		})
	}

	// 4. Compute deterministic readiness with exponential decay
	readiness := fatigue.ComputeCurrentReadiness(records, now)

	// 5. Build prompt and invoke Gemini (if available)
	prompt := fatigue.BuildPreWODAdvicePrompt(readiness, *profile, req.WODDescription, req.Movements, injuries)

	var resp PreWODAdviceResponse
	generated := false

	if ctl.textParser != nil {
		rawResp, _, geminiErr := ctl.textParser.ParseText(ctx, prompt)
		if geminiErr == nil && rawResp != "" {
			match := jsonBlockRegex.FindString(rawResp)
			if match != "" {
				if err := json.Unmarshal([]byte(match), &resp); err == nil {
					generated = true
				}
			}
		} else if geminiErr != nil {
			logger.Log.Warn("Gemini pre-wod advice text parsing failed, using deterministic fallback", zap.Error(geminiErr))
		}
	}

	// 6. Deterministic fallback if Gemini is disabled or unparseable
	if !generated {
		resp = buildDeterministicFallbackAdvice(req.ProfileID, readiness, *profile, req.WODDescription, req.Movements, injuries)
	}

	// Ensure core fields are populated
	resp.ProfileID = req.ProfileID
	resp.OverallFatigueScore = readiness.OverallFatigueScore
	resp.OverallState = readiness.OverallState
	resp.OverallStateKO = readiness.OverallStateKO
	resp.LastWorkoutAt = readiness.LastWorkoutAt

	// Ensure all 6 muscles exist in response
	if len(resp.MuscleReadiness) == 0 {
		items := make([]MuscleReadinessItem, 0, len(fatigue.AllMuscleGroups))
		for _, g := range fatigue.AllMuscleGroups {
			m := readiness.Muscles[g]
			items = append(items, MuscleReadinessItem{
				Group:        g,
				NameKO:       m.NameKO,
				FatigueScore: m.FatigueScore,
				State:        m.State,
				StateKO:      m.StateKO,
				Note:         fmt.Sprintf("%s 상태입니다.", m.StateKO),
			})
		}
		resp.MuscleReadiness = items
	}

	c.JSON(http.StatusOK, resp)
}

func buildDeterministicFallbackAdvice(
	profileID uint,
	readiness fatigue.ProfileReadinessState,
	profile db.Profile,
	wodDescription string,
	movements []string,
	injuries []string,
) PreWODAdviceResponse {
	items := make([]MuscleReadinessItem, 0, len(fatigue.AllMuscleGroups))
	var fatiguedMuscles []string
	var freshMuscles []string

	for _, g := range fatigue.AllMuscleGroups {
		m := readiness.Muscles[g]
		note := "정상 컨디션입니다."
		if m.FatigueScore >= 50 {
			note = "최근 운동으로 인해 피로가 누적되어 주의가 필요합니다."
			fatiguedMuscles = append(fatiguedMuscles, m.NameKO)
		} else if m.FatigueScore <= 25 {
			note = "충분히 회복되어 최상의 수행 능력을 낼 수 있습니다."
			freshMuscles = append(freshMuscles, m.NameKO)
		}
		items = append(items, MuscleReadinessItem{
			Group:        g,
			NameKO:       m.NameKO,
			FatigueScore: m.FatigueScore,
			State:        m.State,
			StateKO:      m.StateKO,
			Note:         note,
		})
	}

	rpeScore := 8
	rpeLabel := "RPE 8 (표준 강도)"
	pacing := "일정한 랩타임을 유지하며 고른 호흡으로 완주하세요."

	if readiness.OverallFatigueScore >= 60 {
		rpeScore = 6
		rpeLabel = "RPE 6~7 (회복 및 기술 중심)"
		pacing = "전체적인 피로도가 높으므로 무리한 최고 기록 도전보다 자세 유지와 템포 조절에 집중하세요."
	} else if readiness.OverallFatigueScore <= 25 {
		rpeScore = 9
		rpeLabel = "RPE 9 (최대 수행 도전)"
		pacing = "신체 컨디션이 매우 우수하므로 적극적인 페이스로 목표 기록 경신에 도전해보세요."
	}

	var scalings []ScalingAdviceItem
	for _, m := range movements {
		weights := fatigue.GetMovementWeights(m)
		if weights.ShouldersPush > 0.6 && readiness.Muscles[fatigue.GroupShouldersPush].FatigueScore >= 50 {
			scalings = append(scalings, ScalingAdviceItem{
				Movement:       m,
				Recommendation: "중량 조절 또는 스케일링",
				Detail:         "어깨 피로도가 높으므로 처방 무게의 80% 수준으로 조절하거나 파워 동작으로 변경 권장합니다.",
			})
		} else if weights.PosteriorChain > 0.7 && readiness.Muscles[fatigue.GroupPosteriorChain].FatigueScore >= 50 {
			scalings = append(scalings, ScalingAdviceItem{
				Movement:       m,
				Recommendation: "허리 중립 유지 및 감량",
				Detail:         "후면사슬 피로로 인해 요추 굴곡 보상이 나타날 수 있으니 중량을 낮추고 셋업 자세를 철저히 점검하세요.",
			})
		}
	}

	var mobility []MobilityWarmupItem
	if readiness.Muscles[fatigue.GroupShouldersPush].FatigueScore >= 40 {
		mobility = append(mobility, MobilityWarmupItem{
			Title:      "Thoracic Extension (흉추 가동성 스트레칭)",
			TargetArea: "Upper Back & Shoulders",
			Duration:   "2분",
			Reason:     "상체 밀기 동작 시 어깨 부담을 줄이고 흉추 신전을 돕습니다.",
		})
	}
	if readiness.Muscles[fatigue.GroupPosteriorChain].FatigueScore >= 40 || readiness.Muscles[fatigue.GroupQuadsSquat].FatigueScore >= 40 {
		mobility = append(mobility, MobilityWarmupItem{
			Title:      "Pigeon Pose (비둘기 자세 스트레칭)",
			TargetArea: "Hips & Glutes",
			Duration:   "각 1분",
			Reason:     "고관절 및 둔근을 이완하여 스쿼트와 리프팅 시 가동범위를 확보합니다.",
		})
	}
	if len(mobility) == 0 {
		mobility = append(mobility, MobilityWarmupItem{
			Title:      "Samson Stretch & Cat-Cow",
			TargetArea: "Full Body & Spine",
			Duration:   "2분",
			Reason:     "전신 척추 및 고관절 굴곡근을 활성화하여 본 운동을 준비합니다.",
		})
	}

	summary := "오늘의 신체 준비도를 확인하고, 피로 부위를 고려한 맞춤 페이스로 부상 없이 운동을 완주하세요."
	if len(fatiguedMuscles) > 0 {
		summary = fmt.Sprintf("%s 부위에 피로가 다소 누적되어 있으니 해당 부위 동작 시 자세와 템포에 주의하세요.", strings.Join(fatiguedMuscles, ", "))
	}

	return PreWODAdviceResponse{
		ProfileID:           profileID,
		OverallFatigueScore: readiness.OverallFatigueScore,
		OverallState:        readiness.OverallState,
		OverallStateKO:      readiness.OverallStateKO,
		MuscleReadiness:     items,
		TargetRPE: TargetRPEInfo{
			Score:          rpeScore,
			Label:          rpeLabel,
			PacingStrategy: pacing,
		},
		ScalingAdvice:  scalings,
		MobilityWarmup: mobility,
		OverallSummary: summary,
		LastWorkoutAt:  readiness.LastWorkoutAt,
	}
}
