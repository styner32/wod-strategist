package worker

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
)

// Pass 1 reads the chunk timeline as one continuous sequence of the same
// person instead of judging each 10-second chunk in isolation. Movement
// assessment stays independent; only which media Pass 2 observes, and how it is
// grouped, follows the person's continuous activity.

type chunkActivityClass int

const (
	// chunkUnclassified carries no usable activity evidence (e.g. an empty
	// response). It is neither exercise nor rest.
	chunkUnclassified chunkActivityClass = iota
	chunkActive
	chunkExplicitRest
)

const (
	// timelineTouchToleranceSecs treats adjacent verified media intervals as continuous.
	timelineTouchToleranceSecs = 0.05
	// labelChangeMinCompleteCycles is the evidence a single chunk needs to keep
	// a movement label different from identical neighbours.
	labelChangeMinCompleteCycles = 2
)

type timelineChunk struct {
	Start, End               float64 // verified merged-media seconds
	CaptureStart, CaptureEnd *float64
	Class                    chunkActivityClass
	Movement                 string // label hint for Pass 2; "Unknown" when unclear
	Candidates               []string
	completeCycles           map[string]int
	UserFixed                bool // a saved correction decided the class; never smoothed
	Description              string
}

type chunkMovementEvidence struct {
	activityState  string
	targetState    string
	hasDoc         bool
	anyEvents      bool
	hasUnassessed  bool
	completeCycles map[string]int
	topMovement    string
}

func readChunkMovementEvidence(chunk db.ChunkAnalysisResult) chunkMovementEvidence {
	out := chunkMovementEvidence{completeCycles: map[string]int{}}
	if len(chunk.MovementObservations) == 0 {
		return out
	}
	var doc activity.Observations
	if json.Unmarshal(chunk.MovementObservations, &doc) != nil || doc.Version != activity.Version {
		return out
	}
	out.hasDoc = true
	out.activityState = strings.ToLower(strings.TrimSpace(doc.ActivityState))
	out.targetState = strings.ToLower(strings.TrimSpace(doc.TargetState))
	best := 0
	for _, e := range doc.Events {
		out.anyEvents = true
		name := activity.CanonicalMovement(e.Movement)
		if name == "" {
			name = strings.TrimSpace(e.Movement)
		}
		if name == "" || isNonExerciseMovement(name) {
			continue
		}
		if e.Complete {
			out.completeCycles[name]++
		}
		if out.completeCycles[name] > best {
			best, out.topMovement = out.completeCycles[name], name
		} else if out.topMovement == "" {
			out.topMovement = name
		}
	}
	for _, gap := range doc.Unassessed {
		reason := strings.ToLower(strings.TrimSpace(gap.Reason))
		if reason != "target_not_visible" {
			out.hasUnassessed = true
		}
		if reason == "incomplete_cycle" {
			out.anyEvents = true
		}
		if out.topMovement == "" && strings.TrimSpace(gap.Movement) != "" && !isNonExerciseMovement(gap.Movement) {
			name := activity.CanonicalMovement(gap.Movement)
			if name == "" {
				name = strings.TrimSpace(gap.Movement)
			}
			out.topMovement = name
		}
	}
	return out
}

// classifyChunkActivity combines the tag, observed signals and movement
// observations. Anything that is not explicit rest but shows activity is
// observed (as Unknown when unlabelled); only explicit walking/rest/setup is rest.
func classifyChunkActivity(chunk db.ChunkAnalysisResult, workoutType string) (chunkActivityClass, string) {
	movement := strings.TrimSpace(chunk.ExerciseType)
	var signals struct {
		Movement      string `json:"movement"`
		ActivityState string `json:"activity_state"`
	}
	_ = json.Unmarshal([]byte(chunk.ObservedSignals), &signals)
	evidence := readChunkMovementEvidence(chunk)

	if IsRecoveryWorkoutType(workoutType) {
		// Recovery keeps rest/setup/unknown (holds are often labelled rest); only walking is skipped.
		if isWalkingActivity(movement) || isWalkingActivity(signals.ActivityState) {
			return chunkExplicitRest, ""
		}
		if movement == "" {
			movement = "Mobility"
		}
		return chunkActive, movement
	}

	label := func() string {
		if movement != "" && !isNonExerciseMovement(movement) {
			return movement
		}
		if evidence.topMovement != "" {
			return evidence.topMovement
		}
		if m := strings.TrimSpace(signals.Movement); m != "" && !isNonExerciseMovement(m) {
			return m
		}
		return "Unknown"
	}

	// Visible cycles are exercise regardless of a missing or contradictory tag.
	if evidence.anyEvents {
		return chunkActive, label()
	}
	if isExplicitNonExerciseActivity(signals.ActivityState) ||
		(movement != "" && isNonExerciseMovement(movement) && !strings.EqualFold(movement, "unknown")) ||
		(evidence.hasDoc && evidence.targetState == "identified" && evidence.activityState == "rest" && !evidence.hasUnassessed) {
		return chunkExplicitRest, ""
	}
	if movement != "" {
		// A named movement or Unknown.
		return chunkActive, label()
	}
	if strings.EqualFold(signals.ActivityState, "exercise") ||
		(strings.EqualFold(signals.ActivityState, "unknown") && !(evidence.hasDoc && evidence.targetState == "not_visible")) ||
		evidence.activityState == "exercise" ||
		(evidence.hasDoc && evidence.targetState == "identified" && evidence.hasUnassessed) ||
		(evidence.hasDoc && evidence.activityState == "unknown" && evidence.targetState != "not_visible" && evidence.targetState != "") {
		return chunkActive, label()
	}
	return chunkUnclassified, ""
}

func newTimelineChunk(chunk db.ChunkAnalysisResult, workoutType string) timelineChunk {
	class, movement := classifyChunkActivity(chunk, workoutType)
	evidence := readChunkMovementEvidence(chunk)
	desc := chunk.Output
	if len(desc) > 200 {
		desc = desc[:200] + "..."
	}
	tc := timelineChunk{
		Start: *chunk.MediaStartSecs, End: *chunk.MediaEndSecs,
		CaptureStart: chunk.StartSecs, CaptureEnd: chunk.EndSecs,
		Class: class, Movement: movement, completeCycles: evidence.completeCycles,
		Description: desc,
	}
	if class == chunkActive && movement != "" {
		tc.Candidates = []string{movement}
	}
	return tc
}

// timelineContinuous reports touching media intervals without a recording pause.
func timelineContinuous(a, b timelineChunk) bool {
	if math.Abs(b.Start-a.End) > timelineTouchToleranceSecs {
		return false
	}
	if a.CaptureEnd != nil && b.CaptureStart != nil {
		return (*b.CaptureStart-*a.CaptureEnd)-(b.Start-a.End) <= 0.01
	}
	return true
}

func isConcreteMovement(m string) bool {
	return strings.TrimSpace(m) != "" && !strings.EqualFold(strings.TrimSpace(m), "unknown")
}

func sameMovementLabel(a, b string) bool {
	ca, cb := activity.CanonicalMovement(a), activity.CanonicalMovement(b)
	if ca == "" {
		ca = strings.TrimSpace(a)
	}
	if cb == "" {
		cb = strings.TrimSpace(b)
	}
	return strings.EqualFold(ca, cb)
}

func addCandidate(list []string, m string) []string {
	if !isConcreteMovement(m) {
		return list
	}
	for _, existing := range list {
		if sameMovementLabel(existing, m) {
			return list
		}
	}
	return append(list, m)
}

// smoothChunkSequence applies hysteresis: activity and movement change only
// when the new state lasts two chunks. A single non-active chunk between two
// continuous active chunks is bridged (two or more consecutive rest chunks are
// real rest), and a single deviating label between identical neighbours is
// treated as the neighbours' movement unless it shows its own complete cycles.
// Saved user corrections are never overridden.
func smoothChunkSequence(chunks []timelineChunk) []timelineChunk {
	out := append([]timelineChunk(nil), chunks...)
	for i := 1; i+1 < len(out); i++ {
		prev, cur, next := out[i-1], out[i], out[i+1]
		if cur.UserFixed || !timelineContinuous(prev, cur) || !timelineContinuous(cur, next) {
			continue
		}
		if prev.Class != chunkActive || next.Class != chunkActive {
			continue
		}
		if cur.Class != chunkActive {
			movement := "Unknown"
			if isConcreteMovement(prev.Movement) && sameMovementLabel(prev.Movement, next.Movement) {
				movement = prev.Movement
			}
			out[i].Class, out[i].Movement = chunkActive, movement
			out[i].Candidates = addCandidate(addCandidate(nil, prev.Movement), next.Movement)
			continue
		}
		if isConcreteMovement(prev.Movement) && sameMovementLabel(prev.Movement, next.Movement) && !sameMovementLabel(cur.Movement, prev.Movement) {
			own := 0
			if isConcreteMovement(cur.Movement) {
				name := activity.CanonicalMovement(cur.Movement)
				if name == "" {
					name = strings.TrimSpace(cur.Movement)
				}
				own = cur.completeCycles[name]
			}
			if own < labelChangeMinCompleteCycles {
				out[i].Candidates = addCandidate(addCandidate(nil, prev.Movement), cur.Movement)
				out[i].Movement = prev.Movement
			}
		}
	}
	return out
}

// buildContinuousSegments groups continuous active chunks into bouts that are
// never cut at 10-second chunk edges or by movement-label changes, and returns
// explicitly non-exercise chunks as skipped media. Unclassified chunks that are
// not bridged are neither (they remain not_analyzed).
func buildContinuousSegments(chunks []timelineChunk) ([]Segment, []Segment) {
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Start < chunks[j].Start })
	// Rows of one retained object share an interval; keep the first.
	deduped := make([]timelineChunk, 0, len(chunks))
	for _, c := range chunks {
		if n := len(deduped); n > 0 && math.Abs(deduped[n-1].Start-c.Start) < 0.01 && math.Abs(deduped[n-1].End-c.End) < 0.01 {
			continue
		}
		deduped = append(deduped, c)
	}
	smoothed := smoothChunkSequence(deduped)

	var segments, skipped []Segment
	for i := 0; i < len(smoothed); {
		c := smoothed[i]
		if c.Class != chunkActive {
			if c.Class == chunkExplicitRest {
				skipped = append(skipped, Segment{Start: formatSegmentTimestamp(c.Start), End: formatSegmentTimestamp(c.End), Type: "Rest"})
			}
			i++
			continue
		}
		j := i
		for j+1 < len(smoothed) && smoothed[j+1].Class == chunkActive && timelineContinuous(smoothed[j], smoothed[j+1]) {
			j++
		}
		segments = append(segments, boutSegment(smoothed, i, j))
		i = j + 1
	}
	return segments, mergeSegmentsByMovement(skipped)
}

func boutSegment(chunks []timelineChunk, first, last int) Segment {
	// No padding into adjacent rest: skipped media must stay skipped so
	// coverage stays complete and no claimed repetition overlaps a skipped gap.
	start, end := chunks[first].Start, chunks[last].End

	counts := map[string]int{}
	order := []string{}
	var candidates []string
	var descriptions []string
	for _, c := range chunks[first : last+1] {
		if isConcreteMovement(c.Movement) {
			key := strings.ToLower(activity.CanonicalMovement(c.Movement))
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(c.Movement))
			}
			if counts[key] == 0 {
				order = append(order, c.Movement)
			}
			counts[key]++
		}
		for _, m := range c.Candidates {
			candidates = addCandidate(candidates, m)
		}
		if c.Description != "" {
			descriptions = append(descriptions, c.Description)
		}
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
	desc := strings.Join(descriptions, " | ")
	if len(candidates) > 1 {
		desc = "청크 후보 종목: " + strings.Join(candidates, ", ") + " | " + desc
	}
	return Segment{Start: formatSegmentTimestamp(start), End: formatSegmentTimestamp(end), Type: movement, Description: desc}
}
