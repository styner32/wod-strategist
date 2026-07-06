package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("intervals", func() {
	Context("mergeFocusIntervals", func() {
		It("merges overlapping intervals and concatenates reasons", func() {
			entries := []FocusEntry{
				{Start: "0:30", End: "0:45", Reason: "어깨 보상"},
				{Start: "0:40", End: "0:55", Reason: "허리 과신전"},
			}
			merged := mergeFocusIntervals(entries)
			Expect(merged).To(HaveLen(1))
			Expect(merged[0].StartSecs).To(Equal(30.0))
			Expect(merged[0].EndSecs).To(Equal(55.0))
			Expect(merged[0].Reasons).To(Equal([]string{"어깨 보상", "허리 과신전"}))
		})

		It("merges near-adjacent intervals within gap tolerance (5.0s)", func() {
			entries := []FocusEntry{
				{Start: "0:30", End: "0:40", Reason: "A"},
				{Start: "0:43", End: "0:50", Reason: "B"}, // gap = 3s <= 5s
			}
			merged := mergeFocusIntervals(entries)
			Expect(merged).To(HaveLen(1))
			Expect(merged[0].StartSecs).To(Equal(30.0))
			Expect(merged[0].EndSecs).To(Equal(50.0))
			Expect(merged[0].Reasons).To(Equal([]string{"A", "B"}))
		})

		It("keeps disjoint intervals separate if gap exceeds tolerance", func() {
			entries := []FocusEntry{
				{Start: "0:30", End: "0:40", Reason: "A"},
				{Start: "0:46", End: "0:50", Reason: "B"}, // gap = 6s > 5s
			}
			merged := mergeFocusIntervals(entries)
			Expect(merged).To(HaveLen(2))
			Expect(merged[0].StartSecs).To(Equal(30.0))
			Expect(merged[0].EndSecs).To(Equal(40.0))
			Expect(merged[1].StartSecs).To(Equal(46.0))
			Expect(merged[1].EndSecs).To(Equal(50.0))
		})

		It("drops zero or negative length intervals", func() {
			entries := []FocusEntry{
				{Start: "0:30", End: "0:30", Reason: "Zero"},
				{Start: "0:40", End: "0:35", Reason: "Negative"},
				{Start: "0:10", End: "0:20", Reason: "Valid"},
			}
			merged := mergeFocusIntervals(entries)
			Expect(merged).To(HaveLen(1))
			Expect(merged[0].StartSecs).To(Equal(10.0))
			Expect(merged[0].EndSecs).To(Equal(20.0))
		})

		It("handles containment", func() {
			entries := []FocusEntry{
				{Start: "0:30", End: "1:00", Reason: "Outer"},
				{Start: "0:40", End: "0:50", Reason: "Inner"},
			}
			merged := mergeFocusIntervals(entries)
			Expect(merged).To(HaveLen(1))
			Expect(merged[0].StartSecs).To(Equal(30.0))
			Expect(merged[0].EndSecs).To(Equal(60.0))
			Expect(merged[0].Reasons).To(Equal([]string{"Outer", "Inner"}))
		})
	})

	Context("capFocusIntervals", func() {
		It("keeps the longest N intervals in chronological order", func() {
			intervals := []mergedInterval{
				{StartSecs: 10, EndSecs: 15, Reasons: []string{"Short1"}}, // len = 5
				{StartSecs: 30, EndSecs: 50, Reasons: []string{"Long"}},   // len = 20
				{StartSecs: 70, EndSecs: 80, Reasons: []string{"Mid"}},    // len = 10
				{StartSecs: 90, EndSecs: 92, Reasons: []string{"Short2"}}, // len = 2
			}
			// Keep top 2 longest: "Long" (20s) and "Mid" (10s)
			// Sorted chronologically: "Long" (30-50) then "Mid" (70-80)
			capped := capFocusIntervals(intervals, 2)
			Expect(capped).To(HaveLen(2))
			Expect(capped[0].StartSecs).To(Equal(30.0))
			Expect(capped[0].EndSecs).To(Equal(50.0))
			Expect(capped[1].StartSecs).To(Equal(70.0))
			Expect(capped[1].EndSecs).To(Equal(80.0))
		})

		It("returns original if size is less than or equal to max", func() {
			intervals := []mergedInterval{
				{StartSecs: 10, EndSecs: 15, Reasons: []string{"A"}},
			}
			capped := capFocusIntervals(intervals, 5)
			Expect(capped).To(Equal(intervals))
		})
	})
})
