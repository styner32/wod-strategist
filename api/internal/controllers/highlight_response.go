package controllers

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/fatigue"
	"github.com/wod-strategist/api/internal/worker"
)

// workoutLoadSchemaVersion reads capability header X-Workout-Load-Schema
func workoutLoadSchemaVersion(c *gin.Context) int {
	if c == nil {
		return 0
	}
	v := c.GetHeader("X-Workout-Load-Schema")
	if v == "1" {
		return 1
	}
	return 0
}

// normalizeHighlightResultsForResponse upgrades recognized legacy highlight
// arrays to the v2 playback-event contract without changing stored rows. An
// unrecognized legacy shape is preserved so older clients do not lose data.
// It also populates session_fatigue dynamically for completed workouts.
func normalizeHighlightResultsForResponse(results []db.AnalysisResult) []db.AnalysisResult {
	return normalizeHighlightResultsForResponseWithSchema(results, 0)
}

func normalizeHighlightResultsForResponseWithSchema(results []db.AnalysisResult, schemaVersion int) []db.AnalysisResult {
	for i := range results {
		results[i].HighlightSegments = normalizeHighlightJSONForResponse(results[i].HighlightSegments)
		populateSessionFatigueWithSchema(&results[i], schemaVersion)
	}
	return results
}

func populateSessionFatigue(res *db.AnalysisResult) {
	populateSessionFatigueWithSchema(res, 0)
}

type sensorSummaryFreshness struct {
	Valid              bool
	SummaryJSON        string
	HRBonus            float64
	ValidHR            bool
	CalculationVersion int
}

func evaluateSensorSummaryFreshness(res *db.AnalysisResult) sensorSummaryFreshness {
	if res == nil || res.SensorState != db.SensorStateCompleted || res.SensorVersion <= 0 {
		return sensorSummaryFreshness{}
	}
	var proc struct {
		RequestID        string `json:"request_id"`
		TargetGeneration string `json:"target_generation"`
	}
	var sum struct {
		Version            int64  `json:"version"`
		RequestID          string `json:"request_id"`
		SourceGeneration   string `json:"source_generation"`
		CalculationVersion int    `json:"calculation_version"`
		Quality            struct {
			ValidHR    bool `json:"valid_hr"`
			IsComplete bool `json:"is_complete"`
		} `json:"quality"`
		HRBonus float64 `json:"hr_bonus"`
	}
	if err := json.Unmarshal([]byte(res.SensorProcessing), &proc); err != nil {
		return sensorSummaryFreshness{}
	}
	if err := json.Unmarshal([]byte(res.SensorSummary), &sum); err != nil {
		return sensorSummaryFreshness{}
	}
	if sum.Version != res.SensorVersion ||
		sum.RequestID == "" ||
		sum.RequestID != proc.RequestID ||
		sum.SourceGeneration == "" ||
		sum.SourceGeneration != proc.TargetGeneration ||
		sum.CalculationVersion != 1 {
		return sensorSummaryFreshness{}
	}

	return sensorSummaryFreshness{
		Valid:              true,
		SummaryJSON:        string(res.SensorSummary),
		HRBonus:            sum.HRBonus,
		ValidHR:            sum.Quality.ValidHR && sum.Quality.IsComplete,
		CalculationVersion: sum.CalculationVersion,
	}
}

func buildFatigueGuidance(stateCode, adviceCode string, hrAdjusted bool) *db.FatigueGuidance {
	var textEN, textKO string
	switch adviceCode {
	case "low_load":
		textEN = "Estimated workout load is low for this session."
		textKO = "이번 세션의 추정 운동 부하가 낮습니다."
	case "moderate_load":
		textEN = "Estimated workout load is moderate for this session."
		textKO = "이번 세션의 추정 운동 부하가 적정 수준입니다."
	case "high_load":
		textEN = "Estimated workout load is high for this session."
		textKO = "이번 세션의 추정 운동 부하가 높습니다."
	case "extreme_load":
		textEN = "Estimated workout load is very high for this session."
		textKO = "이번 세션의 추정 운동 부하가 매우 높습니다."
	default:
		textEN = "Estimated workout load for this session."
		textKO = "이번 세션의 추정 운동 부하입니다."
	}
	return &db.FatigueGuidance{
		StateCode:  stateCode,
		AdviceCode: adviceCode,
		TextEN:     textEN,
		TextKO:     textKO,
	}
}

func populateSessionFatigueWithSchema(res *db.AnalysisResult, schemaVersion int) {
	if res == nil || res.Status != "COMPLETED" {
		return
	}
	scoreJSON := res.SessionScore
	if scoreJSON == "" || scoreJSON == "{}" {
		if res.Output != "" {
			scoreJSON = worker.ParseSessionScore(res.Output)
		}
	}
	if scoreJSON == "" || scoreJSON == "{}" {
		if schemaVersion == 1 {
			res.SessionFatigue = &db.SessionFatigue{
				Status:                 "insufficient_evidence",
				LoadCalculationVersion: 1,
				SensorStatus:           "none",
			}
		}
		return
	}

	// Verify sensor freshness (§3)
	sensorSummaryJSON := "{}"
	sensorStatus := "none"
	hrAdjusted := false

	freshness := evaluateSensorSummaryFreshness(res)
	if freshness.Valid {
		sensorSummaryJSON = freshness.SummaryJSON
		sensorStatus = "applied"
		if freshness.ValidHR && freshness.HRBonus > 0 {
			hrAdjusted = true
		}
	} else if res.SensorState == db.SensorStatePending || res.SensorState == db.SensorStateRunning || res.SensorState == db.SensorStateUploading {
		sensorStatus = "pending"
	} else if res.SensorState == db.SensorStateFailed || res.SensorState == db.SensorStateExpired {
		sensorStatus = "failed"
	}

	loads, ok := fatigue.ComputeSessionMuscleLoadsWithSensor(scoreJSON, sensorSummaryJSON)
	if !ok {
		if schemaVersion == 1 {
			res.SessionFatigue = &db.SessionFatigue{
				Status:                 "insufficient_evidence",
				LoadCalculationVersion: 1,
				SensorStatus:           sensorStatus,
			}
		}
		return
	}

	var total float64
	muscles := make(map[string]int, len(fatigue.AllMuscleGroups))
	for _, g := range fatigue.AllMuscleGroups {
		score := int(math.Round(loads[g]))
		muscles[g] = score
		total += float64(score)
	}
	overallScore := int(math.Round(total / float64(len(fatigue.AllMuscleGroups))))
	state, stateKO := fatigue.StateFromFatigueScore(overallScore)

	adviceCode := "low_load"
	switch {
	case overallScore <= 25:
		adviceCode = "low_load"
	case overallScore <= 50:
		adviceCode = "moderate_load"
	case overallScore <= 75:
		adviceCode = "high_load"
	default:
		adviceCode = "extreme_load"
	}

	type muscleScore struct {
		group string
		score int
		order int
	}
	var candidates []muscleScore
	for i, g := range fatigue.AllMuscleGroups {
		if muscles[g] >= 50 {
			candidates = append(candidates, muscleScore{group: g, score: muscles[g], order: i})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})
	var focusMuscles []string
	for i := 0; i < len(candidates) && i < 2; i++ {
		focusMuscles = append(focusMuscles, candidates[i].group)
	}

	if schemaVersion == 1 {
		res.SessionFatigue = &db.SessionFatigue{
			Status:                 "available",
			LoadCalculationVersion: 1,
			SensorStatus:           sensorStatus,
			HeartRateAdjusted:      hrAdjusted,
			OverallScore:           overallScore,
			State:                  state,
			StateKO:                stateKO,
			AdviceCode:             adviceCode,
			FocusMuscles:           focusMuscles,
			Muscles:                muscles,
			Guidance:               buildFatigueGuidance(state, adviceCode, hrAdjusted),
		}
	} else {
		res.SessionFatigue = &db.SessionFatigue{
			OverallScore: overallScore,
			State:        state,
			StateKO:      stateKO,
			Muscles:      muscles,
		}
	}
}

func normalizeHighlightJSONForResponse(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "[]" {
		return raw
	}

	segments, err := worker.NormalizeHighlightSegmentsJSON(raw, worker.HighlightNormalizeOptions{
		ConservativeUnknownVideoEnd: true,
	})
	if err != nil || (len(segments) == 0 && !hasRecognizedHighlightShape(raw)) {
		return raw
	}
	return worker.MarshalHighlightSegments(segments)
}

func hasRecognizedHighlightShape(raw string) bool {
	var values []map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &values) != nil {
		return false
	}
	knownKeys := []string{
		"version", "start", "end", "start_time", "end_time", "start_secs", "end_secs",
		"type", "movement", "reason", "description", "tags", "observations",
	}
	for _, value := range values {
		for _, key := range knownKeys {
			if _, ok := value[key]; ok {
				return true
			}
		}
	}
	return len(values) == 0
}
