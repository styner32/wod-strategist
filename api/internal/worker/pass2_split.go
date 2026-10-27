package worker

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

const (
	// maxPass2WindowSecs bounds one Pass 2 call. A continuous bout up to this
	// length is observed in a single call; longer bouts (e.g. an AMRAP without
	// rest) are split only for call limits and stitched back afterwards.
	maxPass2WindowSecs = 150.0
	// pass2OverlapSecs is context added on both sides of an internal split so a
	// cycle crossing the split is fully visible in one window.
	pass2OverlapSecs = 5.0
	// candidateMovementsPrefix marks the bout's chunk movement candidates in
	// Segment.Description (set by buildContinuousSegments).
	candidateMovementsPrefix = "청크 후보 종목: "
)

// pass2Window is one Pass 2 call. Seg carries the clip bounds sent to the
// observer; observations are owned only inside [CoreStart, CoreEnd].
type pass2Window struct {
	Seg       Segment
	CoreStart float64
	CoreEnd   float64
	Split     bool
}

func (w pass2Window) core() Segment {
	return Segment{Start: formatSegmentTimestamp(w.CoreStart), End: formatSegmentTimestamp(w.CoreEnd), Type: w.Seg.Type, Description: w.Seg.Description}
}

func splitPass2Windows(segments []Segment) []pass2Window {
	out := make([]pass2Window, 0, len(segments))
	for _, seg := range segments {
		start, end := convertToSeconds(seg.Start).Seconds(), convertToSeconds(seg.End).Seconds()
		duration := end - start
		if duration <= maxPass2WindowSecs+1e-6 {
			out = append(out, pass2Window{Seg: seg, CoreStart: start, CoreEnd: end})
			continue
		}
		n := int(math.Ceil(duration / maxPass2WindowSecs))
		step := duration / float64(n)
		for i := 0; i < n; i++ {
			coreStart := start + float64(i)*step
			coreEnd := start + float64(i+1)*step
			if i == n-1 {
				coreEnd = end
			}
			clip := seg
			clip.Start = formatSegmentTimestamp(math.Max(start, coreStart-pass2OverlapSecs))
			clip.End = formatSegmentTimestamp(math.Min(end, coreEnd+pass2OverlapSecs))
			out = append(out, pass2Window{Seg: clip, CoreStart: coreStart, CoreEnd: coreEnd, Split: true})
		}
	}
	return out
}

// ownWindowObservations keeps what the window owns: a repetition belongs to the
// window in which it completes, timed events and observations are clipped to
// the core. Overlapping context is never counted twice. A window that is not
// split is returned unchanged.
func ownWindowObservations(intervals []workoutprogress.Interval, gaps []workoutprogress.Gap, w pass2Window) ([]workoutprogress.Interval, []workoutprogress.Gap) {
	if !w.Split {
		return intervals, gaps
	}
	lo, hi := w.CoreStart, w.CoreEnd
	clipObs := func(items []workoutprogress.Observation) []workoutprogress.Observation {
		out := []workoutprogress.Observation{}
		for _, o := range items {
			s, e := math.Max(o.Start, lo), math.Min(o.End, hi)
			if e > s {
				o.Start, o.End = s, e
				out = append(out, o)
			}
		}
		return out
	}
	outIntervals := make([]workoutprogress.Interval, 0, len(intervals))
	for _, in := range intervals {
		s, e := math.Max(in.Start, lo), math.Min(in.End, hi)
		if e <= s {
			continue
		}
		in.Start, in.End = s, e
		events := make([]activity.Observation, 0, len(in.RepEvents))
		for _, ev := range in.RepEvents {
			if ev.Unit == "reps" {
				// Completion ownership (lo, hi]; the cycle may have started in the
				// leading context, which Build accepts for owned repetitions.
				if ev.End > lo && ev.End <= hi {
					events = append(events, ev)
				}
				continue
			}
			es, ee := math.Max(ev.Start, lo), math.Min(ev.End, hi)
			if ee > es {
				ev.Start, ev.End = es, ee
				events = append(events, ev)
			}
		}
		in.RepEvents = events
		in.FormObservations = clipObs(in.FormObservations)
		in.ROMObservations = clipObs(in.ROMObservations)
		in.PaceObservations = clipObs(in.PaceObservations)
		outIntervals = append(outIntervals, in)
	}
	outGaps := make([]workoutprogress.Gap, 0, len(gaps))
	for _, g := range gaps {
		if g.Clock != "media" {
			outGaps = append(outGaps, g)
			continue
		}
		s, e := math.Max(g.Start, lo), math.Min(g.End, hi)
		if e > s {
			g.Start, g.End = s, e
			outGaps = append(outGaps, g)
		}
	}
	return outIntervals, outGaps
}

// segmentCandidateMovements returns the bout's chunk movement candidates.
// Only this label list is read from Description; chunk coaching text is an
// assessment output and is never given to the observer.
func segmentCandidateMovements(seg Segment) []string {
	if !strings.HasPrefix(seg.Description, candidateMovementsPrefix) {
		return nil
	}
	list := strings.TrimPrefix(seg.Description, candidateMovementsPrefix)
	if cut := strings.Index(list, " | "); cut >= 0 {
		list = list[:cut]
	}
	out := []string{}
	for _, item := range strings.Split(list, ",") {
		if m := strings.TrimSpace(item); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// decodeWODEvidenceList accepts one or more wod_evidence blocks, each an
// object or an array, so one continuous bout can carry several movements.
// The bool is false when no valid evidence exists.
func decodeWODEvidenceList(text string, seg Segment) ([]wodSegmentEvidence, bool) {
	const fence = "```wod_evidence"
	lo, hi := convertToSeconds(seg.Start).Seconds(), convertToSeconds(seg.End).Seconds()
	valid := []wodSegmentEvidence{}
	rest := text
	for {
		start := strings.Index(rest, fence)
		if start < 0 {
			break
		}
		rest = rest[start+len(fence):]
		end := strings.Index(rest, "```")
		if end < 0 {
			break
		}
		body := strings.TrimSpace(rest[:end])
		rest = rest[end+3:]
		var items []wodSegmentEvidence
		if strings.HasPrefix(body, "[") {
			if json.Unmarshal([]byte(body), &items) != nil {
				continue
			}
		} else {
			var one wodSegmentEvidence
			if json.Unmarshal([]byte(body), &one) != nil {
				continue
			}
			items = []wodSegmentEvidence{one}
		}
		for _, e := range items {
			if e.TargetConfirmed && !isNonExerciseMovement(e.Movement) && e.StartSecs >= lo && e.EndSecs <= hi && e.EndSecs > e.StartSecs &&
				!math.IsNaN(e.EndSecs) && !math.IsInf(e.EndSecs, 0) {
				valid = append(valid, e)
			}
		}
	}
	return valid, len(valid) > 0
}

// clipWODEvidence keeps evidence spans inside the window core.
func clipWODEvidence(items []wodSegmentEvidence, w pass2Window) []wodSegmentEvidence {
	if !w.Split {
		return items
	}
	out := []wodSegmentEvidence{}
	for _, e := range items {
		s, en := math.Max(e.StartSecs, w.CoreStart), math.Min(e.EndSecs, w.CoreEnd)
		if en > s {
			e.StartSecs, e.EndSecs = s, en
			out = append(out, e)
		}
	}
	return out
}

func sortWODEvidence(items []wodSegmentEvidence) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].StartSecs < items[j].StartSecs })
}
