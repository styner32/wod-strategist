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
	// pass2RestSpilloverSecs is bounded context added into an immediately
	// touching continuous skipped_non_exercise span (never across a recording
	// pause) so a repetition starting or finishing across a chunk/rest boundary
	// is visible to Pass 2 without padding core bout bounds.
	pass2RestSpilloverSecs = 5.0
	// candidateMovementsPrefix marks the bout's chunk movement candidates in
	// Segment.Description (set by buildContinuousSegments).
	candidateMovementsPrefix = "청크 후보 종목: "
	// minPass2NaturalSubwindowSecs prevents natural-boundary splits from creating
	// tiny sub-windows while keeping every split window <= maxPass2WindowSecs.
	minPass2NaturalSubwindowSecs = 30.0
)

// pass2Window is one Pass 2 call. Seg carries the clip bounds sent to the
// observer; observations are owned inside [CoreStart, CoreEnd], plus verified
// complete repetitions crossing into disjoint rest-boundary spillover context.
type pass2Window struct {
	Seg           Segment
	CoreStart     float64
	CoreEnd       float64
	Split         bool
	SplitLeft     bool
	SplitRight    bool
	LeadRestSecs  float64
	TrailRestSecs float64
}

func (w pass2Window) core() Segment {
	return Segment{Start: formatSegmentTimestamp(w.CoreStart), End: formatSegmentTimestamp(w.CoreEnd), Type: w.Seg.Type, Description: w.Seg.Description}
}

func splitPass2Windows(segments []Segment) []pass2Window {
	return splitPass2WindowsWithRestContext(segments, nil, nil, 0)
}

func hasHardBoundaryNear(point float64, hardBoundaries []float64, tol float64) bool {
	for _, b := range hardBoundaries {
		if math.Abs(b-point) <= tol {
			return true
		}
	}
	return false
}

func continuousSkippedSpans(gaps []workoutprogress.Gap, hardBoundaries []float64) [][2]float64 {
	raw := make([][2]float64, 0, len(gaps))
	for _, g := range gaps {
		if g.Clock != "media" || g.Reason != skippedNonExerciseReason || !activity.ValidInterval(g.Start, g.End) {
			continue
		}
		pieces := [][2]float64{{g.Start, g.End}}
		for _, hb := range hardBoundaries {
			next := make([][2]float64, 0, len(pieces)+1)
			for _, p := range pieces {
				if hb > p[0]+0.01 && hb < p[1]-0.01 {
					next = append(next, [2]float64{p[0], hb}, [2]float64{hb, p[1]})
				} else {
					next = append(next, p)
				}
			}
			pieces = next
		}
		raw = append(raw, pieces...)
	}
	if len(raw) == 0 {
		return nil
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i][0] < raw[j][0] })
	merged := [][2]float64{raw[0]}
	for _, s := range raw[1:] {
		last := &merged[len(merged)-1]
		if math.Abs(s[0]-last[1]) <= 0.05 && !hasHardBoundaryNear(last[1], hardBoundaries, 0.05) {
			if s[1] > last[1] {
				last[1] = s[1]
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

type naturalSplitCandidate struct {
	at       float64
	priority int // 1 = movement transition, 2 = bridged rest boundary
}

func collectNaturalSplitCandidates(smoothed []timelineChunk, segStart, segEnd float64) []naturalSplitCandidate {
	if len(smoothed) == 0 {
		return nil
	}
	var cands []naturalSplitCandidate
	add := func(at float64, prio int) {
		if at <= segStart+1e-3 || at >= segEnd-1e-3 {
			return
		}
		for i := range cands {
			if math.Abs(cands[i].at-at) <= 0.05 {
				if prio < cands[i].priority {
					cands[i].priority = prio
				}
				return
			}
		}
		cands = append(cands, naturalSplitCandidate{at: at, priority: prio})
	}
	for i := range smoothed {
		cur := smoothed[i]
		if cur.End <= segStart+0.05 || cur.Start >= segEnd-0.05 || cur.Class != chunkActive {
			continue
		}
		if cur.BridgedRest {
			prevMov, nextMov := "", ""
			if i > 0 && smoothed[i-1].Class == chunkActive && !smoothed[i-1].BridgedRest {
				prevMov = smoothed[i-1].Movement
			}
			if i+1 < len(smoothed) && smoothed[i+1].Class == chunkActive && !smoothed[i+1].BridgedRest {
				nextMov = smoothed[i+1].Movement
			}
			if isConcreteMovement(prevMov) && isConcreteMovement(nextMov) && !sameMovementLabel(prevMov, nextMov) {
				add(cur.End, 1)
				add(cur.Start, 1)
			} else {
				add(cur.End, 2)
				add(cur.Start, 2)
			}
			continue
		}
		if i+1 < len(smoothed) {
			next := smoothed[i+1]
			if next.Class == chunkActive && !next.BridgedRest &&
				isConcreteMovement(cur.Movement) && isConcreteMovement(next.Movement) &&
				!sameMovementLabel(cur.Movement, next.Movement) &&
				timelineContinuous(cur, next) {
				add(cur.End, 1)
			}
		}
	}
	return cands
}

func choosePass2SubwindowBounds(start, end float64, n int, smoothed []timelineChunk) []float64 {
	bounds := make([]float64, 0, n+1)
	bounds = append(bounds, start)
	if n <= 1 {
		return append(bounds, end)
	}
	cands := collectNaturalSplitCandidates(smoothed, start, end)
	curStart := start
	for k := 0; k < n-1; k++ {
		remAfter := n - 1 - k
		ideal := curStart + (end-curStart)/float64(remAfter+1)
		lo := math.Max(curStart+minPass2NaturalSubwindowSecs, end-float64(remAfter)*maxPass2WindowSecs)
		hi := math.Min(curStart+maxPass2WindowSecs, end-float64(remAfter)*minPass2NaturalSubwindowSecs)
		chosen := start + float64(k+1)*(end-start)/float64(n)
		if lo <= hi+1e-6 {
			chosen = math.Min(math.Max(ideal, lo), hi)
			bestPrio := 99
			bestDist := math.Inf(1)
			for _, c := range cands {
				if c.at < lo-1e-6 || c.at > hi+1e-6 {
					continue
				}
				dist := math.Abs(c.at - ideal)
				if c.priority < bestPrio || (c.priority == bestPrio && (dist < bestDist-1e-6 || (math.Abs(dist-bestDist) <= 1e-6 && c.at > chosen))) {
					bestPrio = c.priority
					bestDist = dist
					chosen = c.at
				}
			}
		}
		bounds = append(bounds, chosen)
		curStart = chosen
	}
	bounds = append(bounds, end)
	return bounds
}

func subwindowSegmentFromChunks(baseSeg Segment, coreStart, coreEnd float64, smoothed []timelineChunk) Segment {
	out := baseSeg
	if len(smoothed) == 0 {
		return out
	}
	counts := map[string]int{}
	order := []string{}
	var candidates []string
	var descriptions []string
	matched := 0
	for _, c := range smoothed {
		if c.Class != chunkActive || c.End <= coreStart+0.05 || c.Start >= coreEnd-0.05 {
			continue
		}
		matched++
		if !c.BridgedRest && isConcreteMovement(c.Movement) {
			key := strings.ToLower(activity.CanonicalMovement(c.Movement))
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(c.Movement))
			}
			if counts[key] == 0 {
				order = append(order, c.Movement)
			}
			counts[key]++
			candidates = addCandidate(candidates, c.Movement)
		}
		if !c.BridgedRest {
			for _, m := range c.Candidates {
				candidates = addCandidate(candidates, m)
			}
		}
		if c.Description != "" {
			descriptions = append(descriptions, c.Description)
		}
	}
	if matched == 0 {
		return out
	}
	movement, best := "Unknown", 0
	for _, m := range order {
		key := strings.ToLower(activity.CanonicalMovement(m))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(m))
		}
		if counts[key] > best {
			movement, best = m, counts[key]
		}
	}
	if best == 0 && isConcreteMovement(baseSeg.Type) {
		movement = baseSeg.Type
	}
	desc := strings.Join(descriptions, " | ")
	if len(candidates) > 1 {
		desc = candidateMovementsPrefix + strings.Join(candidates, ", ") + " | " + desc
	}
	out.Type = movement
	out.Description = desc
	return out
}

func splitPass2WindowsWithRestContext(segments []Segment, gaps []workoutprogress.Gap, hardBoundaries []float64, minClipStart float64) []pass2Window {
	return splitPass2WindowsWithNaturalBoundaries(segments, gaps, hardBoundaries, minClipStart, nil)
}

func splitPass2WindowsWithNaturalBoundaries(
	segments []Segment,
	gaps []workoutprogress.Gap,
	hardBoundaries []float64,
	minClipStart float64,
	timeline []timelineChunk,
) []pass2Window {
	smoothed := prepareSmoothedTimelineChunks(timeline)
	skipped := continuousSkippedSpans(gaps, hardBoundaries)
	out := make([]pass2Window, 0, len(segments))
	for bIdx, seg := range segments {
		start, end := convertToSeconds(seg.Start).Seconds(), convertToSeconds(seg.End).Seconds()
		duration := end - start
		leadSecs, trailSecs := 0.0, 0.0
		if !hasHardBoundaryNear(start, hardBoundaries, 0.05) {
			for _, sp := range skipped {
				if math.Abs(sp[1]-start) <= 0.05 {
					effStart := math.Max(sp[0], minClipStart)
					if effStart < start {
						limit := start - effStart
						if bIdx > 0 {
							prevEnd := convertToSeconds(segments[bIdx-1].End).Seconds()
							if math.Abs(prevEnd-sp[0]) <= 0.05 && !hasHardBoundaryNear(sp[0], hardBoundaries, 0.05) {
								mid := math.Max((sp[0]+sp[1])/2, effStart)
								limit = math.Max(0, start-mid)
							}
						}
						leadSecs = math.Min(pass2RestSpilloverSecs, limit)
					}
					break
				}
			}
		}
		if !hasHardBoundaryNear(end, hardBoundaries, 0.05) && end >= minClipStart-1e-6 {
			for _, sp := range skipped {
				if math.Abs(sp[0]-end) <= 0.05 {
					limit := math.Max(0, sp[1]-end)
					if bIdx+1 < len(segments) {
						nextStart := convertToSeconds(segments[bIdx+1].Start).Seconds()
						if math.Abs(sp[1]-nextStart) <= 0.05 && !hasHardBoundaryNear(sp[1], hardBoundaries, 0.05) {
							mid := (sp[0] + sp[1]) / 2
							limit = math.Max(0, mid-end)
						}
					}
					trailSecs = math.Min(pass2RestSpilloverSecs, limit)
					break
				}
			}
		}

		if duration <= maxPass2WindowSecs+1e-6 {
			clip := seg
			if leadSecs > 0 {
				clip.Start = formatSegmentTimestamp(start - leadSecs)
			}
			if trailSecs > 0 {
				clip.End = formatSegmentTimestamp(end + trailSecs)
			}
			out = append(out, pass2Window{
				Seg:           clip,
				CoreStart:     start,
				CoreEnd:       end,
				LeadRestSecs:  math.Max(0, start-convertToSeconds(clip.Start).Seconds()),
				TrailRestSecs: math.Max(0, convertToSeconds(clip.End).Seconds()-end),
			})
			continue
		}
		n := int(math.Ceil(duration / maxPass2WindowSecs))
		bounds := choosePass2SubwindowBounds(start, end, n, smoothed)
		for i := 0; i < n; i++ {
			coreStart := bounds[i]
			coreEnd := bounds[i+1]
			clip := subwindowSegmentFromChunks(seg, coreStart, coreEnd, smoothed)
			splitLeft := i > 0
			splitRight := i < n-1
			winLead, winTrail := 0.0, 0.0
			if splitLeft {
				clip.Start = formatSegmentTimestamp(math.Max(start, coreStart-pass2OverlapSecs))
			} else if leadSecs > 0 {
				clip.Start = formatSegmentTimestamp(coreStart - leadSecs)
				winLead = math.Max(0, coreStart-convertToSeconds(clip.Start).Seconds())
			} else {
				clip.Start = formatSegmentTimestamp(coreStart)
			}
			if splitRight {
				clip.End = formatSegmentTimestamp(math.Min(end, coreEnd+pass2OverlapSecs))
			} else if trailSecs > 0 {
				clip.End = formatSegmentTimestamp(coreEnd + trailSecs)
				winTrail = math.Max(0, convertToSeconds(clip.End).Seconds()-coreEnd)
			} else {
				clip.End = formatSegmentTimestamp(coreEnd)
			}
			out = append(out, pass2Window{
				Seg:           clip,
				CoreStart:     coreStart,
				CoreEnd:       coreEnd,
				Split:         true,
				SplitLeft:     splitLeft,
				SplitRight:    splitRight,
				LeadRestSecs:  winLead,
				TrailRestSecs: winTrail,
			})
		}
	}
	return out
}

// ownWindowObservations keeps what the window owns: a repetition belongs to the
// window in which it completes, timed events and observations are clipped to
// the core on internal split edges, and complete repetitions crossing into
// disjoint rest-boundary spillover extend only to the verified event bounds.
func ownWindowObservations(intervals []workoutprogress.Interval, gaps []workoutprogress.Gap, w pass2Window) ([]workoutprogress.Interval, []workoutprogress.Gap) {
	if !w.Split && w.LeadRestSecs == 0 && w.TrailRestSecs == 0 {
		return intervals, gaps
	}
	clipObs := func(items []workoutprogress.Observation, lo, hi float64) []workoutprogress.Observation {
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
	minLead := w.CoreStart
	if !w.SplitLeft && w.LeadRestSecs > 0 {
		minLead = w.CoreStart - w.LeadRestSecs
	}
	maxTrail := w.CoreEnd
	if !w.SplitRight && w.TrailRestSecs > 0 {
		maxTrail = w.CoreEnd + w.TrailRestSecs
	}
	outIntervals := make([]workoutprogress.Interval, 0, len(intervals))
	for _, in := range intervals {
		lo, hi := w.CoreStart, w.CoreEnd
		events := make([]activity.Observation, 0, len(in.RepEvents))
		if in.Activity == "exercise" {
			for _, ev := range in.RepEvents {
				if ev.Unit == "reps" {
					ownedLeft := ev.End > w.CoreStart
					if !w.SplitLeft && w.LeadRestSecs > 0 {
						ownedLeft = ev.End > minLead
					}
					ownedRight := ev.End <= w.CoreEnd
					if !w.SplitRight && w.TrailRestSecs > 0 && ev.Complete {
						ownedRight = ev.End <= maxTrail
					}
					if ownedLeft && ownedRight {
						events = append(events, ev)
						if !w.SplitLeft && w.LeadRestSecs > 0 && ev.Complete && ev.Start < lo {
							lo = math.Max(minLead, ev.Start)
						}
						if !w.SplitRight && w.TrailRestSecs > 0 && ev.Complete && ev.End > hi {
							hi = math.Min(maxTrail, ev.End)
						}
					}
					continue
				}
				es, ee := math.Max(ev.Start, w.CoreStart), math.Min(ev.End, w.CoreEnd)
				if ee > es {
					ev.Start, ev.End = es, ee
					events = append(events, ev)
				}
			}
		}
		s, e := math.Max(in.Start, lo), math.Min(in.End, hi)
		if e <= s {
			continue
		}
		in.Start, in.End = s, e
		in.RepEvents = events
		in.FormObservations = clipObs(in.FormObservations, s, e)
		in.ROMObservations = clipObs(in.ROMObservations, s, e)
		in.PaceObservations = clipObs(in.PaceObservations, s, e)
		outIntervals = append(outIntervals, in)
	}
	outGaps := make([]workoutprogress.Gap, 0, len(gaps))
	for _, g := range gaps {
		if g.Clock != "media" {
			outGaps = append(outGaps, g)
			continue
		}
		s, e := math.Max(g.Start, w.CoreStart), math.Min(g.End, w.CoreEnd)
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

// clipWODEvidence keeps evidence spans inside the window's owned range.
func clipWODEvidence(items []wodSegmentEvidence, w pass2Window) []wodSegmentEvidence {
	if !w.Split && w.LeadRestSecs == 0 && w.TrailRestSecs == 0 {
		return items
	}
	lo, hi := w.CoreStart-w.LeadRestSecs, w.CoreEnd+w.TrailRestSecs
	if w.SplitLeft {
		lo = w.CoreStart
	}
	if w.SplitRight {
		hi = w.CoreEnd
	}
	out := []wodSegmentEvidence{}
	for _, e := range items {
		s, en := math.Max(e.StartSecs, lo), math.Min(e.EndSecs, hi)
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
