package workoutprogress

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ComparisonFor", func() {
	recordAt := func(id uint, at time.Time, load, rate float64) Record {
		return Record{ID: id, SessionID: "session", Status: "confirmed", Outcome: "completed", Movement: "Power Clean", Start: 0, End: 60, ElapsedSeconds: 60, Reps: IntPtr(10), RepsPerMinute: Float64Ptr(rate), Load: &Load{Value: load, Unit: "kg", Basis: "total", Status: "confirmed"}, LoadKG: Float64Ptr(load), Measurement: "window", Technique: "singles", WorkoutPrescription: "Grace", PerformedAt: &at}
	}

	It("prefers the latest exact baseline over a newer reference and retains an independent lower-load baseline", func() {
		now := time.Now().UTC()
		current := recordAt(5, now, 70, 10)
		oldDirect := recordAt(1, now.Add(-4*time.Hour), 70, 4)
		direct := recordAt(2, now.Add(-3*time.Hour), 70, 5)
		lighter := recordAt(3, now.Add(-2*time.Hour), 60, 8)
		newReference := recordAt(4, now.Add(-time.Hour), 80, 9)
		history := []Record{oldDirect, direct, lighter, newReference}
		comparison, ok := ComparisonFor(current, history)
		Expect(ok).To(BeTrue())
		Expect(comparison.Kind).To(Equal("direct"))
		Expect(comparison.BaselineRecordID).To(Equal(direct.ID))
		Expect(comparison.RateChangePercent).To(HaveValue(Equal(100.0)))
		Expect(comparison.HeavierLoadBaselineRecordID).To(HaveValue(Equal(lighter.ID)))
		Expect(comparison.HeavierLoadKG).To(HaveValue(Equal(10.0)))
		Expect(history[0]).To(Equal(oldDirect), "comparison must not mutate source records")
		all := append(append([]Record{}, history...), current)
		results := Comparisons(all)
		Expect(results[len(results)-1]).To(Equal(comparison), "the existing bulk API keeps the one-record rules")
	})

	It("reports changed conditions without a pace gain and never substitutes confirmation time for workout time", func() {
		now := time.Now().UTC()
		current := recordAt(2, now, 70, 10)
		prior := recordAt(1, now.Add(-time.Hour), 60, 5)
		comparison, ok := ComparisonFor(current, []Record{prior})
		Expect(ok).To(BeTrue())
		Expect(comparison.Kind).To(Equal("reference"))
		Expect(comparison.ConditionDifferences).To(ContainElement("load"))
		Expect(comparison.RateChangePercent).To(BeNil())
		current.PerformedAt = nil
		current.ConfirmedAt = now
		_, ok = ComparisonFor(current, []Record{prior})
		Expect(ok).To(BeFalse())
		current.PerformedAt = &now
		current.Reps = nil
		_, ok = ComparisonFor(current, []Record{prior})
		Expect(ok).To(BeFalse())
	})
})
