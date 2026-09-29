// Package activity counts visible movement cycles independently of coaching,
// workout targets, legacy rep estimates, and muscle-load calculations.
package activity

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/movement"
)

const Version = 1

type LocalizedText struct {
	KO string `json:"ko"`
	EN string `json:"en"`
}

type CaptureAssessment struct {
	State    string        `json:"state"`
	Issue    string        `json:"issue,omitempty"`
	Evidence string        `json:"evidence,omitempty"`
	Advice   LocalizedText `json:"advice"`
}

type Interval struct {
	Start  float64 `json:"start_secs"`
	End    float64 `json:"end_secs"`
	Reason string  `json:"reason"`
}

type Observation struct {
	Movement string  `json:"movement"`
	Unit     string  `json:"unit"` // reps or seconds
	Start    float64 `json:"start_secs"`
	End      float64 `json:"end_secs"`
	Complete bool    `json:"complete"`
	Evidence string  `json:"evidence"`
}

type Observations struct {
	DurationSecs  float64       `json:"duration_secs"`
	Version       int           `json:"version"`
	TargetState   string        `json:"target_state"`   // identified, ambiguous, not_visible, unknown
	ActivityState string        `json:"activity_state"` // exercise, rest, unknown
	Events        []Observation `json:"events"`
	Unassessed    []Interval    `json:"unassessed"`
}

func Unknown(start, end float64, reason string) Observations {
	return Observations{DurationSecs: end - start, Version: Version, TargetState: "unknown", ActivityState: "unknown", Events: []Observation{}, Unassessed: []Interval{{Start: start, End: end, Reason: reason}}}
}

func ValidInterval(start, end float64) bool {
	return !math.IsNaN(start) && !math.IsNaN(end) && !math.IsInf(start, 0) && !math.IsInf(end, 0) && start >= 0 && end > start
}

func CanonicalMovement(raw string) string {
	key := movement.NormalizeKey(raw)
	for _, name := range movement.All() {
		if movement.NormalizeKey(name) == key {
			return name
		}
	}
	return ""
}

// UnitForMovement is intentionally explicit for non-cycle activities. Unsupported
// names cannot quietly create a count. Distinct catalog variants stay distinct.
func UnitForMovement(name string) string {
	switch name {
	case "Farmer's Carry", "Handstand Walk", "Row", "Echo Bike", "Bike Erg", "Skierg", "Run", "Surfing", "Paddle", "Yoga":
		return "seconds"
	}
	for _, group := range movement.MovementGroups {
		if group.Category == "Yoga" {
			for _, candidate := range group.Movements {
				if candidate == name && name != "Sun Salutation" {
					return "seconds"
				}
			}
		}
	}
	return "reps"
}

// Decode rejects malformed/overlapping evidence; it never fills unknown counts.
// Times are chunk-local during capture and absolute media seconds during review.
func Decode(raw []byte, start, end float64) (Observations, error) {
	var doc Observations
	if !ValidInterval(start, end) || json.Unmarshal(raw, &doc) != nil || doc.Version != Version || doc.Events == nil || doc.Unassessed == nil {
		return Unknown(start, end, "invalid_observations"), errors.New("invalid movement observations")
	}
	doc.DurationSecs = end - start
	switch doc.TargetState {
	case "identified", "ambiguous", "not_visible", "unknown":
	default:
		return Unknown(start, end, "invalid_target"), errors.New("invalid target state")
	}
	switch doc.ActivityState {
	case "exercise", "rest", "unknown":
	default:
		return Unknown(start, end, "invalid_activity"), errors.New("invalid activity state")
	}
	if doc.TargetState != "identified" || doc.ActivityState == "unknown" {
		return Unknown(start, end, "unassessable_target_or_activity"), nil
	}
	if doc.ActivityState == "rest" && len(doc.Events) > 0 {
		return Unknown(start, end, "conflicting_activity"), errors.New("events during rest")
	}
	sort.Slice(doc.Events, func(i, j int) bool { return doc.Events[i].Start < doc.Events[j].Start })
	previousEnd := start
	accepted := make([]Observation, 0, len(doc.Events))
	for _, event := range doc.Events {
		if !ValidInterval(event.Start, event.End) || event.Start < start || event.End > end || event.Start < previousEnd || strings.TrimSpace(event.Evidence) == "" {
			return Unknown(start, end, "invalid_event"), errors.New("invalid or overlapping event")
		}
		previousEnd = event.End
		event.Movement = CanonicalMovement(event.Movement)
		if event.Movement == "" || event.Unit != UnitForMovement(event.Movement) {
			doc.Unassessed = append(doc.Unassessed, Interval{event.Start, event.End, "unsupported_movement_or_unit"})
			continue
		}
		if !event.Complete {
			doc.Unassessed = append(doc.Unassessed, Interval{event.Start, event.End, "incomplete_cycle"})
			continue
		}
		accepted = append(accepted, event)
	}
	doc.Events = accepted
	for _, gap := range doc.Unassessed {
		if !ValidInterval(gap.Start, gap.End) || gap.Start < start || gap.End > end || gap.Reason == "" {
			return Unknown(start, end, "invalid_gap"), errors.New("invalid unassessed interval")
		}
	}
	// A claimed event intersecting an unassessable interval is not countable.
	doc.Events = nil
	for _, event := range accepted {
		clear := true
		for _, gap := range doc.Unassessed {
			if event.Start < gap.End && event.End > gap.Start {
				clear = false
				break
			}
		}
		if clear {
			doc.Events = append(doc.Events, event)
		}
	}
	if doc.Events == nil {
		doc.Events = []Observation{}
	}
	if doc.ActivityState == "exercise" && len(doc.Events) == 0 && len(doc.Unassessed) == 0 {
		doc.Unassessed = append(doc.Unassessed, Interval{start, end, "no_countable_evidence"})
	}
	return doc, nil
}

// Own assigns a repeated cycle to the interval in which it completes: (start,end].
// Duration observations are clipped, so overlapping review context is never added.
func Own(doc Observations, start, end float64) Observations {
	result := doc
	result.Events = []Observation{}
	result.Unassessed = []Interval{}
	for _, event := range doc.Events {
		if event.Unit == "reps" {
			if event.End > start && event.End <= end {
				result.Events = append(result.Events, event)
			}
		} else {
			event.Start = math.Max(event.Start, start)
			event.End = math.Min(event.End, end)
			if event.End > event.Start {
				result.Events = append(result.Events, event)
			}
		}
	}
	for _, gap := range doc.Unassessed {
		gap.Start = math.Max(gap.Start, start)
		gap.End = math.Min(gap.End, end)
		if gap.End > gap.Start {
			result.Unassessed = append(result.Unassessed, gap)
		}
	}
	return result
}
