package controllers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/fatigue"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/movement"
	"github.com/wod-strategist/api/internal/sensor"
	"github.com/wod-strategist/api/internal/wodeval"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var jsonBlockRegex = regexp.MustCompile(`(?s)\{.*\}`)

type recentWODFeedbackRow struct {
	SessionID            string
	WorkoutAt            time.Time
	SessionScore         string
	NormalizedWorkout    string
	SensorVersion        int64
	SensorState          string
	SensorProcessing     db.JSONDocument
	SensorSummary        db.JSONDocument
	WorkoutType          *string
	WODEvaluationVersion *int
	Effort               *int
}

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

	// 2. Query in read-only repeatable read transaction
	asOf := time.Now().UTC()
	sevenDaysAgo := asOf.Add(-7 * 24 * time.Hour)
	twentyEightDaysAgo := asOf.Add(-28 * 24 * time.Hour)

	var pastResults []db.AnalysisResult
	var recentWODRows []recentWODFeedbackRow
	var unresolvedCount int64

	txErr := ctl.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY").Error

		// Count unresolved time sessions: completed, non-archived, but workout_at is NULL or not reliable or future
		if err := tx.Model(&db.AnalysisResult{}).
			Where("profile_id = ? AND status = 'COMPLETED' AND archived_at IS NULL", req.ProfileID).
			Where("(workout_at IS NULL OR workout_at > ? OR workout_at_source IS NULL OR workout_at_source NOT IN ('session_ulid', 'legacy_local_time'))", asOf).
			Count(&unresolvedCount).Error; err != nil {
			return err
		}

		// Fetch all sessions with reliable workout_at in [asOf-7d, asOf] without arbitrary limit
		if err := tx.Where("profile_id = ? AND status = 'COMPLETED' AND archived_at IS NULL", req.ProfileID).
			Where("workout_at >= ? AND workout_at <= ?", sevenDaysAgo, asOf).
			Where("workout_at_source IN ('session_ulid', 'legacy_local_time')").
			Order("workout_at DESC").
			Find(&pastResults).Error; err != nil {
			return err
		}

		// Fetch recent up to 3 completed non-archived main WOD sessions in [asOf-28d, asOf]
		return tx.Table("analysis_results AS a").
			Select("a.session_id, a.workout_at, a.session_score, a.normalized_workout, a.sensor_version, a.sensor_state, a.sensor_processing, a.sensor_summary, s.workout_type, s.wod_evaluation_version, s.effort").
			Joins("LEFT JOIN sessions s ON s.session_id = a.session_id AND s.profile_id = a.profile_id").
			Where("a.profile_id = ? AND a.status = 'COMPLETED' AND a.archived_at IS NULL", req.ProfileID).
			Where("COALESCE(NULLIF(a.analysis_type, ''), ?) = ?", db.AnalysisTypeWOD, db.AnalysisTypeWOD).
			Where("COALESCE(NULLIF(s.workout_type, ''), ?) = ?", "wod", "wod").
			Where("a.workout_at >= ? AND a.workout_at <= ?", twentyEightDaysAgo, asOf).
			Where("a.workout_at_source IN ('session_ulid', 'legacy_local_time')").
			Order("a.workout_at DESC, a.id DESC").
			Limit(3).
			Scan(&recentWODRows).Error
	})

	if txErr != nil {
		logger.Log.Error("failed to query past sessions for pre-wod advice", zap.Uint("profile_id", req.ProfileID), zap.Error(txErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to calculate muscle readiness"})
		return
	}

	totalSessions := len(pastResults)
	validSessions := 0
	records := make([]fatigue.SessionLoadRecord, 0, len(pastResults))

	for _, res := range pastResults {
		loads, ok := fatigue.ComputeSessionMuscleLoads(res.SessionScore)
		if ok {
			validSessions++
			workoutTime := asOf
			if res.WorkoutAt != nil {
				workoutTime = *res.WorkoutAt
			}
			records = append(records, fatigue.SessionLoadRecord{
				SessionID:   res.SessionID,
				CreatedAt:   workoutTime,
				MuscleLoads: loads,
			})
		}
	}

	evidence := fatigue.EvidenceCounts{
		TotalSessions:          totalSessions,
		ValidSessions:          validSessions,
		ExcludedSessions:       totalSessions - validSessions,
		UnresolvedTimeSessions: int(unresolvedCount),
	}

	// 3. Compute deterministic readiness and recent 3 WOD summaries
	readiness := fatigue.ComputeCurrentReadiness(records, evidence, asOf)
	recentWorkouts := buildRecentWorkoutSummaries(recentWODRows)

	var resp PreWODAdviceResponse
	resp.ProfileID = req.ProfileID

	// 4. Gemini call: ONLY when evidence_status == "complete"
	if readiness.EvidenceStatus == "complete" && ctl.textParser != nil {
		prompt := fatigue.BuildPreWODAdvicePrompt(readiness, *profile, req.WODDescription, req.Movements, injuries, recentWorkouts...)
		rawResp, usage, geminiErr := ctl.textParser.ParseText(ctx, prompt)
		ctl.recordTokenUsage(c, req.ProfileID, "strategy:pre-wod", usage)
		if geminiErr == nil && rawResp != "" {
			match := jsonBlockRegex.FindString(rawResp)
			if match != "" {
				_ = json.Unmarshal([]byte(match), &resp)
			}
		} else if geminiErr != nil {
			logger.Log.Warn("Gemini pre-wod advice text parsing failed, using deterministic fallback", zap.Error(geminiErr))
		}
	}

	if resp.TargetRPE == nil && readiness.EvidenceStatus == "complete" {
		resp = buildDeterministicFallbackAdvice(req.ProfileID, readiness, *profile, req.WODDescription, req.Movements, injuries, recentWorkouts)
	}

	// 5. Always apply FinalizePreWODAdvice
	fatigue.FinalizePreWODAdvice(&resp, readiness, recentWorkouts...)

	c.JSON(http.StatusOK, resp)
}

func buildRecentWorkoutSummaries(rows []recentWODFeedbackRow) []RecentWorkoutSummary {
	if len(rows) == 0 {
		return nil
	}
	out := make([]RecentWorkoutSummary, 0, len(rows))
	for _, r := range rows {
		var durationSeconds *int
		if score, ok := wodeval.Decode(r.SessionScore); ok {
			if dur := wodeval.DurationSeconds(score.WODStart); dur != nil && *dur > 0 {
				rounded := int(math.Round(*dur))
				if rounded > 0 {
					durationSeconds = &rounded
				}
			}
		}

		var manualEffort *int
		if r.WorkoutType != nil && *r.WorkoutType == "wod" &&
			r.WODEvaluationVersion != nil && *r.WODEvaluationVersion == wodeval.Version &&
			r.Effort != nil && *r.Effort >= 1 && *r.Effort <= 10 {
			e := *r.Effort
			manualEffort = &e
		}

		var hybridBrief *HybridSensorBrief
		candidate := db.AnalysisResult{
			SensorVersion:    r.SensorVersion,
			SensorState:      r.SensorState,
			SensorProcessing: r.SensorProcessing,
			SensorSummary:    r.SensorSummary,
		}
		if fresh := evaluateSensorSummaryFreshness(&candidate); fresh.Valid && fresh.CalculationVersion >= 3 {
			var summary sensor.SensorSummaryResult
			if json.Unmarshal(r.SensorSummary, &summary) == nil &&
				summary.CalculationVersion >= 3 &&
				summary.Metrics.Hybrid != nil &&
				summary.Metrics.Hybrid.HybridIntensity != nil &&
				summary.Metrics.Hybrid.DataCoverage >= 0.50 &&
				summary.Metrics.Hybrid.ValidHybridSeconds >= 60 {
				hy := summary.Metrics.Hybrid
				hybridBrief = &HybridSensorBrief{
					HeartIntensity:     hy.HeartIntensity,
					MotionIntensity:    hy.MotionIntensity,
					HybridIntensity:    *hy.HybridIntensity,
					HybridLoadAU:       hy.HybridLoadAU,
					DataCoverage:       hy.DataCoverage,
					CalibrationVersion: hy.CalibrationVersion,
				}
			}
		}

		out = append(out, RecentWorkoutSummary{
			SessionID:       r.SessionID,
			WorkoutAt:       r.WorkoutAt.UTC(),
			Movements:       extractRecentWorkoutMovements(r.SessionScore, r.NormalizedWorkout),
			DurationSeconds: durationSeconds,
			Effort:          manualEffort,
			HybridSensor:    hybridBrief,
		})
	}
	return out
}

func extractRecentWorkoutMovements(sessionScoreJSON, normalizedWorkoutJSON string) []string {
	seen := make(map[string]struct{})
	var list []string
	addMovement := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		if fatigue.IsNonLoadMovement(movement.NormalizeKey(trimmed)) {
			return
		}
		display := trimmed
		if canonical := movement.Canonical(trimmed); canonical != "" {
			display = canonical
		}
		key := strings.ToLower(display)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		list = append(list, display)
	}

	if trimmed := strings.TrimSpace(sessionScoreJSON); trimmed != "" && trimmed != "{}" {
		var parsed struct {
			Movements map[string]any `json:"movements"`
		}
		if json.Unmarshal([]byte(trimmed), &parsed) == nil && len(parsed.Movements) > 0 {
			keys := make([]string, 0, len(parsed.Movements))
			for k := range parsed.Movements {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				addMovement(k)
			}
		}
	}

	if len(list) == 0 {
		if trimmed := strings.TrimSpace(normalizedWorkoutJSON); trimmed != "" && trimmed != "[]" {
			var normMovs []db.NormalizedMovement
			if json.Unmarshal([]byte(trimmed), &normMovs) == nil {
				for _, m := range normMovs {
					addMovement(m.Movement)
				}
			}
		}
	}

	if list == nil {
		return []string{}
	}
	return list
}

func buildDeterministicFallbackAdvice(
	profileID uint,
	readiness fatigue.ProfileReadinessState,
	profile db.Profile,
	wodDescription string,
	movements []string,
	injuries []string,
	recentWorkouts []RecentWorkoutSummary,
) PreWODAdviceResponse {
	items := make([]MuscleReadinessItem, 0, len(fatigue.AllMuscleGroups))
	var fatiguedMuscles []string

	for _, g := range fatigue.AllMuscleGroups {
		m, ok := readiness.Muscles[g]
		if !ok {
			continue
		}
		note := "정상 컨디션입니다."
		if m.FatigueScore >= 50 {
			note = "최근 운동으로 인해 피로가 누적되어 주의가 필요합니다."
			fatiguedMuscles = append(fatiguedMuscles, m.NameKO)
		} else if m.FatigueScore <= 25 {
			note = "충분히 회복되어 최상의 수행 능력을 낼 수 있습니다."
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

	scoreVal := 50
	if readiness.OverallFatigueScore != nil {
		scoreVal = *readiness.OverallFatigueScore
	}

	if scoreVal >= 60 {
		rpeScore = 6
		rpeLabel = "RPE 6~7 (회복 및 기술 중심)"
		pacing = "전체적인 피로도가 높으므로 무리한 최고 기록 도전보다 자세 유지와 템포 조절에 집중하세요."
	} else if scoreVal <= 25 {
		rpeScore = 9
		rpeLabel = "RPE 9 (최대 수행 도전)"
		pacing = "신체 컨디션이 매우 우수하므로 적극적인 페이스로 목표 기록 경신에 도전해보세요."
	}

	// rpeScore is 8 or 9 below fatigue 60. The same classification drives the
	// effort reflection reason, so the adjustment and its explanation cite the
	// same workout.
	if scoreVal < 60 {
		signal := fatigue.ClassifyRecentEffort(recentWorkouts)
		anchor := ""
		if signal.Anchor != nil {
			anchor = fatigue.FormatRecentWorkoutAnchor(*signal.Anchor, signal.AnchorIndex)
		}
		switch signal.Kind {
		case fatigue.RecentEffortHigh:
			rpeScore--
			rpeLabel = fmt.Sprintf("RPE %d (조절된 페이스)", rpeScore)
			pacing = fmt.Sprintf("%s에서 높은 체감 강도가 기록되었으므로 초반 오버페이스를 줄이고 세트를 계획적으로 나누어 진행하세요.", anchor)
		case fatigue.RecentEffortLow:
			pacing = fmt.Sprintf("%s에서 비교적 여유 있는 체감 강도가 기록되었으므로 초반 1~2라운드에서 몸 상태를 확인한 뒤 점진적으로 템포를 올려보세요.", anchor)
		case fatigue.RecentEffortHighSensor:
			rpeScore--
			rpeLabel = fmt.Sprintf("RPE %d (조절된 페이스)", rpeScore)
			pacing = fmt.Sprintf("%s에서 높은 센서 하이브리드 활동강도가 측정되었으므로 초반 호흡 여유를 확보하며 일정한 페이스를 유지하세요.", anchor)
		}
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
		TargetRPE: &TargetRPEInfo{
			Score:          rpeScore,
			Label:          rpeLabel,
			PacingStrategy: pacing,
		},
		EffortReflectionReason: fatigue.BuildDeterministicEffortReflectionReason(recentWorkouts, readiness),
		ScalingAdvice:          scalings,
		MobilityWarmup:         mobility,
		OverallSummary:         summary,
		LastWorkoutAt:          readiness.LastWorkoutAt,
	}
}
