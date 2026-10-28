package recognitionreview

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
	"github.com/wod-strategist/api/internal/workoutprogress"
)

const epsilon = 1e-6

// BuildEvaluation reads raw persisted progress, never a confirmation's possibly
// synthesized original_candidate. Counts are owned by completion in (start,end].
func BuildEvaluation(progress workoutprogress.Progress, records []workoutprogress.Record, chunks []ChunkEvidence, analysisOutput string) Evaluation {
	current := []workoutprogress.Record{}
	for _, r := range records {
		if r.Status == "confirmed" && r.SourceVersion == progress.SourceVersion && activity.ValidInterval(r.Start, r.End) {
			current = append(current, r)
		}
	}
	current = workoutprogress.ActiveRecords(current)
	sort.Slice(current, func(i, j int) bool {
		if current[i].Start == current[j].Start {
			return current[i].ID < current[j].ID
		}
		return current[i].Start < current[j].Start
	})
	out := Evaluation{SourceVersion: progress.SourceVersion, Records: []RecordEvaluation{}}
	for _, r := range current {
		entry := compareRecord(progress, r)
		out.Records = append(out.Records, entry)
		out.Summary.Total++
		switch entry.Status {
		case "matched":
			out.Summary.Matched++
		case "mismatch":
			out.Summary.Mismatch++
		default:
			out.Summary.InsufficientEvidence++
		}
	}
	// Include all input evidence so a diagnosis cannot silently use changed chunks
	// or assessment text merely because the progress source_version stayed equal.
	orderedChunks := append([]ChunkEvidence{}, chunks...)
	sort.Slice(orderedChunks, func(i, j int) bool { return orderedChunks[i].ID < orderedChunks[j].ID })
	raw, _ := json.Marshal(struct {
		Progress workoutprogress.Progress
		Records  []workoutprogress.Record
		Chunks   []ChunkEvidence
		Output   string
	}{progress, current, orderedChunks, analysisOutput})
	hash := sha256.Sum256(raw)
	out.Fingerprint = hex.EncodeToString(hash[:])
	return out
}

func movementName(name string) string {
	if v := activity.CanonicalMovement(name); v != "" {
		return v
	}
	return strings.TrimSpace(name)
}

func compareRecord(p workoutprogress.Progress, r workoutprogress.Record) RecordEvaluation {
	out := RecordEvaluation{Record: r, PredictedMovements: []MovementCount{}, Gaps: []workoutprogress.Gap{}, SourceRefs: []string{}, Status: "insufficient_evidence"}
	counts := map[string]int{}
	refs := map[string]bool{}
	events := map[string]bool{}
	covered := []workoutprogress.Gap{}
	count := 0
	knownMovement, wrongMovement := false, false
	complete := true
	for _, in := range p.Intervals {
		lo, hi := math.Max(r.Start, in.Start), math.Min(r.End, in.End)
		if hi <= lo {
			continue
		}
		if in.Activity == "unknown" {
			complete = false
		} else {
			covered = append(covered, workoutprogress.Gap{Start: lo, End: hi})
		}
		if in.Activity == "exercise" {
			name := movementName(in.Movement)
			if name == "" || strings.EqualFold(name, "unknown") {
				complete = false
			} else {
				knownMovement = true
				if !strings.EqualFold(name, movementName(r.Movement)) {
					wrongMovement = true
				}
				if _, ok := counts[name]; !ok {
					counts[name] = 0
				}
			}
		}
		for _, ref := range in.ObservationRefs {
			if strings.TrimSpace(ref) != "" {
				refs[ref] = true
			}
		}
		for _, ev := range in.RepEvents {
			if !ev.Complete || ev.Unit != "reps" || !activity.ValidInterval(ev.Start, ev.End) || ev.End <= r.Start || ev.End > r.End {
				continue
			}
			name := movementName(ev.Movement)
			key := fmt.Sprintf("%s:%.9f:%.9f", strings.ToLower(name), ev.Start, ev.End)
			if events[key] {
				continue
			}
			events[key] = true
			counts[name]++
			count++
			if name != "" {
				knownMovement = true
				if !strings.EqualFold(name, movementName(r.Movement)) {
					wrongMovement = true
				}
			}
		}
	}
	for _, gap := range p.Unassessed {
		// Capture-clock gaps cannot be projected by applying their timestamps to
		// media. A relevant recording pause is already represented in media evidence.
		if gap.Clock != "media" || gap.Start >= r.End || gap.End <= r.Start {
			continue
		}
		gap.Start = math.Max(gap.Start, r.Start)
		gap.End = math.Min(gap.End, r.End)
		out.Gaps = append(out.Gaps, gap)
		// An observed incomplete cycle establishes no completed repetition for
		// that cycle; missing/unsupported evidence does not establish zero.
		if gap.Reason != "incomplete_cycle" {
			complete = false
		}
	}
	sort.Slice(covered, func(i, j int) bool { return covered[i].Start < covered[j].Start })
	cursor := r.Start
	for _, span := range covered {
		if span.Start > cursor+epsilon {
			complete = false
		}
		cursor = math.Max(cursor, span.End)
	}
	if cursor < r.End-epsilon {
		complete = false
	}
	if activity.UnitForMovement(movementName(r.Movement)) != "reps" {
		complete = false
	}
	for name, n := range counts {
		out.PredictedMovements = append(out.PredictedMovements, MovementCount{Movement: name, Reps: n})
	}
	sort.Slice(out.PredictedMovements, func(i, j int) bool { return out.PredictedMovements[i].Movement < out.PredictedMovements[j].Movement })
	for ref := range refs {
		out.SourceRefs = append(out.SourceRefs, ref)
	}
	sort.Strings(out.SourceRefs)
	out.EvidenceComplete = complete
	if complete {
		out.ObservedCompleteReps = &count
		delta := count - r.Reps
		out.RepsDelta = &delta
	}
	if wrongMovement {
		v := false
		out.MovementMatch = &v
	} else if complete {
		v := knownMovement
		out.MovementMatch = &v
	}
	if out.MovementMatch != nil && !*out.MovementMatch {
		out.Status = "mismatch"
	} else if complete && out.RepsDelta != nil {
		if *out.RepsDelta == 0 {
			out.Status = "matched"
		} else {
			out.Status = "mismatch"
		}
	}
	return out
}

// BuildBatches uses the production Pass 2 limits: balanced <=150s cores with
// 5s context at internal boundaries. Context never escapes the confirmed record.
func BuildBatches(records []RecordEvaluation, start, end float64, recordIDs []uint) ([]Batch, error) {
	if !activity.ValidInterval(start, end) || len(recordIDs) == 0 || len(recordIDs) > 100 {
		return nil, errors.New("select 1–100 records and a valid video range")
	}
	ids := map[uint]bool{}
	for _, id := range recordIDs {
		if id == 0 || ids[id] {
			return nil, errors.New("record IDs must be unique")
		}
		ids[id] = true
	}
	out := []Batch{}
	found := 0
	for _, row := range records {
		if !ids[row.Record.ID] {
			continue
		}
		found++
		r := row.Record
		if r.Start < start-epsilon || r.End > end+epsilon {
			return nil, errors.New("selected records must fit entirely inside the review range")
		}
		n := int(math.Ceil((r.End - r.Start) / 150))
		if n < 1 {
			return nil, errors.New("invalid confirmed record bounds")
		}
		step := (r.End - r.Start) / float64(n)
		for i := 0; i < n; i++ {
			lo := r.Start + float64(i)*step
			hi := r.Start + float64(i+1)*step
			if i == n-1 {
				hi = r.End
			}
			a, b := lo, hi
			if i > 0 {
				a = math.Max(r.Start, lo-5)
			}
			if i < n-1 {
				b = math.Min(r.End, hi+5)
			}
			out = append(out, Batch{ID: len(out) + 1, RecordID: r.ID, StartSecs: a, EndSecs: b, CoreStartSecs: lo, CoreEndSecs: hi, Status: "QUEUED", Findings: []Finding{}})
		}
	}
	if found != len(ids) {
		return nil, errors.New("selected record is unavailable or needs confirmation")
	}
	return out, nil
}
