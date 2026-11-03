package workoutprogress

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func restTimingInterval(id string, start, end, captureStart, captureEnd float64) Interval {
	return Interval{ID: id, Start: start, End: end, Activity: "exercise", Movement: "Power Clean", CaptureStart: Float64Ptr(captureStart), CaptureEnd: Float64Ptr(captureEnd)}
}

func restTimingProgress(intervals []Interval, gaps ...Gap) Progress {
	return Progress{SourceVersion: "rest-timing-source", Intervals: intervals, Unassessed: gaps}
}

var _ = Describe("Rest confirmation timing", func() {
	DescribeTable("uses complete selected timing coverage without inferring clocks",
		func(progress Progress, start, end, wantElapsed float64, wantError bool) {
			before, err := json.Marshal(progress)
			Expect(err).NotTo(HaveOccurred())
			record, err := Confirm(progress, Confirmation{IntervalID: "selected", Outcome: "rest", Start: start, End: end})
			if wantError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(record.ElapsedSeconds).To(BeNumerically("~", wantElapsed, 1e-6))
				Expect(record.Outcome).To(Equal("rest"))
				Expect(record.Measurement).To(Equal("set"))
				Expect(record.Technique).To(Equal("unspecified"))
				Expect(record.Movement).To(BeEmpty())
				Expect(record.Reps).To(BeNil())
				Expect(record.RepsPerMinute).To(BeNil())
				Expect(record.RateBasis).To(BeNil())
				Expect(record.Load).To(BeNil())
			}
			after, err := json.Marshal(progress)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		},
		Entry("single mapped interval with verified partial bounds",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 100, 110)}), 2.0, 8.0, 6.0, false),
		Entry("all mapped intervals contribute their actual capture duration",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 20), restTimingInterval("next", 10, 20, 20, 40)}), 0.0, 20.0, 40.0, false),
		Entry("continuous mapped partial first and last spans",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 100, 110), restTimingInterval("next", 10, 20, 110, 120)}), 5.0, 15.0, 10.0, false),
		Entry("overlapping count gap does not split a whole mapped interval",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 20)}, Gap{Start: 4, End: 6, Clock: "media", Reason: "incomplete_cycle"}), 0.0, 10.0, 20.0, false),
		Entry("unmapped exercise and explicit unassessed coverage can be corrected together",
			restTimingProgress([]Interval{{ID: "selected", Start: 0, End: 10}, {ID: "next", Start: 20, End: 30}}, Gap{Start: 10, End: 20, Clock: "media", Reason: "not_visible"}), 0.0, 30.0, 30.0, false),
		Entry("synthetic unassessed selection uses the union of sorted overlapping gaps",
			restTimingProgress(nil, Gap{Start: 8, End: 20, Clock: "media", Reason: "not_visible"}, Gap{Start: 0, End: 10, Clock: "media", Reason: "not_analyzed"}), 0.0, 20.0, 20.0, false),
		Entry("an unverified mapping outside the selection does not block it",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 10)}, Gap{Start: 10, End: 20, Clock: "media", Reason: "unverified_capture_mapping"}), 0.0, 10.0, 10.0, false),
		Entry("an uncovered hole cannot become rest",
			restTimingProgress([]Interval{{ID: "selected", Start: 0, End: 10}, {ID: "next", Start: 20, End: 30}}), 0.0, 30.0, 0.0, true),
		Entry("selection beyond known media coverage is rejected",
			restTimingProgress([]Interval{{ID: "selected", Start: 10, End: 20}}), 10.0, 25.0, 0.0, true),
		Entry("selection before known media coverage is rejected",
			restTimingProgress([]Interval{{ID: "selected", Start: 10, End: 20}}), 5.0, 20.0, 0.0, true),
		Entry("a wholly uncovered synthetic selection is rejected",
			restTimingProgress(nil), 0.0, 10.0, 0.0, true),
		Entry("unverified mapping overlapping an actual interval is rejected",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 10)}, Gap{Start: 4, End: 6, Clock: "media", Reason: "unverified_capture_mapping"}), 0.0, 10.0, 0.0, true),
		Entry("mixed mapped and unmapped intervals are rejected",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 10), {ID: "next", Start: 10, End: 20}}), 0.0, 20.0, 0.0, true),
		Entry("synthetic gap coverage cannot inherit the adjacent capture clock",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 10)}, Gap{Start: 10, End: 20, Clock: "media", Reason: "not_visible"}), 0.0, 20.0, 0.0, true),
		Entry("discontinuous capture endpoints are rejected",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 10), restTimingInterval("next", 10, 20, 20, 30)}), 0.0, 20.0, 0.0, true),
		Entry("an explicit capture gap cannot be classified as recorded rest",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 10)}, Gap{Start: 4, End: 6, Clock: "capture", Reason: "recording_gap"}), 0.0, 10.0, 0.0, true),
		Entry("unmapped media cannot establish whether a capture gap is crossed",
			restTimingProgress([]Interval{{ID: "selected", Start: 0, End: 10}}, Gap{Start: 20, End: 30, Clock: "capture", Reason: "recording_gap"}), 0.0, 10.0, 0.0, true),
		Entry("partial selection of a scaled mapping is ambiguous",
			restTimingProgress([]Interval{restTimingInterval("selected", 0, 10, 0, 20)}), 0.0, 5.0, 0.0, true),
		Entry("an incomplete capture endpoint pair is rejected",
			restTimingProgress([]Interval{{ID: "selected", Start: 0, End: 10, CaptureStart: Float64Ptr(0)}}), 0.0, 10.0, 0.0, true),
	)

	It("ignores source activity, movement and load when reclassifying mapped coverage", func() {
		first := restTimingInterval("selected", 0, 10, 100, 110)
		first.Load = &Load{Value: 60, Unit: "kg", Basis: "total"}
		second := restTimingInterval("next", 10, 20, 110, 120)
		second.Movement = "Burpee"
		third := restTimingInterval("last", 20, 30, 120, 130)
		third.Activity, third.Movement = "unknown", ""
		record, err := Confirm(restTimingProgress([]Interval{first, second, third}), Confirmation{IntervalID: "selected", Outcome: "rest", Start: 0, End: 30})
		Expect(err).NotTo(HaveOccurred())
		Expect(record.ElapsedSeconds).To(Equal(30.0))
		Expect(record.Load).To(BeNil())
	})

	It("still requires overlap with an existing selected interval", func() {
		progress := restTimingProgress([]Interval{{ID: "selected", Start: 0, End: 10}, {ID: "other", Start: 10, End: 20}})
		_, err := Confirm(progress, Confirmation{IntervalID: "selected", Outcome: "rest", Start: 10, End: 20})
		Expect(err).To(MatchError("window must overlap its selected exercise interval"))
	})
})
