package workoutprogress

import (
	"math"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/activity"
)

// MinIntervalDurationSecs is the shortest media span kept as its own timeline
// entry. No workout interval is shorter than one second; shorter fragments are
// segment-boundary artifacts and are attached to a neighbouring observation.
const MinIntervalDurationSecs = 1.0

// continuityToleranceSecs is the largest separation between two observations
// that is still treated as one continuous span.
const continuityToleranceSecs = 1.0

// countOnlyGapReasons mark missing counts inside an observed span; they never
// interrupt the span itself.
var countOnlyGapReasons = map[string]bool{"incomplete_cycle": true, "no_countable_evidence": true}

// NormalizeContinuity runs before Build on the assembled session timeline:
//  1. a sub-second interval is attached to the following touching interval
//     (the preceding one when it is last), never kept on its own;
//  2. a sub-second media gap outside every interval is absorbed the same way;
//  3. touching intervals with the same activity, canonical movement and load
//     are stitched into one interval.
//
// hardBoundaries are media seconds where the capture clock is discontinuous
// (recording pause). Nothing is attached or stitched across them. Inputs are
// not mutated. Capture-clock gaps are passed through unchanged.
func NormalizeContinuity(intervals []Interval, gaps []Gap, hardBoundaries []float64) ([]Interval, []Gap) {
	items := make([]Interval, 0, len(intervals))
	for _, in := range intervals {
		if activity.ValidInterval(in.Start, in.End) {
			items = append(items, cloneInterval(in))
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Start < items[j].Start })
	items = absorbShortIntervals(items, hardBoundaries)

	outGaps := make([]Gap, 0, len(gaps))
	for _, g := range gaps {
		outGaps = append(outGaps, g)
	}
	items, outGaps = absorbShortGaps(items, outGaps, hardBoundaries)
	return stitchIntervals(items, hardBoundaries), outGaps
}

func cloneInterval(in Interval) Interval {
	out := in
	out.ObservationRefs = append([]string(nil), in.ObservationRefs...)
	out.RepEvents = append([]activity.Observation(nil), in.RepEvents...)
	out.FormObservations = append([]Observation(nil), in.FormObservations...)
	out.ROMObservations = append([]Observation(nil), in.ROMObservations...)
	out.PaceObservations = append([]Observation(nil), in.PaceObservations...)
	if in.Load != nil {
		load := *in.Load
		out.Load = &load
	}
	if in.CaptureStart != nil {
		v := *in.CaptureStart
		out.CaptureStart = &v
	}
	if in.CaptureEnd != nil {
		v := *in.CaptureEnd
		out.CaptureEnd = &v
	}
	return out
}

func touches(end, start float64) bool {
	separation := start - end
	return separation >= -1e-6 && separation <= continuityToleranceSecs+1e-6
}

// crossesHardBoundary reports a capture discontinuity between end and start
// (inclusive, with tolerance so a pause exactly at a chunk edge counts).
func crossesHardBoundary(end, start float64, boundaries []float64) bool {
	for _, b := range boundaries {
		if b >= end-1e-3 && b <= start+1e-3 {
			return true
		}
	}
	return false
}

func sameMovement(a, b Interval) bool {
	if a.Activity != b.Activity {
		return false
	}
	if a.Activity == "rest" {
		return true
	}
	if a.Activity != "exercise" {
		return false
	}
	ma, mb := canonicalOrRaw(a.Movement), canonicalOrRaw(b.Movement)
	return ma != "" && strings.EqualFold(ma, mb)
}

func canonicalOrRaw(name string) string {
	if canonical := activity.CanonicalMovement(name); canonical != "" {
		return canonical
	}
	return strings.TrimSpace(name)
}

func sameLoad(a, b *Load) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Value == b.Value && a.Unit == b.Unit && a.Basis == b.Basis
}

// captureContinuous rejects a merge when both sides carry capture endpoints
// and the capture clock advanced more than the media clock between them.
func captureContinuous(a, b Interval) bool {
	if a.CaptureEnd == nil || b.CaptureStart == nil {
		return true
	}
	return (*b.CaptureStart-*a.CaptureEnd)-(b.Start-a.End) <= 0.01
}

// moveObservations transfers fragment evidence to the receiving interval when
// both describe the same activity/movement; otherwise the fragment's evidence
// is dropped (a sub-second fragment cannot stand as its own observation).
func moveObservations(from Interval, to *Interval) {
	if !sameMovement(from, *to) {
		return
	}
	to.ObservationRefs = append(to.ObservationRefs, from.ObservationRefs...)
	to.RepEvents = append(to.RepEvents, from.RepEvents...)
	to.FormObservations = append(to.FormObservations, from.FormObservations...)
	to.ROMObservations = append(to.ROMObservations, from.ROMObservations...)
	to.PaceObservations = append(to.PaceObservations, from.PaceObservations...)
	sort.SliceStable(to.RepEvents, func(i, j int) bool { return to.RepEvents[i].Start < to.RepEvents[j].Start })
}

func absorbShortIntervals(items []Interval, boundaries []float64) []Interval {
	for changed := true; changed; {
		changed = false
		for i := range items {
			in := items[i]
			if in.End-in.Start >= MinIntervalDurationSecs {
				continue
			}
			// Prefer the following interval; at the end of a run use the preceding one.
			if i+1 < len(items) && touches(in.End, items[i+1].Start) && !crossesHardBoundary(in.End, items[i+1].Start, boundaries) &&
				(i == 0 || items[i-1].End <= in.Start+1e-6) {
				next := &items[i+1]
				moveObservations(in, next)
				next.Start = in.Start
				if next.CaptureStart != nil && in.CaptureStart != nil {
					v := *in.CaptureStart
					next.CaptureStart = &v
				} else {
					next.CaptureStart, next.CaptureEnd = nil, nil
				}
			} else if i > 0 && touches(items[i-1].End, in.Start) && !crossesHardBoundary(items[i-1].End, in.Start, boundaries) {
				prev := &items[i-1]
				moveObservations(in, prev)
				prev.End = in.End
				if prev.CaptureEnd != nil && in.CaptureEnd != nil {
					v := *in.CaptureEnd
					prev.CaptureEnd = &v
				} else {
					prev.CaptureStart, prev.CaptureEnd = nil, nil
				}
			}
			items = append(items[:i], items[i+1:]...)
			changed = true
			break
		}
	}
	return items
}

func coveredByInterval(start, end float64, items []Interval) bool {
	for _, in := range items {
		if in.Start <= start+1e-6 && in.End >= end-1e-6 {
			return true
		}
	}
	return false
}

func absorbShortGaps(items []Interval, gaps []Gap, boundaries []float64) ([]Interval, []Gap) {
	sort.SliceStable(gaps, func(i, j int) bool { return gaps[i].Start < gaps[j].Start })
	// Gaps already inside an observed interval (e.g. a brief occlusion) are
	// evidence about that interval and stay; only uncovered slivers are absorbed.
	inside := make([]bool, len(gaps))
	for i, g := range gaps {
		inside[i] = coveredByInterval(g.Start, g.End, items)
	}
	out := make([]Gap, 0, len(gaps))
	for gi, g := range gaps {
		if g.Clock != "media" || g.End-g.Start >= MinIntervalDurationSecs || inside[gi] {
			out = append(out, g)
			continue
		}
		if coveredByInterval(g.Start, g.End, items) {
			// Covered by an interval extended for an earlier sliver.
			if countOnlyGapReasons[g.Reason] {
				out = append(out, g)
			}
			continue
		}
		absorbed := false
		// Attach to the interval that starts where the gap ends, else the one ending where it starts.
		for i := range items {
			if touches(g.End, items[i].Start) && items[i].Start >= g.End-1e-6 && !crossesHardBoundary(g.End, items[i].Start, boundaries) &&
				(i == 0 || items[i-1].End <= g.Start+1e-6) {
				items[i].Start = math.Min(items[i].Start, g.Start)
				items[i].CaptureStart, items[i].CaptureEnd = nil, nil
				absorbed = true
				break
			}
		}
		if !absorbed {
			for i := len(items) - 1; i >= 0; i-- {
				if touches(items[i].End, g.Start) && items[i].End <= g.Start+1e-6 && !crossesHardBoundary(items[i].End, g.Start, boundaries) &&
					(i+1 == len(items) || items[i+1].Start >= g.End-1e-6) {
					items[i].End = math.Max(items[i].End, g.End)
					items[i].CaptureStart, items[i].CaptureEnd = nil, nil
					absorbed = true
					break
				}
			}
		}
		if !absorbed {
			// Between two gaps: extend the following touching media gap, else the preceding one.
			for j := range gaps {
				other := &gaps[j]
				if other.Clock == "media" && other.End-other.Start >= MinIntervalDurationSecs && touches(g.End, other.Start) && other.Start >= g.End-1e-6 {
					other.Start = math.Min(other.Start, g.Start)
					absorbed = true
					break
				}
			}
		}
		if !absorbed {
			for j := len(out) - 1; j >= 0; j-- {
				if out[j].Clock == "media" && out[j].End-out[j].Start >= MinIntervalDurationSecs && touches(out[j].End, g.Start) {
					out[j].End = math.Max(out[j].End, g.End)
					break
				}
			}
		}
		// Count-only markers inside a now-extended interval remain as count evidence.
		if countOnlyGapReasons[g.Reason] && coveredByInterval(g.Start, g.End, items) {
			out = append(out, g)
		}
	}
	return items, out
}

func stitchIntervals(items []Interval, boundaries []float64) []Interval {
	if len(items) == 0 {
		return items
	}
	out := []Interval{items[0]}
	for _, next := range items[1:] {
		cur := &out[len(out)-1]
		if !sameMovement(*cur, next) || !sameLoad(cur.Load, next.Load) || !touches(cur.End, next.Start) ||
			crossesHardBoundary(cur.End, next.Start, boundaries) || !captureContinuous(*cur, next) {
			out = append(out, next)
			continue
		}
		cur.ObservationRefs = append(cur.ObservationRefs, next.ObservationRefs...)
		cur.RepEvents = joinTimedEvents(append(cur.RepEvents, next.RepEvents...))
		cur.FormObservations = append(cur.FormObservations, next.FormObservations...)
		cur.ROMObservations = append(cur.ROMObservations, next.ROMObservations...)
		cur.PaceObservations = append(cur.PaceObservations, next.PaceObservations...)
		cur.End = next.End
		if cur.CaptureStart != nil && next.CaptureEnd != nil {
			v := *next.CaptureEnd
			cur.CaptureEnd = &v
		} else {
			cur.CaptureStart, cur.CaptureEnd = nil, nil
		}
	}
	return out
}

// joinTimedEvents keeps repetition events in order (dropping exact duplicates)
// and joins touching seconds-unit observations of one movement into one span.
func joinTimedEvents(events []activity.Observation) []activity.Observation {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start < events[j].Start })
	out := make([]activity.Observation, 0, len(events))
	for _, e := range events {
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.Unit == "seconds" && e.Unit == "seconds" && last.Complete && e.Complete &&
				strings.EqualFold(canonicalOrRaw(last.Movement), canonicalOrRaw(e.Movement)) && touches(last.End, e.Start) {
				last.End = math.Max(last.End, e.End)
				continue
			}
			if last.Start == e.Start && last.End == e.End && last.Unit == e.Unit && strings.EqualFold(last.Movement, e.Movement) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}
