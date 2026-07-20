package controllers

import (
	"encoding/json"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/worker"
)

// normalizeHighlightResultsForResponse upgrades recognized legacy highlight
// arrays to the v2 playback-event contract without changing stored rows. An
// unrecognized legacy shape is preserved so older clients do not lose data.
func normalizeHighlightResultsForResponse(results []db.AnalysisResult) []db.AnalysisResult {
	for i := range results {
		results[i].HighlightSegments = normalizeHighlightJSONForResponse(results[i].HighlightSegments)
	}
	return results
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
