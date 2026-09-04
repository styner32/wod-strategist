package controllers

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/fatigue"
	"github.com/wod-strategist/api/internal/worker"
)

// normalizeHighlightResultsForResponse upgrades recognized legacy highlight
// arrays to the v2 playback-event contract without changing stored rows. An
// unrecognized legacy shape is preserved so older clients do not lose data.
// It also populates session_fatigue dynamically for completed workouts.
func normalizeHighlightResultsForResponse(results []db.AnalysisResult) []db.AnalysisResult {
	for i := range results {
		results[i].HighlightSegments = normalizeHighlightJSONForResponse(results[i].HighlightSegments)
		populateSessionFatigue(&results[i])
	}
	return results
}

func populateSessionFatigue(res *db.AnalysisResult) {
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
		return
	}

	loads := fatigue.ComputeSessionMuscleLoads(scoreJSON, nil, 0)
	var total float64
	muscles := make(map[string]int, len(fatigue.AllMuscleGroups))
	for _, g := range fatigue.AllMuscleGroups {
		score := int(math.Round(loads[g]))
		muscles[g] = score
		total += float64(score)
	}
	overallScore := int(math.Round(total / float64(len(fatigue.AllMuscleGroups))))
	state, stateKO := fatigue.StateFromFatigueScore(overallScore)

	res.SessionFatigue = &db.SessionFatigue{
		OverallScore: overallScore,
		State:        state,
		StateKO:      stateKO,
		Muscles:      muscles,
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
