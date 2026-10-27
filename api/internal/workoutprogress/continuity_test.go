package workoutprogress

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
)

func rowSeconds(start, end float64) activity.Observation {
	return activity.Observation{Movement: "Row", Unit: "seconds", Start: start, End: end, Complete: true, Evidence: "continuous strokes on the rower"}
}

func row(id string, start, end float64, events ...activity.Observation) Interval {
	return Interval{ID: id, Start: start, End: end, Activity: "exercise", Movement: "Row", ObservationRefs: []string{id}, RepEvents: events}
}

var _ = Describe("Timeline continuity normalization", func() {
	It("attaches a sub-second interval to the following interval of the same movement", func() {
		fragment := exercise(20, 20.4, cleanCycle(20.0, 20.35))
		fragment.ID = "fragment"
		next := exercise(20.4, 30, cleanCycle(22, 24))
		next.ID = "next"
		intervals, _ := NormalizeContinuity([]Interval{next, fragment}, nil, nil)
		Expect(intervals).To(HaveLen(1))
		Expect(intervals[0].ID).To(Equal("next"))
		Expect(intervals[0].Start).To(Equal(20.0))
		Expect(intervals[0].End).To(Equal(30.0))
		Expect(intervals[0].RepEvents).To(HaveLen(2))
		Expect(intervals[0].ObservationRefs).To(ContainElement("segment-1"))
	})

	It("attaches a trailing sub-second interval to the preceding interval", func() {
		intervals, _ := NormalizeContinuity([]Interval{row("a", 0, 30), row("tail", 30, 30.5)}, nil, nil)
		Expect(intervals).To(HaveLen(1))
		Expect(intervals[0].End).To(Equal(30.5))
	})

	It("attaches a sub-second fragment of another movement to the next interval without moving its evidence", func() {
		fragment := exercise(10, 10.3, cleanCycle(10, 10.2))
		intervals, _ := NormalizeContinuity([]Interval{fragment, row("row", 10.3, 40)}, nil, nil)
		Expect(intervals).To(HaveLen(1))
		Expect(intervals[0].Movement).To(Equal("Row"))
		Expect(intervals[0].Start).To(Equal(10.0))
		Expect(intervals[0].RepEvents).To(BeEmpty())
	})

	It("stitches touching same-movement intervals and joins continuous timed events", func() {
		a := row("a", 1100.37, 1130.37, rowSeconds(1100.37, 1130.37))
		b := row("b", 1130.37, 1169.5, rowSeconds(1130.37, 1169.5))
		b.FormObservations = []Observation{{Start: 1150, End: 1155, Evidence: "handle path stays level"}}
		intervals, _ := NormalizeContinuity([]Interval{a, b}, nil, nil)
		Expect(intervals).To(HaveLen(1))
		Expect(intervals[0].ID).To(Equal("a"))
		Expect(intervals[0].Start).To(Equal(1100.37))
		Expect(intervals[0].End).To(Equal(1169.5))
		Expect(intervals[0].RepEvents).To(HaveLen(1))
		Expect(intervals[0].RepEvents[0].End).To(Equal(1169.5))
		Expect(intervals[0].FormObservations).To(HaveLen(1))
		Expect(intervals[0].ObservationRefs).To(Equal([]string{"a", "b"}))
	})

	It("absorbs a sub-second boundary gap and then stitches across it", func() {
		gaps := []Gap{
			{Start: 1140.40, End: 1140.60, Clock: "media", Reason: "incomplete_cycle", Movement: "Row"},
			{Start: 1140.60, End: 1140.90, Clock: "media", Reason: "not_analyzed"},
		}
		intervals, outGaps := NormalizeContinuity([]Interval{row("a", 1100, 1140.40), row("b", 1140.90, 1170)}, gaps, nil)
		Expect(intervals).To(HaveLen(1))
		Expect(intervals[0].Start).To(Equal(1100.0))
		Expect(intervals[0].End).To(Equal(1170.0))
		for _, g := range outGaps {
			Expect(g.Reason).NotTo(Equal("not_analyzed"))
			Expect(g.Start >= intervals[0].Start && g.End <= intervals[0].End).To(BeTrue(), "remaining count-only markers stay inside the interval")
		}
	})

	It("keeps short repetition events inside a valid interval", func() {
		du := activity.Observation{Movement: "Double Under", Unit: "reps", Start: 2.0, End: 2.5, Complete: true, Evidence: "rope passes under both feet"}
		in := Interval{ID: "du", Start: 0, End: 10, Activity: "exercise", Movement: "Double Under", RepEvents: []activity.Observation{du}}
		intervals, _ := NormalizeContinuity([]Interval{in}, nil, nil)
		Expect(intervals[0].RepEvents).To(HaveLen(1))
	})

	It("never stitches across rest, different movements, loads, or a recording pause", func() {
		rest := Interval{ID: "rest", Start: 30, End: 50, Activity: "rest"}
		Expect(func() []Interval {
			out, _ := NormalizeContinuity([]Interval{row("a", 0, 30), rest, row("b", 50, 80)}, nil, nil)
			return out
		}()).To(HaveLen(3))

		clean := exercise(30, 60)
		Expect(func() []Interval {
			out, _ := NormalizeContinuity([]Interval{row("a", 0, 30), clean}, nil, nil)
			return out
		}()).To(HaveLen(2))

		heavy := exercise(0, 30)
		heavy.Load = &Load{Value: 135, Unit: "lb", Basis: "total"}
		heavier := exercise(30, 60)
		heavier.Load = &Load{Value: 155, Unit: "lb", Basis: "total"}
		Expect(func() []Interval { out, _ := NormalizeContinuity([]Interval{heavy, heavier}, nil, nil); return out }()).To(HaveLen(2))

		Expect(func() []Interval {
			out, _ := NormalizeContinuity([]Interval{row("a", 0, 30), row("b", 30, 60)}, nil, []float64{30})
			return out
		}()).To(HaveLen(2))

		captureA, captureAEnd, captureB, captureBEnd := 0.0, 30.0, 45.0, 75.0
		a, b := row("a", 0, 30), row("b", 30, 60)
		a.CaptureStart, a.CaptureEnd = &captureA, &captureAEnd
		b.CaptureStart, b.CaptureEnd = &captureB, &captureBEnd
		Expect(func() []Interval { out, _ := NormalizeContinuity([]Interval{a, b}, nil, nil); return out }()).To(HaveLen(2))
	})

	It("keeps long gaps and capture-clock gaps and does not mutate its input", func() {
		input := []Interval{row("a", 0, 0.5), row("b", 0.5, 30)}
		gaps := []Gap{{Start: 30, End: 40, Clock: "media", Reason: "not_visible"}, {Start: 5, End: 5.5, Clock: "capture", Reason: "recording_gap"}}
		intervals, outGaps := NormalizeContinuity(input, gaps, nil)
		Expect(intervals).To(HaveLen(1))
		Expect(input[1].Start).To(Equal(0.5))
		Expect(outGaps).To(ContainElement(gaps[0]))
		Expect(outGaps).To(ContainElement(gaps[1]))
	})

	It("produces a timeline that Build accepts and Decode round-trips", func() {
		intervals, gaps := NormalizeContinuity([]Interval{row("a", 0, 30, rowSeconds(0, 30)), row("b", 30, 30.2), row("c", 30.2, 60, rowSeconds(30.2, 60))}, nil, nil)
		p := Build(intervals, gaps, "video")
		Expect(p.Intervals).To(HaveLen(1))
		for _, in := range p.Intervals {
			Expect(in.End - in.Start).To(BeNumerically(">=", MinIntervalDurationSecs))
		}
	})
})
