package workoutprogress

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"time"
)

func cleanCycle(start, end float64) activity.Observation {
	return activity.Observation{Movement: "Power Clean", Unit: "reps", Start: start, End: end, Complete: true, Evidence: "bar moves from floor to front rack"}
}
func exercise(start, end float64, events ...activity.Observation) Interval {
	return Interval{Start: start, End: end, Activity: "exercise", Movement: "Power Clean", ObservationRefs: []string{"segment-1"}, RepEvents: events}
}

var _ = Describe("Independent workout progress", func() {
	It("preserves reappearing movement, load change and clock mapping without inventing rest", func() {
		first := exercise(10, 20, cleanCycle(11, 13))
		first.Load = &Load{Value: 135, Unit: "lb", Basis: "total"}
		second := exercise(30, 45, cleanCycle(31, 33))
		second.Load = &Load{Value: 155, Unit: "lb", Basis: "total"}
		captureStart, captureEnd := 50.0, 65.0
		second.CaptureStart = &captureStart
		second.CaptureEnd = &captureEnd
		p := Build([]Interval{second, first}, []Gap{{Start: 20, End: 30, Clock: "media", Reason: "camera_gap"}}, "video-v1")
		Expect(p.Intervals).To(HaveLen(2))
		Expect(p.Unassessed).To(HaveLen(1))
		Expect(p.Intervals[0].Load.Status).To(Equal("pending_confirmation"))
		Expect(p.Intervals[1].Load.Value).To(Equal(155.0))
		Expect(*p.Intervals[1].CaptureStart).To(Equal(50.0))
		Expect(p.Intervals[0].RepEvents).To(HaveLen(1))
		raw, err := json.Marshal(p)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := Decode(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal(p))
	})
	It("owns a cycle once at its completion boundary and records incomplete evidence", func() {
		partial := cleanCycle(15, 18)
		partial.Complete = false
		p := Build([]Interval{exercise(0, 10, cleanCycle(8, 10)), exercise(10, 20, cleanCycle(8, 10), cleanCycle(12, 14), partial)}, nil, "video")
		Expect(p.Intervals[0].RepEvents).To(HaveLen(1))
		Expect(p.Intervals[1].RepEvents).To(HaveLen(1))
		Expect(p.Unassessed).To(ContainElement(Gap{Start: 15, End: 18, Clock: "media", Reason: "incomplete_cycle", Movement: "Power Clean"}))
	})
	It("withholds overlapping evidence while preserving qualitative unknown movement", func() {
		unknown := exercise(20, 30, cleanCycle(21, 23))
		unknown.Movement = "Uncatalogued mobility"
		unknown.RepEvents[0].Movement = unknown.Movement
		unknown.ROMObservations = []Observation{{Start: 20, End: 30, Evidence: "controlled depth increases"}}
		p := Build([]Interval{exercise(0, 10, cleanCycle(2, 6)), exercise(5, 15, cleanCycle(6, 8)), unknown}, nil, "video")
		Expect(p.Intervals).To(HaveLen(2))
		Expect(p.Intervals[0].RepEvents).To(BeEmpty())
		Expect(p.Intervals[1].Movement).To(Equal("Uncatalogued mobility"))
		Expect(p.Intervals[1].ROMObservations).To(HaveLen(1))
		Expect(p.Intervals[1].RepEvents).To(BeEmpty())
	})
	It("does not mutate original observations or include assessment text in its source", func() {
		input := []Interval{exercise(10, 20, cleanCycle(11, 13))}
		input[0].Load = &Load{Value: 135, Unit: "lb", Basis: "total"}
		before, _ := json.Marshal(input)
		p := Build(input, nil, "same-video")
		after, _ := json.Marshal(input)
		Expect(after).To(Equal(before))
		Expect(Build(input, nil, "same-video").SourceVersion).To(Equal(p.SourceVersion))
		p.Intervals[0].RepEvents[0].Evidence = "changed"
		Expect(input[0].RepEvents[0].Evidence).To(Equal("bar moves from floor to front rack"))
		Expect(Build(input, nil, "different-video").SourceVersion).NotTo(Equal(p.SourceVersion))
	})
	It("does not accept repetitions during claimed rest or malformed stored progress", func() {
		in := exercise(0, 10, cleanCycle(1, 3))
		in.Activity = "rest"
		p := Build([]Interval{in}, nil, "video")
		Expect(p.Intervals[0].Activity).To(Equal("unknown"))
		Expect(p.Intervals[0].RepEvents).To(BeEmpty())
		for _, raw := range []string{`null`, `{}`, `{"version":2,"clock":"media","source_version":"v","intervals":[],"unassessed":[]}`} {
			_, err := Decode([]byte(raw))
			Expect(err).To(HaveOccurred())
		}
	})
})

var _ = Describe("Confirmed performance benchmark math", func() {
	It("reports observed rest and before/after cycle duration without physiological recovery estimates", func() {
		p := Build([]Interval{exercise(0, 20, cleanCycle(1, 3), cleanCycle(6, 8), cleanCycle(12, 14)), {Start: 20, End: 40, Activity: "rest", ObservationRefs: []string{"rest"}}, exercise(40, 60, cleanCycle(44, 47), cleanCycle(49, 52))}, nil, "video")
		before, _ := json.Marshal(p)
		input := Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 5, Measurement: "window", Technique: "singles"}
		r, err := Confirm(p, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.RestComparisons).To(HaveLen(1))
		rest := r.RestComparisons[0]
		Expect(rest.RestStart).To(Equal(20.0))
		Expect(rest.RestEnd).To(Equal(40.0))
		Expect(*rest.RestElapsedSeconds).To(Equal(20.0))
		Expect(*rest.BeforeMedianCycleSeconds).To(Equal(2.0))
		Expect(*rest.AfterMedianCycleSeconds).To(Equal(3.0))
		Expect(*rest.CycleChangePercent).To(Equal(50.0))
		envelope, err := json.Marshal(map[string]any{"version": p.Version, "clock": p.Clock, "interval_id": input.IntervalID, "start_secs": input.Start, "end_secs": input.End, "source_version": p.SourceVersion, "intervals": p.Intervals, "unassessed": p.Unassessed})
		Expect(err).NotTo(HaveOccurred())
		Expect(RestComparisonsFromCandidate(envelope, r)).To(Equal(r.RestComparisons))
		r.Reps = 6
		mismatch := RestComparisonsFromCandidate(envelope, r)
		Expect(mismatch).To(HaveLen(1))
		Expect(*mismatch[0].RestElapsedSeconds).To(Equal(20.0))
		Expect(mismatch[0].BeforeMedianCycleSeconds).To(BeNil())
		Expect(mismatch[0].AfterMedianCycleSeconds).To(BeNil())
		Expect(mismatch[0].CycleChangePercent).To(BeNil())
		r.Measurement = "set"
		Expect(RestComparisonsFromCandidate(envelope, r)).To(BeEmpty())
		r.Measurement = "window"
		r.SourceVersion = "different-source"
		Expect(RestComparisonsFromCandidate(envelope, r)).To(BeEmpty())
		after, _ := json.Marshal(p)
		Expect(after).To(Equal(before))
	})
	It("keeps uncertain post-rest cycle changes null while preserving verified rest elapsed", func() {
		p := Build([]Interval{exercise(0, 20, cleanCycle(1, 3)), {Start: 20, End: 40, Activity: "rest", ObservationRefs: []string{"rest"}}, exercise(40, 60, cleanCycle(44, 47))}, nil, "video")
		input := Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 2, Measurement: "window", Technique: "singles"}
		for i := range p.Intervals {
			start, end := float64(i*40), float64((i+1)*40)
			p.Intervals[i].CaptureStart = &start
			p.Intervals[i].CaptureEnd = &end
		}
		r, err := Confirm(p, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.RestComparisons).To(HaveLen(1))
		Expect(*r.RestComparisons[0].RestElapsedSeconds).To(Equal(40.0))
		Expect(r.RestComparisons[0].CycleChangePercent).To(BeNil())
		Expect(r.RestComparisons[0].BeforeMedianCycleSeconds).To(BeNil())
		for i := range p.Intervals {
			p.Intervals[i].CaptureStart = nil
			p.Intervals[i].CaptureEnd = nil
		}
		p.Unassessed = append(p.Unassessed, Gap{Start: 45, End: 46, Clock: "media", Reason: "incomplete_cycle"})
		r, err = Confirm(p, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.RestComparisons[0].CycleChangePercent).To(BeNil())
		Expect(*r.RestComparisons[0].RestElapsedSeconds).To(Equal(20.0))
	})
	It("confirms a real 60-second repeated-movement window including observed rest", func() {
		p := Build([]Interval{exercise(0, 20, cleanCycle(1, 3), cleanCycle(6, 8), cleanCycle(12, 14)), {Start: 20, End: 40, Activity: "rest", ObservationRefs: []string{"rest"}}, exercise(40, 60, cleanCycle(44, 46), cleanCycle(49, 51))}, nil, "video")
		input := Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 5, Load: &Load{Value: 135, Unit: "lb", Basis: "total"}, Measurement: "window", Technique: "singles"}
		r, err := Confirm(p, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.ElapsedSeconds).To(Equal(60.0))
		Expect(r.RepsPerMinute).To(Equal(5.0))
		Expect(r.RateBasis).To(Equal("actual_60_second_window"))
		Expect(*r.MedianCycleSeconds).To(Equal(2.0))
		start, end, ok := WindowBounds(p, p.Intervals[0].ID)
		Expect(ok).To(BeTrue())
		Expect(start).To(Equal(0.0))
		Expect(end).To(Equal(60.0))
		input.Measurement = "set"
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
		input.Measurement = "window"
		input.Reps = 6
		r, err = Confirm(p, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.MedianCycleSeconds).To(BeNil())
	})
	It("rejects cross-window unknown activity, other movement, coverage holes and load changes", func() {
		base := []Interval{exercise(0, 20, cleanCycle(1, 3)), {Start: 20, End: 40, Activity: "rest", ObservationRefs: []string{"rest"}}, exercise(40, 60, cleanCycle(44, 46))}
		p := Build(base, nil, "video")
		input := Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 2, Measurement: "window", Technique: "singles"}
		p.Intervals[1].Activity = "unknown"
		_, err := Confirm(p, input)
		Expect(err).To(HaveOccurred())
		p = Build(base, nil, "video")
		p.Intervals[2].Movement = "Double-under"
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
		p = Build(base, nil, "video")
		p.Intervals[1].End = 35
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
		p = Build(base, nil, "video")
		p.Unassessed = append(p.Unassessed, Gap{Start: 25, End: 35, Clock: "media", Reason: "not_visible"})
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
		p = Build(base, nil, "video")
		p.Intervals[0].Load = &Load{Value: 135, Unit: "lb", Basis: "total"}
		p.Intervals[2].Load = &Load{Value: 155, Unit: "lb", Basis: "total"}
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
	})
	It("requires contiguous capture mapping across repeated exercise and observed rest", func() {
		p := Build([]Interval{exercise(0, 20, cleanCycle(1, 3)), {Start: 20, End: 40, Activity: "rest", ObservationRefs: []string{"rest"}}, exercise(40, 60, cleanCycle(44, 46))}, nil, "video")
		for i := range p.Intervals {
			a, b := float64(i*40), float64((i+1)*40)
			p.Intervals[i].CaptureStart = &a
			p.Intervals[i].CaptureEnd = &b
		}
		input := Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 2, Measurement: "window", Technique: "singles"}
		r, err := Confirm(p, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.ElapsedSeconds).To(Equal(120.0))
		Expect(r.RepsPerMinute).To(Equal(1.0))
		Expect(r.MedianCycleSeconds).To(BeNil())
		input.End = 50
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
		input.End = 60
		p.Intervals[2].CaptureStart = nil
		p.Intervals[2].CaptureEnd = nil
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
		start, end := 81.0, 121.0
		p.Intervals[2].CaptureStart = &start
		p.Intervals[2].CaptureEnd = &end
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
	})
	It("withholds a subwindow whose rounded clock endpoints cannot establish positive elapsed", func() {
		p := Build([]Interval{exercise(0, 20, cleanCycle(1, 3))}, nil, "video")
		start, end := 0.0, 19.995
		p.Intervals[0].CaptureStart = &start
		p.Intervals[0].CaptureEnd = &end
		_, err := Confirm(p, Confirmation{IntervalID: p.Intervals[0].ID, Start: 19.999, End: 20, Reps: 1, Measurement: "window", Technique: "singles"})
		Expect(err).To(HaveOccurred())
	})
	It("distinguishes a true 60-second window from normalized rate and preserves load", func() {
		p := Build([]Interval{exercise(0, 60, cleanCycle(1, 3), cleanCycle(5, 9), cleanCycle(12, 18))}, nil, "video")
		r, err := Confirm(p, Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 3, Load: &Load{Value: 135, Unit: "lb", Basis: "total"}, Measurement: "window", Technique: "singles"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.RepsPerMinute).To(Equal(3.0))
		Expect(r.RateBasis).To(Equal("actual_60_second_window"))
		Expect(*r.LoadKG).To(Equal(61.2))
		Expect(r.Load.Value).To(Equal(135.0))
		Expect(*r.MedianCycleSeconds).To(Equal(4.0))
		r, err = Confirm(p, Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 30, Reps: 3, Load: nil, Measurement: "set", Technique: "singles"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Load).To(BeNil())
		Expect(r.RepsPerMinute).To(Equal(6.0))
		Expect(r.RateBasis).To(Equal("normalized_per_minute"))
	})
	It("withholds median speed when manual count differs and rejects unavailable windows", func() {
		p := Build([]Interval{exercise(0, 60, cleanCycle(1, 3))}, nil, "video")
		r, err := Confirm(p, Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 5, Measurement: "window", Technique: "unspecified"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.MedianCycleSeconds).To(BeNil())
		p.Unassessed = append(p.Unassessed, Gap{Start: 10, End: 20, Clock: "media", Reason: "camera_gap"})
		_, err = Confirm(p, Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 5, Measurement: "window", Technique: "unspecified"})
		Expect(err).To(HaveOccurred())
		_, err = Confirm(p, Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 61, Reps: 5, Measurement: "window", Technique: "singles"})
		Expect(err).To(HaveOccurred())
	})
	It("never normalizes away recording pauses and uses verified elapsed capture time", func() {
		p := Build([]Interval{exercise(0, 60, cleanCycle(1, 3))}, nil, "video")
		start, end := 0.0, 120.0
		p.Intervals[0].CaptureStart = &start
		p.Intervals[0].CaptureEnd = &end
		input := Confirmation{IntervalID: p.Intervals[0].ID, Start: 0, End: 60, Reps: 5, Measurement: "window", Technique: "singles"}
		r, err := Confirm(p, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.ElapsedSeconds).To(Equal(120.0))
		Expect(r.RepsPerMinute).To(Equal(2.5))
		Expect(r.RateBasis).To(Equal("normalized_per_minute"))
		Expect(r.MedianCycleSeconds).To(BeNil())
		input.End = 30
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
		input.End = 60
		p.Unassessed = append(p.Unassessed, Gap{Start: 40, End: 100, Clock: "capture", Reason: "recording_gap"})
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
		p.Intervals[0].CaptureStart = nil
		p.Intervals[0].CaptureEnd = nil
		_, err = Confirm(p, input)
		Expect(err).To(HaveOccurred())
	})
	It("compares latest same-condition record and reports 3 to 5 as 66.7 percent", func() {
		day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		next := day.Add(24 * time.Hour)
		load := &Load{Value: 135, Unit: "lb", Basis: "total", Status: "confirmed"}
		base := Record{ID: 1, SessionID: "old", Status: "confirmed", Movement: "Power Clean", Load: load, LoadKG: Kilograms(load), Start: 0, End: 60, Reps: 3, RepsPerMinute: 3, Measurement: "window", Technique: "singles", WorkoutPrescription: "Grace", PerformedAt: &day}
		current := base
		current.ID = 2
		current.SessionID = "new"
		current.Reps = 5
		current.RepsPerMinute = 5
		current.PerformedAt = &next
		out := Comparisons([]Record{current, base})
		Expect(out).To(HaveLen(1))
		Expect(out[0].Kind).To(Equal("direct"))
		Expect(*out[0].RateChangePercent).To(BeNumerically("~", 66.666666, 0.0001))
		Expect(out[0].HeavierLoadKG).To(BeNil())
		Expect(Best([]Record{base, current})[0].ID).To(Equal(uint(2)))
		current.WorkoutPrescription = "Different WOD"
		out = Comparisons([]Record{current, base})
		Expect(out[0].Kind).To(Equal("reference"))
		Expect(out[0].RateChangePercent).To(BeNil())
		Expect(out[0].ConditionDifferences).To(ContainElement("workout_prescription"))
		current.WorkoutPrescription = "Grace"
		current.Technique = "unspecified"
		out = Comparisons([]Record{current, base})
		Expect(out[0].Kind).To(Equal("reference"))
		Expect(out[0].ConditionDifferences).To(ContainElement("unknown_technique"))
	})
	It("separates equal-rep heavier load from speed and never compares unknown workout time", func() {
		day := time.Now()
		next := day.Add(time.Hour)
		load := &Load{Value: 60, Unit: "kg", Basis: "total", Status: "confirmed"}
		base := Record{ID: 1, SessionID: "old", Status: "confirmed", Movement: "Power Clean", Load: load, LoadKG: Kilograms(load), Start: 0, End: 60, Reps: 3, RepsPerMinute: 3, Measurement: "window", Technique: "singles", WorkoutPrescription: "Grace", PerformedAt: &day}
		current := base
		current.ID = 2
		current.PerformedAt = &next
		current.Load = &Load{Value: 65, Unit: "kg", Basis: "total"}
		current.LoadKG = Kilograms(current.Load)
		out := Comparisons([]Record{current, base})
		Expect(out[0].RateChangePercent).To(BeNil())
		Expect(*out[0].HeavierLoadKG).To(Equal(5.0))
		current.Load.Basis = "per_implement"
		out = Comparisons([]Record{current, base})
		Expect(out[0].HeavierLoadKG).To(BeNil())
		Expect(out[0].ConditionDifferences).To(ContainElement("load_basis"))
		current.PerformedAt = nil
		Expect(Comparisons([]Record{current, base})).To(BeEmpty())
	})
	It("retains heavier-load progress even when a same-load pace baseline exists", func() {
		day := time.Now()
		later := day.Add(time.Hour)
		latest := later.Add(time.Hour)
		light := &Load{Value: 60, Unit: "kg", Basis: "total", Status: "confirmed"}
		heavy := &Load{Value: 65, Unit: "kg", Basis: "total", Status: "confirmed"}
		base := Record{ID: 1, SessionID: "light", Status: "confirmed", Movement: "Power Clean", Load: light, LoadKG: Kilograms(light), Start: 0, End: 60, Reps: 5, RepsPerMinute: 5, Measurement: "window", Technique: "singles", WorkoutPrescription: "Grace", PerformedAt: &day}
		pace := base
		pace.ID = 2
		pace.SessionID = "heavy-prior"
		pace.Load = heavy
		pace.LoadKG = Kilograms(heavy)
		pace.Reps = 3
		pace.RepsPerMinute = 3
		pace.PerformedAt = &later
		current := pace
		current.ID = 3
		current.SessionID = "current"
		current.Reps = 5
		current.RepsPerMinute = 5
		current.PerformedAt = &latest
		out := Comparisons([]Record{current, pace, base})
		Expect(out[0].BaselineRecordID).To(Equal(uint(2)))
		Expect(out[0].Kind).To(Equal("direct"))
		Expect(*out[0].HeavierLoadKG).To(Equal(5.0))
		Expect(*out[0].HeavierLoadBaselineRecordID).To(Equal(uint(1)))
		Expect(*out[0].RateChangePercent).To(BeNumerically("~", 66.666666, 0.0001))
	})
	It("directly compares faster equal-rep sets while different rep sets remain reference", func() {
		day := time.Now()
		next := day.Add(time.Hour)
		load := &Load{Value: 60, Unit: "kg", Basis: "total", Status: "confirmed"}
		base := Record{ID: 1, SessionID: "old", Status: "confirmed", Movement: "Power Clean", Load: load, LoadKG: Kilograms(load), Start: 0, End: 60, ElapsedSeconds: 60, Reps: 5, RepsPerMinute: 5, Measurement: "set", Technique: "singles", WorkoutPrescription: "5 power cleans", PerformedAt: &day}
		current := base
		current.ID = 2
		current.SessionID = "new"
		current.PerformedAt = &next
		current.End = 30
		current.ElapsedSeconds = 30
		current.RepsPerMinute = 10
		out := Comparisons([]Record{current, base})
		Expect(out[0].Kind).To(Equal("direct"))
		Expect(*out[0].RateChangePercent).To(Equal(100.0))
		current.Load = &Load{Value: 65, Unit: "kg", Basis: "total"}
		current.LoadKG = Kilograms(current.Load)
		out = Comparisons([]Record{current, base})
		Expect(*out[0].HeavierLoadKG).To(Equal(5.0))
		Expect(out[0].RateChangePercent).To(BeNil())
		current.Reps = 6
		out = Comparisons([]Record{current, base})
		Expect(out[0].Kind).To(Equal("reference"))
		Expect(out[0].ConditionDifferences).To(ContainElement("reps"))
		Expect(out[0].HeavierLoadKG).To(BeNil())
	})
	It("confirms a continuous set split across 10-second segment boundaries, capture mappings, and unanalyzed tail segments", func() {
		p := Build(
			[]Interval{
				exercise(0, 9, cleanCycle(1, 3), cleanCycle(5, 7)),
				exercise(11, 20, cleanCycle(12, 14), cleanCycle(16, 18)),
			},
			[]Gap{{Start: 9, End: 11, Clock: "media", Reason: "incomplete_cycle", Movement: "Power Clean"}},
			"video",
		)
		r, err := Confirm(p, Confirmation{
			IntervalID:  p.Intervals[0].ID,
			Start:       0,
			End:         20,
			Reps:        10,
			Load:        &Load{Value: 135, Unit: "lb", Basis: "total"},
			Measurement: "set",
			Technique:   "touch_and_go",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.ElapsedSeconds).To(Equal(20.0))
		Expect(r.Reps).To(Equal(10))
		Expect(r.RepsPerMinute).To(Equal(30.0))
		Expect(r.Measurement).To(Equal("set"))

		capStart, capEnd := 113.0, 120.0
		seg := exercise(113, 120, cleanCycle(114, 116), cleanCycle(117, 119))
		seg.CaptureStart = &capStart
		seg.CaptureEnd = &capEnd
		tailProgress := Build(
			[]Interval{seg},
			[]Gap{{Start: 120, End: 130, Clock: "media", Reason: "not_analyzed"}},
			"video-113-125",
		)
		r2, err := Confirm(tailProgress, Confirmation{
			IntervalID:  tailProgress.Intervals[0].ID,
			Start:       113,
			End:         125,
			Reps:        10,
			Load:        &Load{Value: 135, Unit: "lb", Basis: "total"},
			Measurement: "set",
			Technique:   "unspecified",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.ElapsedSeconds).To(Equal(12.0))
		Expect(r2.Reps).To(Equal(10))
		Expect(r2.RepsPerMinute).To(Equal(50.0))
	})
	It("confirms a merged contiguous set across two movements under whichever movement interval ID is selected", func() {
		sc := Interval{Start: 10, End: 13, Activity: "exercise", Movement: "Squat Clean", ObservationRefs: []string{"chunk:1"}, RepEvents: []activity.Observation{cleanCycle(10, 13)}}
		fs := Interval{Start: 13, End: 25, Activity: "exercise", Movement: "Front Squat", ObservationRefs: []string{"chunk:2"}, RepEvents: []activity.Observation{cleanCycle(13, 16), cleanCycle(16, 19), cleanCycle(19, 22), cleanCycle(22, 25)}}
		p := Build([]Interval{sc, fs}, nil, "video-sc-fs")

		asFrontSquat, err := Confirm(p, Confirmation{
			IntervalID:  p.Intervals[1].ID,
			Start:       10,
			End:         25,
			Reps:        5,
			Load:        &Load{Value: 135, Unit: "lb", Basis: "total"},
			Measurement: "set",
			Technique:   "singles",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(asFrontSquat.Movement).To(Equal("Front Squat"))
		Expect(asFrontSquat.Start).To(Equal(10.0))
		Expect(asFrontSquat.End).To(Equal(25.0))
		Expect(asFrontSquat.ElapsedSeconds).To(Equal(15.0))
		Expect(asFrontSquat.Reps).To(Equal(5))

		asSquatClean, err := Confirm(p, Confirmation{
			IntervalID:  p.Intervals[0].ID,
			Start:       10,
			End:         25,
			Reps:        5,
			Load:        &Load{Value: 135, Unit: "lb", Basis: "total"},
			Measurement: "set",
			Technique:   "singles",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(asSquatClean.Movement).To(Equal("Squat Clean"))
		Expect(asSquatClean.ElapsedSeconds).To(Equal(15.0))
	})
})

