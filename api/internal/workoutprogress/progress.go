// Package workoutprogress assembles independent observations without assessment
// feedback. All times are original merged-media seconds unless a gap says otherwise.
package workoutprogress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/activity"
)

const Version = 1

type Load struct {
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Basis  string  `json:"basis"`
	Status string  `json:"status"`
}

type Observation struct {
	Start    float64 `json:"start_secs"`
	End      float64 `json:"end_secs"`
	Evidence string  `json:"evidence"`
}

type Interval struct {
	ID               string                 `json:"id"`
	Start            float64                `json:"start_secs"`
	End              float64                `json:"end_secs"`
	Activity         string                 `json:"activity"`
	Movement         string                 `json:"movement"`
	Load             *Load                  `json:"load"`
	ObservationRefs  []string               `json:"observation_refs"`
	RepEvents        []activity.Observation `json:"rep_events"`
	FormObservations []Observation          `json:"form_observations"`
	ROMObservations  []Observation          `json:"rom_observations"`
	PaceObservations []Observation          `json:"pace_observations"`
	CaptureStart     *float64               `json:"capture_start_secs,omitempty"`
	CaptureEnd       *float64               `json:"capture_end_secs,omitempty"`
}

type Gap struct {
	Start    float64 `json:"start_secs"`
	End      float64 `json:"end_secs"`
	Clock    string  `json:"clock"`
	Reason   string  `json:"reason"`
	Movement string  `json:"movement,omitempty"`
}

type Progress struct {
	Version       int        `json:"version"`
	Clock         string     `json:"clock"`
	MediaSourceID string     `json:"media_source_id,omitempty"`
	SourceVersion string     `json:"source_version"`
	Intervals     []Interval `json:"intervals"`
	Unassessed    []Gap      `json:"unassessed"`
}

func ValidLoad(load *Load) bool {
	return load == nil || (!math.IsNaN(load.Value) && !math.IsInf(load.Value, 0) && load.Value > 0 &&
		(load.Unit == "lb" || load.Unit == "kg") && (load.Basis == "total" || load.Basis == "per_implement"))
}

func filterObservations(in []Observation, start, end float64) []Observation {
	out := []Observation{}
	for _, o := range in {
		if activity.ValidInterval(o.Start, o.End) && o.Start >= start && o.End <= end && strings.TrimSpace(o.Evidence) != "" {
			out = append(out, o)
		}
	}
	return out
}

// Build validates cycles with activity's existing validator and completion
// ownership. It never creates a rest interval from absent coverage.
func Build(intervals []Interval, gaps []Gap, sourceID string) Progress {
	p := Progress{Version: Version, Clock: "media", MediaSourceID: sourceID, Intervals: []Interval{}, Unassessed: []Gap{}}
	// Copy before validation so final assessment/retry cannot mutate observation input.
	raw, _ := json.Marshal(intervals)
	var copied []Interval
	_ = json.Unmarshal(raw, &copied)
	sort.SliceStable(copied, func(i, j int) bool { return copied[i].Start < copied[j].Start })
	for _, g := range gaps {
		if activity.ValidInterval(g.Start, g.End) && strings.TrimSpace(g.Reason) != "" && (g.Clock == "media" || g.Clock == "capture") {
			p.Unassessed = append(p.Unassessed, g)
		}
	}
	previousEnd := -1.0
	ids := map[string]bool{}
	for _, in := range copied {
		if !activity.ValidInterval(in.Start, in.End) {
			continue
		}
		if in.Start < previousEnd {
			p.Unassessed = append(p.Unassessed, Gap{Start: in.Start, End: in.End, Clock: "media", Reason: "overlapping_observation_intervals", Movement: in.Movement})
			continue
		}
		previousEnd = in.End
		if in.ID == "" || ids[in.ID] {
			in.ID = fmt.Sprintf("interval-%d", len(p.Intervals)+1)
		}
		ids[in.ID] = true
		if canonical := activity.CanonicalMovement(in.Movement); canonical != "" {
			in.Movement = canonical
		}
		if !ValidLoad(in.Load) {
			in.Load = nil
		}
		if in.Load != nil && in.Load.Status != "confirmed" {
			in.Load.Status = "pending_confirmation"
		}
		if in.ObservationRefs == nil {
			in.ObservationRefs = []string{}
		}
		in.FormObservations = filterObservations(in.FormObservations, in.Start, in.End)
		in.ROMObservations = filterObservations(in.ROMObservations, in.Start, in.End)
		in.PaceObservations = filterObservations(in.PaceObservations, in.Start, in.End)
		if in.CaptureStart == nil || in.CaptureEnd == nil || !activity.ValidInterval(*in.CaptureStart, *in.CaptureEnd) {
			in.CaptureStart = nil
			in.CaptureEnd = nil
		}
		if in.RepEvents == nil {
			in.RepEvents = []activity.Observation{}
		}
		// A wrong-unit event must not become a complete cycle through activity's
		// legacy unsupported-name recovery path.
		validUnits := []activity.Observation{}
		for _, event := range in.RepEvents {
			canonical := activity.CanonicalMovement(event.Movement)
			if canonical != "" && event.Unit != activity.UnitForMovement(canonical) {
				if activity.ValidInterval(event.Start, event.End) && event.Start >= in.Start && event.End <= in.End {
					p.Unassessed = append(p.Unassessed, Gap{Start: event.Start, End: event.End, Clock: "media", Reason: "unit_mismatch", Movement: event.Movement})
				}
				continue
			}
			validUnits = append(validUnits, event)
		}
		in.RepEvents = validUnits
		if in.Activity != "exercise" && in.Activity != "rest" {
			in.Activity = "unknown"
		}
		windowStart := in.Start
		for _, e := range in.RepEvents {
			if e.Start >= 0 && e.Start < windowStart {
				windowStart = e.Start
			}
		}
		doc := activity.Observations{Version: activity.Version, TargetState: "identified", ActivityState: in.Activity, Events: in.RepEvents, Unassessed: []activity.Interval{}}
		// Unknown qualitative movements remain visible but never generate reps.
		data, _ := json.Marshal(doc)
		validated, err := activity.Decode(data, windowStart, in.End)
		if err != nil && in.Activity == "rest" {
			in.Activity = "unknown"
		}
		validated = activity.Own(validated, in.Start, in.End)
		in.RepEvents = validated.Events
		for _, g := range validated.Unassessed {
			p.Unassessed = append(p.Unassessed, Gap{Start: g.Start, End: g.End, Clock: "media", Reason: g.Reason, Movement: g.Movement})
		}
		p.Intervals = append(p.Intervals, in)
	}
	sort.SliceStable(p.Unassessed, func(i, j int) bool {
		if p.Unassessed[i].Clock == p.Unassessed[j].Clock {
			return p.Unassessed[i].Start < p.Unassessed[j].Start
		}
		return p.Unassessed[i].Clock < p.Unassessed[j].Clock
	})
	uniqueGaps := []Gap{}
	gapKeys := map[Gap]bool{}
	for _, gap := range p.Unassessed {
		if !gapKeys[gap] {
			uniqueGaps = append(uniqueGaps, gap)
			gapKeys[gap] = true
		}
	}
	p.Unassessed = uniqueGaps
	// A conflicting or unassessed media window cannot simultaneously claim reps.
	for i := range p.Intervals {
		accepted := []activity.Observation{}
		for _, e := range p.Intervals[i].RepEvents {
			clear := true
			for _, g := range p.Unassessed {
				if g.Clock == "media" && e.Start < g.End && e.End > g.Start {
					clear = false
					break
				}
			}
			if clear {
				accepted = append(accepted, e)
			}
		}
		p.Intervals[i].RepEvents = accepted
	}
	evidence, _ := json.Marshal(struct {
		SourceID  string
		Intervals []Interval
		Gaps      []Gap
	}{sourceID, p.Intervals, p.Unassessed})
	hash := sha256.Sum256(evidence)
	p.SourceVersion = hex.EncodeToString(hash[:])
	return p
}

func Decode(raw []byte) (Progress, error) {
	var p Progress
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p.Version != Version || p.Clock != "media" || strings.TrimSpace(p.SourceVersion) == "" || p.Intervals == nil || p.Unassessed == nil {
		return Progress{}, errors.New("invalid workout progress")
	}
	seen := map[string]bool{}
	lastEnd := -1.0
	for _, in := range p.Intervals {
		if !activity.ValidInterval(in.Start, in.End) || in.Start < lastEnd || in.ID == "" || seen[in.ID] || !ValidLoad(in.Load) || (in.Activity != "exercise" && in.Activity != "rest" && in.Activity != "unknown") {
			return Progress{}, errors.New("invalid workout interval")
		}
		lastEnd = in.End
		seen[in.ID] = true
		if in.RepEvents == nil || in.ObservationRefs == nil || in.FormObservations == nil || in.ROMObservations == nil || in.PaceObservations == nil {
			return Progress{}, errors.New("missing observation arrays")
		}
		lastEventEnd := -1.0
		for _, event := range in.RepEvents {
			if !event.Complete || !activity.ValidInterval(event.Start, event.End) || event.End <= in.Start || event.End > in.End || event.Start < lastEventEnd || strings.TrimSpace(event.Evidence) == "" || activity.CanonicalMovement(event.Movement) == "" || event.Unit != activity.UnitForMovement(activity.CanonicalMovement(event.Movement)) || in.Activity != "exercise" {
				return Progress{}, errors.New("invalid stored repetition evidence")
			}
			lastEventEnd = event.End
		}
		if in.Load != nil && in.Load.Status != "confirmed" && in.Load.Status != "pending_confirmation" {
			return Progress{}, errors.New("invalid load confirmation status")
		}
		if (in.CaptureStart != nil || in.CaptureEnd != nil) && (in.CaptureStart == nil || in.CaptureEnd == nil || !activity.ValidInterval(*in.CaptureStart, *in.CaptureEnd)) {
			return Progress{}, errors.New("invalid capture mapping")
		}
		if len(filterObservations(in.FormObservations, in.Start, in.End)) != len(in.FormObservations) || len(filterObservations(in.ROMObservations, in.Start, in.End)) != len(in.ROMObservations) || len(filterObservations(in.PaceObservations, in.Start, in.End)) != len(in.PaceObservations) {
			return Progress{}, errors.New("invalid direct observations")
		}
	}
	for _, gap := range p.Unassessed {
		if !activity.ValidInterval(gap.Start, gap.End) || gap.Reason == "" || (gap.Clock != "media" && gap.Clock != "capture") {
			return Progress{}, errors.New("invalid workout gap")
		}
	}
	return p, nil
}
