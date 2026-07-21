package db

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	personalHintSessionLimit = 20
	personalHintResultLimit  = 5
)

// PersonalMovementHint is a weak, profile-scoped confusion watchlist entry.
// Callers must still require direct visual evidence and must not treat this as
// a confirmed movement for a new session.
type PersonalMovementHint struct {
	PredictedMovement string `json:"predicted_movement"`
	CorrectedMovement string `json:"corrected_movement"`
	SessionCount      int    `json:"session_count"`
}

// BuildPersonalMovementHints returns repeated active movement corrections from
// recent sessions. The current session is excluded to avoid a correction
// influencing re-analysis of the evidence that produced it.
func BuildPersonalMovementHints(ctx context.Context, database *gorm.DB, profileID uint, excludeSessionID string) ([]PersonalMovementHint, error) {
	var latest []AnalysisFeedback
	err := database.WithContext(ctx).Raw(`
		WITH current_events AS (
			SELECT DISTINCT ON (feedback_key) *
			FROM analysis_feedback
			WHERE profile_id = ?
			  AND session_id <> ?
			ORDER BY feedback_key, revision DESC
		), recent_feedback_sessions AS (
			SELECT session_id, MAX(created_at) AS latest_feedback_at
			FROM current_events
			WHERE retracted = FALSE
			GROUP BY session_id
			ORDER BY latest_feedback_at DESC
			LIMIT ?
		)
		SELECT current_events.*
		FROM current_events
		JOIN recent_feedback_sessions USING (session_id)
		WHERE current_events.category = ?
		ORDER BY current_events.created_at DESC, current_events.id DESC
	`, profileID, excludeSessionID, personalHintSessionLimit, FeedbackCategoryMovement).Scan(&latest).Error
	if err != nil {
		return nil, err
	}
	return buildPersonalMovementHints(latest), nil
}

type movementHintGroup struct {
	predicted string
	corrected string
	sessions  map[string]struct{}
	latestAt  time.Time
}

func buildPersonalMovementHints(latest []AnalysisFeedback) []PersonalMovementHint {
	includedSessions := make(map[string]struct{}, personalHintSessionLimit)
	groups := make(map[string]*movementHintGroup)

	for _, event := range latest {
		if event.Retracted {
			continue
		}
		if _, included := includedSessions[event.SessionID]; !included {
			if len(includedSessions) >= personalHintSessionLimit {
				continue
			}
			includedSessions[event.SessionID] = struct{}{}
		}

		var original struct {
			ExerciseType         string `json:"exercise_type"`
			ObservedMovementName string `json:"observed_movement_name"`
		}
		var correction struct {
			MovementName string `json:"movement_name"`
		}
		if json.Unmarshal(event.OriginalPrediction, &original) != nil || json.Unmarshal(event.Correction, &correction) != nil {
			continue
		}
		predicted := strings.TrimSpace(original.ObservedMovementName)
		if predicted == "" {
			predicted = strings.TrimSpace(original.ExerciseType)
		}
		corrected := strings.TrimSpace(correction.MovementName)
		predictedKey := normalizeMovementHintName(predicted)
		correctedKey := normalizeMovementHintName(corrected)
		if predictedKey == "" || correctedKey == "" || predictedKey == correctedKey {
			continue
		}

		key := predictedKey + "\x00" + correctedKey
		group, exists := groups[key]
		if !exists {
			group = &movementHintGroup{
				predicted: predicted,
				corrected: corrected,
				sessions:  make(map[string]struct{}),
				latestAt:  event.CreatedAt,
			}
			groups[key] = group
		}
		group.sessions[event.SessionID] = struct{}{}
		if event.CreatedAt.After(group.latestAt) {
			group.latestAt = event.CreatedAt
			group.predicted = predicted
			group.corrected = corrected
		}
	}

	qualified := make([]*movementHintGroup, 0, len(groups))
	for _, group := range groups {
		if len(group.sessions) >= 2 {
			qualified = append(qualified, group)
		}
	}
	sort.SliceStable(qualified, func(i, j int) bool {
		if len(qualified[i].sessions) != len(qualified[j].sessions) {
			return len(qualified[i].sessions) > len(qualified[j].sessions)
		}
		if !qualified[i].latestAt.Equal(qualified[j].latestAt) {
			return qualified[i].latestAt.After(qualified[j].latestAt)
		}
		return qualified[i].predicted < qualified[j].predicted
	})
	if len(qualified) > personalHintResultLimit {
		qualified = qualified[:personalHintResultLimit]
	}

	result := make([]PersonalMovementHint, 0, len(qualified))
	for _, group := range qualified {
		result = append(result, PersonalMovementHint{
			PredictedMovement: group.predicted,
			CorrectedMovement: group.corrected,
			SessionCount:      len(group.sessions),
		})
	}
	return result
}

func normalizeMovementHintName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}
