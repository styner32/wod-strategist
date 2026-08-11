package db

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

type MobilityRestriction struct {
	Joint        string    `json:"joint"`
	Side         string    `json:"side"`
	Observation  string    `json:"observation"`
	SessionCount int       `json:"session_count"`
	Movements    []string  `json:"movements"` // distinct examples, up to 3 each
	Evidence     []string  `json:"evidence"`  // distinct examples, up to 3 each
	LatestAt     time.Time `json:"latest_at"`
}

type mobilityItem struct {
	Joint       string  `json:"joint"`
	Side        string  `json:"side"`
	Observation string  `json:"observation"`
	Movement    string  `json:"movement"`
	Evidence    string  `json:"evidence"`
	Confidence  float64 `json:"confidence"`
	Assessable  bool    `json:"assessable"`
}

// BuildMobilityHistory queries the last 20 completed non-archived sessions for profileID
// that contain non-empty mobility observations, groups by (joint, observation), sorts
// by session count descending then recency, and caps at 5.
func BuildMobilityHistory(ctx context.Context, database *gorm.DB, profileID uint, excludeSessionID string) ([]MobilityRestriction, error) {
	if database == nil || profileID == 0 {
		return nil, nil
	}

	var results []AnalysisResult
	err := database.WithContext(ctx).
		Where("profile_id = ? AND session_id <> ? AND status = ? AND archived_at IS NULL AND mobility_observations != '' AND mobility_observations != '[]'",
			profileID, excludeSessionID, "COMPLETED").
		Order("created_at DESC").
		Limit(20).
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}

	type groupKey struct {
		joint       string
		observation string
	}

	type groupState struct {
		joint       string
		side        string
		observation string
		sessions    map[string]struct{}
		movements   []string
		evidence    []string
		latestAt    time.Time
	}

	groups := make(map[groupKey]*groupState)

	for _, r := range results {
		var obs []mobilityItem
		if jsonErr := json.Unmarshal([]byte(r.MobilityObservations), &obs); jsonErr != nil || len(obs) == 0 {
			continue
		}

		for _, item := range obs {
			joint := strings.TrimSpace(item.Joint)
			observation := strings.TrimSpace(item.Observation)
			if joint == "" || observation == "" {
				continue
			}

			key := groupKey{joint: strings.ToLower(joint), observation: strings.ToLower(observation)}
			state := groups[key]
			if state == nil {
				side := strings.TrimSpace(item.Side)
				if side == "" {
					side = "both"
				}
				state = &groupState{
					joint:       joint,
					side:        side,
					observation: observation,
					sessions:    make(map[string]struct{}),
					latestAt:    r.CreatedAt,
				}
				groups[key] = state
			}

			state.sessions[r.SessionID] = struct{}{}
			if r.CreatedAt.After(state.latestAt) {
				state.latestAt = r.CreatedAt
				if item.Side != "" {
					state.side = item.Side
				}
			}

			if item.Movement != "" && len(state.movements) < 3 && !containsStr(state.movements, item.Movement) {
				state.movements = append(state.movements, item.Movement)
			}
			if item.Evidence != "" && len(state.evidence) < 3 && !containsStr(state.evidence, item.Evidence) {
				state.evidence = append(state.evidence, item.Evidence)
			}
		}
	}

	list := make([]MobilityRestriction, 0, len(groups))
	for _, g := range groups {
		list = append(list, MobilityRestriction{
			Joint:        g.joint,
			Side:         g.side,
			Observation:  g.observation,
			SessionCount: len(g.sessions),
			Movements:    g.movements,
			Evidence:     g.evidence,
			LatestAt:     g.latestAt,
		})
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].SessionCount != list[j].SessionCount {
			return list[i].SessionCount > list[j].SessionCount
		}
		return list[i].LatestAt.After(list[j].LatestAt)
	})

	if len(list) > 5 {
		list = list[:5]
	}

	return list, nil
}

func containsStr(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
