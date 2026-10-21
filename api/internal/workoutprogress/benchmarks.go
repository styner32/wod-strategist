package workoutprogress

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/activity"
)

type Confirmation struct {
	IntervalID  string  `json:"interval_id"`
	Movement    string  `json:"movement,omitempty"`
	Start       float64 `json:"start_secs"`
	End         float64 `json:"end_secs"`
	Reps        int     `json:"reps"`
	Load        *Load   `json:"load"`
	Measurement string  `json:"measurement"`
	Technique   string  `json:"technique"`
}

type Record struct {
	ID                  uint             `json:"id"`
	SessionID           string           `json:"session_id"`
	IntervalID          string           `json:"interval_id"`
	SourceVersion       string           `json:"source_version"`
	Status              string           `json:"status"`
	Movement            string           `json:"movement"`
	Start               float64          `json:"start_secs"`
	End                 float64          `json:"end_secs"`
	ElapsedSeconds      float64          `json:"elapsed_seconds"`
	Reps                int              `json:"reps"`
	Load                *Load            `json:"load"`
	LoadKG              *float64         `json:"load_kg"`
	Measurement         string           `json:"measurement"`
	Technique           string           `json:"technique"`
	WorkoutPrescription string           `json:"workout_prescription"`
	RepsPerMinute       float64          `json:"reps_per_minute"`
	RateBasis           string           `json:"rate_basis"`
	MedianCycleSeconds  *float64         `json:"median_cycle_seconds"`
	PerformedAt         *time.Time       `json:"performed_at"`
	PerformedAtSource   *string          `json:"performed_at_source"`
	ConfirmedAt         time.Time        `json:"confirmed_at"`
	RestComparisons     []RestComparison `json:"rest_comparisons"`
}

type RestComparison struct {
	RestStart                float64  `json:"rest_start_secs"`
	RestEnd                  float64  `json:"rest_end_secs"`
	RestElapsedSeconds       *float64 `json:"rest_elapsed_seconds"`
	BeforeMedianCycleSeconds *float64 `json:"before_median_cycle_seconds"`
	AfterMedianCycleSeconds  *float64 `json:"after_median_cycle_seconds"`
	CycleChangePercent       *float64 `json:"cycle_change_percent"`
}

type Comparison struct {
	CurrentRecordID             uint     `json:"current_record_id"`
	BaselineRecordID            uint     `json:"baseline_record_id"`
	Kind                        string   `json:"kind"`
	ConditionDifferences        []string `json:"condition_differences"`
	RateChangePercent           *float64 `json:"rate_change_percent"`
	HeavierLoadKG               *float64 `json:"heavier_load_kg"`
	HeavierLoadBaselineRecordID *uint    `json:"heavier_load_baseline_record_id"`
}

func Kilograms(load *Load) *float64 {
	if load == nil || !ValidLoad(load) {
		return nil
	}
	kg := load.Value
	if load.Unit == "lb" {
		kg *= 0.45359237
	}
	kg = math.Round(kg*10) / 10
	return &kg
}

// Confirm leaves the raw interval unchanged. A null load explicitly confirms no
// external load, while an unconfirmed candidate remains absent from benchmarks.
func Confirm(progress Progress, input Confirmation) (Record, error) {
	if !activity.ValidInterval(input.Start, input.End) || input.Reps < 0 || input.Reps > math.MaxInt32 || !ValidLoad(input.Load) || (input.Measurement != "set" && input.Measurement != "window") || (input.Technique != "unspecified" && input.Technique != "singles" && input.Technique != "touch_and_go") {
		return Record{}, errors.New("invalid performance confirmation")
	}
	explicitMovement := strings.TrimSpace(input.Movement)
	if explicitMovement != "" {
		if strings.EqualFold(explicitMovement, "rest") || strings.EqualFold(explicitMovement, "unknown") || strings.EqualFold(explicitMovement, "none") {
			return Record{}, errors.New("unknown exercise movement")
		}
		if canonical := activity.CanonicalMovement(explicitMovement); canonical != "" {
			explicitMovement = canonical
		}
	}
	var interval *Interval
	for i := range progress.Intervals {
		if progress.Intervals[i].ID == input.IntervalID {
			interval = &progress.Intervals[i]
			break
		}
	}
	var (
		spans []observedSpan
		err   error
	)
	isSyntheticInterval := interval == nil || interval.Activity != "exercise" || strings.TrimSpace(interval.Movement) == ""
	if isSyntheticInterval {
		if explicitMovement == "" {
			return Record{}, errors.New("selected window is not within an observed exercise interval")
		}
		if input.Measurement == "set" {
			for _, in := range progress.Intervals {
				if in.Activity == "rest" && in.End > input.Start+1e-6 && in.Start < input.End-1e-6 {
					return Record{}, errors.New("a set cannot include rest intervals")
				}
			}
		}
		synthetic := Interval{
			ID:       input.IntervalID,
			Start:    input.Start,
			End:      input.End,
			Activity: "exercise",
			Movement: explicitMovement,
		}
		if interval != nil && interval.CaptureStart != nil && interval.CaptureEnd != nil && activity.ValidInterval(*interval.CaptureStart, *interval.CaptureEnd) {
			synthetic.Start = interval.Start
			synthetic.End = interval.End
			synthetic.CaptureStart = interval.CaptureStart
			synthetic.CaptureEnd = interval.CaptureEnd
		}
		interval = &synthetic
		spans = []observedSpan{{Interval: synthetic, Start: input.Start, End: input.End}}
	} else {
		spans, err = selectObservedSpans(progress, input)
		if err != nil {
			return Record{}, err
		}
	}
	for _, gap := range progress.Unassessed {
		if gap.Clock == "media" && input.Start < gap.End && input.End > gap.Start {
			if input.Measurement == "set" || isSyntheticInterval {
				if gap.Reason == "unverified_capture_mapping" {
					return Record{}, errors.New("selected window contains unavailable video evidence")
				}
			} else if !countOnlyGap(gap) {
				return Record{}, errors.New("selected window contains unavailable video evidence")
			}
		}
	}
	var (
		elapsed         float64
		continuousClock bool
	)
	if (input.Measurement == "set" || isSyntheticInterval) && len(spans) == 1 {
		in := spans[0].Interval
		if (in.CaptureStart != nil) != (in.CaptureEnd != nil) {
			return Record{}, errors.New("capture mapping is invalid")
		}
		if in.CaptureStart == nil {
			elapsed = input.End - input.Start
			continuousClock = true
		} else {
			if !activity.ValidInterval(*in.CaptureStart, *in.CaptureEnd) {
				return Record{}, errors.New("capture mapping is invalid")
			}
			captureDuration := *in.CaptureEnd - *in.CaptureStart
			continuousClock = math.Abs(captureDuration-(in.End-in.Start)) <= 0.01
			start := *in.CaptureStart + (input.Start - in.Start)
			end := *in.CaptureStart + (input.End - in.Start)
			if input.Start == in.Start {
				start = *in.CaptureStart
			}
			if input.End == in.End {
				end = *in.CaptureEnd
			}
			if start < 0 && input.Start < in.Start {
				end -= start
				start = 0
			}
			if !activity.ValidInterval(start, end) {
				return Record{}, errors.New("selected subwindow capture timing cannot be verified")
			}
			elapsed = end - start
		}
	} else {
		elapsed, continuousClock, err = selectedElapsed(progress, spans)
		if err != nil {
			return Record{}, err
		}
	}
	movement := interval.Movement
	if explicitMovement != "" {
		movement = explicitMovement
	}
	if canonical := activity.CanonicalMovement(movement); canonical != "" {
		movement = canonical
	}
	if activity.UnitForMovement(movement) != "reps" {
		return Record{}, errors.New("duration movements cannot confirm repetition benchmarks")
	}
	record := Record{IntervalID: input.IntervalID, SourceVersion: progress.SourceVersion, Status: "confirmed", Movement: movement, Start: input.Start, End: input.End, ElapsedSeconds: elapsed, Reps: input.Reps, Measurement: input.Measurement, Technique: input.Technique, RateBasis: "normalized_per_minute", RepsPerMinute: float64(input.Reps) * 60 / elapsed}
	if record.RepsPerMinute < 0 || (input.Reps > 0 && record.RepsPerMinute <= 0) || math.IsInf(record.RepsPerMinute, 0) || math.IsNaN(record.RepsPerMinute) {
		return Record{}, errors.New("selected window cannot produce a finite performance rate")
	}
	if input.Load != nil {
		load := *input.Load
		load.Status = "confirmed"
		record.Load = &load
	}
	record.LoadKG = Kilograms(record.Load)
	if input.Measurement == "window" && math.Abs(elapsed-60) < 0.001 {
		record.RateBasis = "actual_60_second_window"
	}
	cycles := []float64{}
	for _, span := range spans {
		for _, e := range span.Interval.RepEvents {
			if e.Start >= input.Start && e.End <= input.End && e.Complete && e.Unit == "reps" && e.Movement == movement {
				cycles = append(cycles, e.End-e.Start)
			}
		}
	}
	completeCoverage := true
	for _, gap := range progress.Unassessed {
		if gap.Clock == "media" && gap.Start < input.End && gap.End > input.Start {
			completeCoverage = false
		}
	}
	if input.Reps > 0 && continuousClock && completeCoverage && len(cycles) == input.Reps {
		sort.Float64s(cycles)
		median := cycles[len(cycles)/2]
		if len(cycles)%2 == 0 {
			median = (median + cycles[len(cycles)/2-1]) / 2
		}
		record.MedianCycleSeconds = &median
	}
	record.RestComparisons = RestComparisons(progress, input)
	return record, nil
}

// RestComparisons describes observed before/after cycle duration. It does not
// estimate biological recovery and never mutates the source observations.
func RestComparisons(progress Progress, input Confirmation) []RestComparison {
	out := []RestComparison{}
	if input.Measurement != "window" || input.Reps <= 0 || !activity.ValidInterval(input.Start, input.End) {
		return out
	}
	spans, err := selectObservedSpans(progress, input)
	if err != nil {
		return out
	}
	_, cycleClockVerified, err := selectedElapsed(progress, spans)
	if err != nil {
		return out
	}
	if len(spans) < 3 {
		return out
	}
	movement := activity.CanonicalMovement(spans[0].Interval.Movement)
	if movement == "" {
		movement = strings.TrimSpace(spans[0].Interval.Movement)
	}
	completeCount := 0
	for _, span := range spans {
		completeCount += len(spanCycleSeconds(span, movement))
	}
	completeEvidence := cycleClockVerified && completeCount == input.Reps
	for _, gap := range progress.Unassessed {
		if gap.Clock == "media" && input.Start < gap.End && input.End > gap.Start {
			completeEvidence = false
		}
	}
	for i := 1; i < len(spans)-1; i++ {
		if spans[i].Interval.Activity != "rest" {
			continue
		}
		end := i + 1
		for end < len(spans) && spans[end].Interval.Activity == "rest" {
			end++
		}
		if end >= len(spans) || spans[i-1].Interval.Activity != "exercise" || spans[end].Interval.Activity != "exercise" {
			i = end - 1
			continue
		}
		comparison := RestComparison{RestStart: spans[i].Start, RestEnd: spans[end-1].End}
		if elapsed, _, err := selectedElapsed(progress, spans[i:end]); err == nil {
			comparison.RestElapsedSeconds = &elapsed
		}
		if completeEvidence {
			comparison.BeforeMedianCycleSeconds = medianCycleSeconds(spanCycleSeconds(spans[i-1], movement))
			comparison.AfterMedianCycleSeconds = medianCycleSeconds(spanCycleSeconds(spans[end], movement))
			if comparison.BeforeMedianCycleSeconds != nil && comparison.AfterMedianCycleSeconds != nil {
				change := (*comparison.AfterMedianCycleSeconds / *comparison.BeforeMedianCycleSeconds - 1) * 100
				if !math.IsInf(change, 0) && !math.IsNaN(change) {
					comparison.CycleChangePercent = &change
				}
			}
		}
		out = append(out, comparison)
		i = end - 1
	}
	return out
}

func spanCycleSeconds(span observedSpan, movement string) []float64 {
	out := []float64{}
	for _, event := range span.Interval.RepEvents {
		if event.Complete && event.Unit == "reps" && event.Start >= span.Start && event.End <= span.End && activity.CanonicalMovement(event.Movement) == movement {
			out = append(out, event.End-event.Start)
		}
	}
	return out
}

func medianCycleSeconds(cycles []float64) *float64 {
	if len(cycles) == 0 {
		return nil
	}
	sort.Float64s(cycles)
	median := cycles[len(cycles)/2]
	if len(cycles)%2 == 0 {
		median = (median + cycles[len(cycles)/2-1]) / 2
	}
	return &median
}

// RestComparisonsFromCandidate reconstructs response-only metrics from the
// preserved confirmed window, including after the operational source changes.
func RestComparisonsFromCandidate(raw []byte, record Record) []RestComparison {
	out := []RestComparison{}
	if record.Measurement != "window" {
		return out
	}
	var candidate struct {
		Version       int        `json:"version"`
		Clock         string     `json:"clock"`
		IntervalID    string     `json:"interval_id"`
		Start         float64    `json:"start_secs"`
		End           float64    `json:"end_secs"`
		SourceVersion string     `json:"source_version"`
		Intervals     []Interval `json:"intervals"`
		Unassessed    []Gap      `json:"unassessed"`
	}
	if json.Unmarshal(raw, &candidate) != nil || candidate.Version != Version || candidate.Clock != "media" || candidate.SourceVersion != record.SourceVersion || candidate.IntervalID != record.IntervalID || candidate.Start != record.Start || candidate.End != record.End || candidate.Intervals == nil || candidate.Unassessed == nil {
		return out
	}
	progress := Progress{Version: candidate.Version, Clock: candidate.Clock, SourceVersion: candidate.SourceVersion, Intervals: candidate.Intervals, Unassessed: candidate.Unassessed}
	doc, err := json.Marshal(progress)
	if err != nil {
		return out
	}
	progress, err = Decode(doc)
	if err != nil {
		return out
	}
	return RestComparisons(progress, Confirmation{IntervalID: record.IntervalID, Start: record.Start, End: record.End, Reps: record.Reps, Measurement: record.Measurement})
}

type observedSpan struct {
	Interval   Interval
	Start, End float64
}

func countOnlyGap(gap Gap) bool {
	return gap.Reason == "no_countable_evidence" || gap.Reason == "incomplete_cycle" || gap.Reason == "unsupported_movement_or_unit" || gap.Reason == "unit_mismatch"
}

func coveredByCountOnlyGaps(unassessed []Gap, from, to float64) bool {
	if to <= from+1e-6 {
		return true
	}
	cursor := from
	for _, gap := range unassessed {
		if gap.Clock != "media" || !countOnlyGap(gap) || gap.End <= cursor {
			continue
		}
		if gap.Start > cursor+1e-6 {
			return false
		}
		if gap.End > cursor {
			cursor = gap.End
		}
		if cursor >= to-1e-6 {
			return true
		}
	}
	return cursor >= to-1e-6
}

// Window confirmation may include visibly observed rests between repetitions;
// a set may span contiguous same-movement exercise intervals (including across
// 10-second segment boundaries and count-only incomplete-cycle boundary gaps)
// as long as no rest interval is included.
func selectObservedSpans(progress Progress, input Confirmation) ([]observedSpan, error) {
	anchor := -1
	for i := range progress.Intervals {
		if progress.Intervals[i].ID == input.IntervalID {
			anchor = i
			break
		}
	}
	if anchor < 0 {
		return nil, errors.New("exercise interval not found")
	}
	first := progress.Intervals[anchor]
	if first.Activity != "exercise" || input.Start >= first.End || input.End <= first.Start {
		return nil, errors.New("window must overlap its selected exercise interval")
	}
	startIdx := anchor
	if input.Start < first.Start {
		for i := 0; i < anchor; i++ {
			if progress.Intervals[i].End > input.Start {
				startIdx = i
				break
			}
		}
	}
	name := activity.CanonicalMovement(first.Movement)
	if name == "" {
		name = strings.TrimSpace(first.Movement)
	}
	if input.Measurement == "set" {
		var exSpans []Interval
		var candidate *Load
		for _, in := range progress.Intervals {
			if in.End <= input.Start || in.Start >= input.End || in.Activity != "exercise" {
				continue
			}
			movement := activity.CanonicalMovement(in.Movement)
			if movement == "" {
				movement = strings.TrimSpace(in.Movement)
			}
			if movement == name && in.Load != nil {
				if candidate != nil && (candidate.Basis != in.Load.Basis || *Kilograms(candidate) != *Kilograms(in.Load)) {
					return nil, errors.New("selected window contains different load candidates; confirm separate windows")
				}
				candidate = in.Load
			}
			exSpans = append(exSpans, in)
		}
		if len(exSpans) >= 2 {
			firstExEnd := exSpans[0].End
			lastExStart := exSpans[len(exSpans)-1].Start
			for _, in := range progress.Intervals {
				if in.Activity == "rest" && in.End > firstExEnd-1e-6 && in.Start < lastExStart+1e-6 {
					return nil, errors.New("a set cannot include rest intervals")
				}
			}
		}
		combined := first
		if len(exSpans) > 1 {
			events := make([]activity.Observation, 0)
			refs := make([]string, 0)
			seenRefs := map[string]bool{}
			for _, in := range exSpans {
				events = append(events, in.RepEvents...)
				for _, ref := range in.ObservationRefs {
					if !seenRefs[ref] {
						seenRefs[ref] = true
						refs = append(refs, ref)
					}
				}
			}
			combined.RepEvents = events
			combined.ObservationRefs = refs
		}
		return []observedSpan{{Interval: combined, Start: input.Start, End: input.End}}, nil
	}
	spans := []observedSpan{}
	cursor := input.Start
	var candidate *Load
	for i := startIdx; i < len(progress.Intervals) && cursor < input.End; i++ {
		in := progress.Intervals[i]
		if in.End <= cursor {
			continue
		}
		if in.Start > cursor+0.000001 {
			canBridge := in.CaptureStart == nil && in.CaptureEnd == nil &&
				(len(spans) == 0 || (spans[len(spans)-1].Interval.CaptureStart == nil && spans[len(spans)-1].Interval.CaptureEnd == nil)) &&
				coveredByCountOnlyGaps(progress.Unassessed, cursor, in.Start)
			if !canBridge {
				return nil, errors.New("selected window contains an observation coverage gap")
			}
			if len(spans) > 0 {
				spans[len(spans)-1].End = in.Start
				cursor = in.Start
			}
		}
		if in.Activity == "exercise" {
			movement := activity.CanonicalMovement(in.Movement)
			if movement == "" {
				movement = strings.TrimSpace(in.Movement)
			}
			if movement != name {
				return nil, errors.New("selected window contains a different exercise")
			}
			if in.Load != nil {
				if candidate != nil && (candidate.Basis != in.Load.Basis || *Kilograms(candidate) != *Kilograms(in.Load)) {
					return nil, errors.New("selected window contains different load candidates; confirm separate windows")
				}
				candidate = in.Load
			}
		} else if in.Activity == "rest" {
			if input.Measurement == "set" {
				return nil, errors.New("a set cannot include rest intervals")
			}
		} else {
			return nil, errors.New("selected window contains unobserved activity")
		}
		end := math.Min(input.End, in.End)
		spans = append(spans, observedSpan{Interval: in, Start: cursor, End: end})
		cursor = end
	}
	if cursor < input.End-1e-6 {
		if len(spans) > 0 &&
			spans[len(spans)-1].Interval.CaptureStart == nil &&
			spans[len(spans)-1].Interval.CaptureEnd == nil &&
			coveredByCountOnlyGaps(progress.Unassessed, cursor, input.End) {
			spans[len(spans)-1].End = input.End
			cursor = input.End
		} else {
			return nil, errors.New("selected window exceeds observed coverage")
		}
	}
	return spans, nil
}

func selectedElapsed(progress Progress, spans []observedSpan) (float64, bool, error) {
	mapped := 0
	for _, span := range spans {
		if span.Interval.CaptureStart != nil || span.Interval.CaptureEnd != nil {
			if span.Interval.CaptureStart == nil || span.Interval.CaptureEnd == nil || !activity.ValidInterval(*span.Interval.CaptureStart, *span.Interval.CaptureEnd) {
				return 0, false, errors.New("capture mapping is invalid")
			}
			mapped++
		}
	}
	if mapped != 0 && mapped != len(spans) {
		return 0, false, errors.New("selected window has incomplete capture mapping")
	}
	if mapped == 0 {
		for _, gap := range progress.Unassessed {
			if gap.Clock == "capture" {
				return 0, false, errors.New("capture mapping is required to exclude recording gaps")
			}
		}
		elapsed := 0.0
		for _, span := range spans {
			elapsed += span.End - span.Start
		}
		return elapsed, true, nil
	}
	elapsed := 0.0
	cycleClockVerified := true
	previousEnd := -1.0
	for _, span := range spans {
		in := span.Interval
		captureDuration := *in.CaptureEnd - *in.CaptureStart
		whole := span.Start == in.Start && span.End == in.End
		if math.Abs(captureDuration-(in.End-in.Start)) > 0.01 {
			if !whole {
				return 0, false, errors.New("capture timing of the selected subwindow cannot be verified")
			}
			cycleClockVerified = false
		}
		start := *in.CaptureStart + (span.Start - in.Start)
		end := *in.CaptureStart + (span.End - in.Start)
		if span.Start == in.Start {
			start = *in.CaptureStart
		}
		if span.End == in.End {
			end = *in.CaptureEnd
		}
		if !activity.ValidInterval(start, end) {
			return 0, false, errors.New("selected subwindow capture timing cannot be verified")
		}
		for _, gap := range progress.Unassessed {
			if gap.Clock == "capture" && ((start < gap.End && end > gap.Start) || (*in.CaptureStart < gap.End && *in.CaptureEnd > gap.Start)) {
				return 0, false, errors.New("selected interval crosses a recording gap")
			}
		}
		if previousEnd >= 0 && math.Abs(start-previousEnd) > 0.01 {
			return 0, false, errors.New("selected window crosses discontinuous capture timing")
		}
		elapsed += end - start
		previousEnd = end
	}
	return elapsed, cycleClockVerified, nil
}

// WindowBounds provides media bounds for a flat-load repeated-movement window
// beginning at the selected exercise. Actual confirmation revalidates the span.
func WindowBounds(progress Progress, intervalID string) (float64, float64, bool) {
	var anchor *Interval
	for i := range progress.Intervals {
		if progress.Intervals[i].ID == intervalID {
			anchor = &progress.Intervals[i]
			break
		}
	}
	if anchor == nil || anchor.Activity != "exercise" {
		return 0, 0, false
	}
	ends := []float64{}
	for _, in := range progress.Intervals {
		if in.End > anchor.Start {
			ends = append(ends, in.End)
		}
	}
	for _, gap := range progress.Unassessed {
		if gap.Clock == "media" && gap.Start > anchor.Start && !countOnlyGap(gap) {
			ends = append(ends, gap.Start)
		}
	}
	sort.Float64s(ends)
	maxEnd := anchor.Start
	for _, end := range ends {
		if end <= maxEnd {
			continue
		}
		input := Confirmation{IntervalID: intervalID, Start: anchor.Start, End: end, Measurement: "window"}
		spans, err := selectObservedSpans(progress, input)
		if err != nil {
			break
		}
		clear := true
		for _, gap := range progress.Unassessed {
			if gap.Clock == "media" && anchor.Start < gap.End && end > gap.Start && !countOnlyGap(gap) {
				clear = false
				break
			}
		}
		if !clear {
			break
		}
		if _, _, err = selectedElapsed(progress, spans); err != nil {
			break
		}
		maxEnd = end
	}
	return anchor.Start, maxEnd, maxEnd > anchor.Start
}

func conditionDifferences(current, previous Record) []string {
	diff := []string{}
	if activity.CanonicalMovement(current.Movement) == "" || activity.CanonicalMovement(previous.Movement) == "" {
		diff = append(diff, "unknown_movement")
	}
	if (current.Load == nil) != (previous.Load == nil) || (current.LoadKG != nil && previous.LoadKG != nil && *current.LoadKG != *previous.LoadKG) {
		diff = append(diff, "load")
	}
	if current.Load != nil && previous.Load != nil && current.Load.Basis != previous.Load.Basis {
		diff = append(diff, "load_basis")
	}
	if current.Measurement != previous.Measurement {
		diff = append(diff, "measurement")
	}
	if current.Measurement == "set" && previous.Measurement == "set" {
		if current.Reps != previous.Reps {
			diff = append(diff, "reps")
		}
	} else if math.Abs(recordDuration(current)-recordDuration(previous)) > 0.001 {
		diff = append(diff, "window_duration")
	}
	if strings.TrimSpace(current.WorkoutPrescription) == "" || strings.TrimSpace(previous.WorkoutPrescription) == "" {
		diff = append(diff, "unknown_workout_prescription")
	} else if current.WorkoutPrescription != previous.WorkoutPrescription {
		diff = append(diff, "workout_prescription")
	}
	if current.Technique == "unspecified" || previous.Technique == "unspecified" {
		diff = append(diff, "unknown_technique")
	} else if current.Technique != previous.Technique {
		diff = append(diff, "technique")
	}
	return diff
}

func recordDuration(record Record) float64 {
	if record.ElapsedSeconds > 0 {
		return record.ElapsedSeconds
	}
	return record.End - record.Start
}

func recordBefore(a, b Record) bool {
	// Confirmation time is never substituted for unknown workout time.
	if a.PerformedAt == nil || b.PerformedAt == nil {
		return false
	}
	if a.PerformedAt.Equal(*b.PerformedAt) {
		return a.SessionID == b.SessionID && a.Start < b.Start
	}
	return a.PerformedAt.Before(*b.PerformedAt)
}

func recordSupersedes(candidate, target Record, candidateIdx, targetIdx int) bool {
	if candidateIdx == targetIdx {
		return false
	}
	if candidate.SessionID != target.SessionID {
		return false
	}
	if candidate.PerformedAt != nil && target.PerformedAt != nil && !candidate.PerformedAt.Equal(*target.PerformedAt) {
		return false
	}
	if !(candidate.Start < target.End-1e-6 && candidate.End > target.Start+1e-6) {
		return false
	}
	if candidate.Status == "confirmed" && target.Status != "confirmed" {
		return true
	}
	if candidate.Status != "confirmed" && target.Status == "confirmed" {
		return false
	}
	candidateDur := candidate.End - candidate.Start
	targetDur := target.End - target.Start
	candidateContainsTarget := candidate.Start <= target.Start+1e-6 && candidate.End >= target.End-1e-6 && candidateDur > targetDur+1e-6
	targetContainsCandidate := target.Start <= candidate.Start+1e-6 && target.End >= candidate.End-1e-6 && targetDur > candidateDur+1e-6
	if candidateContainsTarget && !targetContainsCandidate {
		return true
	}
	if targetContainsCandidate && !candidateContainsTarget {
		return false
	}
	if !candidate.ConfirmedAt.IsZero() && !target.ConfirmedAt.IsZero() && !candidate.ConfirmedAt.Equal(target.ConfirmedAt) {
		return candidate.ConfirmedAt.After(target.ConfirmedAt)
	}
	if candidate.ID != target.ID {
		return candidate.ID > target.ID
	}
	return candidateIdx > targetIdx
}

// ActiveRecords filters out same-session records that have been superseded by a
// wider merged confirmation or a more recent overlapping confirmation.
func ActiveRecords(records []Record) []Record {
	out := make([]Record, 0, len(records))
	for targetIdx, target := range records {
		superseded := false
		for candidateIdx, candidate := range records {
			if recordSupersedes(candidate, target, candidateIdx, targetIdx) {
				superseded = true
				break
			}
		}
		if !superseded {
			out = append(out, target)
		}
	}
	return out
}

func Comparisons(records []Record) []Comparison {
	out := []Comparison{}
	for _, current := range records {
		if current.Status != "confirmed" || current.Reps <= 0 || current.RepsPerMinute <= 0 || current.PerformedAt == nil {
			continue
		}
		var direct, reference *Record
		for i := range records {
			prior := &records[i]
			if prior.ID == current.ID || prior.Status != "confirmed" || prior.Reps <= 0 || prior.RepsPerMinute <= 0 || prior.Movement != current.Movement || !recordBefore(*prior, current) {
				continue
			}
			if reference == nil || recordBefore(*reference, *prior) {
				reference = prior
			}
			if len(conditionDifferences(current, *prior)) == 0 && (direct == nil || recordBefore(*direct, *prior)) {
				direct = prior
			}
		}
		base := direct
		kind := "direct"
		if base == nil {
			base = reference
			kind = "reference"
		}
		if base == nil {
			continue
		}
		comparison := Comparison{CurrentRecordID: current.ID, BaselineRecordID: base.ID, Kind: kind, ConditionDifferences: conditionDifferences(current, *base)}
		if kind == "direct" && base.RepsPerMinute > 0 {
			change := (current.RepsPerMinute/base.RepsPerMinute - 1) * 100
			comparison.RateChangePercent = &change
		}
		// The most recent comparable lower-load equal-rep record is independent
		// of the same-load pace baseline. This is performed load, not force/1RM.
		var heavierBaseline *Record
		for i := range records {
			prior := &records[i]
			if prior.Status != "confirmed" || prior.Reps <= 0 || prior.RepsPerMinute <= 0 || prior.Movement != current.Movement || !recordBefore(*prior, current) || current.Reps != prior.Reps || current.LoadKG == nil || prior.LoadKG == nil || current.Load == nil || prior.Load == nil || current.Load.Basis != prior.Load.Basis || *current.LoadKG <= *prior.LoadKG {
				continue
			}
			compatible := true
			for _, d := range conditionDifferences(current, *prior) {
				if d != "load" {
					compatible = false
					break
				}
			}
			if compatible && (heavierBaseline == nil || recordBefore(*heavierBaseline, *prior)) {
				heavierBaseline = prior
			}
		}
		if heavierBaseline != nil {
			gain := *current.LoadKG - *heavierBaseline.LoadKG
			id := heavierBaseline.ID
			comparison.HeavierLoadKG = &gain
			comparison.HeavierLoadBaselineRecordID = &id
		}
		out = append(out, comparison)
	}
	return out
}

func Best(records []Record) []Record {
	out := []Record{}
	for _, r := range records {
		if r.Status != "confirmed" || r.Reps <= 0 || r.RepsPerMinute <= 0 || r.PerformedAt == nil || r.Technique == "unspecified" || strings.TrimSpace(r.WorkoutPrescription) == "" || activity.CanonicalMovement(r.Movement) == "" {
			continue
		}
		found := false
		for i := range out {
			if out[i].Movement == r.Movement && len(conditionDifferences(r, out[i])) == 0 {
				if r.RepsPerMinute > out[i].RepsPerMinute {
					out[i] = r
				}
				found = true
				break
			}
		}
		if !found {
			out = append(out, r)
		}
	}
	return out
}
