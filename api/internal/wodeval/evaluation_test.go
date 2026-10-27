package wodeval

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("WOD training load", func() {
	var location *time.Location
	BeforeEach(func() {
		var err error
		location, err = time.LoadLocation("Asia/Seoul")
		Expect(err).NotTo(HaveOccurred())
	})
	number := func(v float64) *float64 { return &v }
	integer := func(v int) *int { return &v }
	window := func(start, end float64) Start {
		return Start{Seconds: number(start), EndSeconds: number(end), Status: "confirmed"}
	}
	It("excludes prefix waiting while preserving in-WOD rest and fractional minutes", func() {
		a := Workout{Effort: integer(8), Start: window(120, 1320)}
		b := Workout{Effort: integer(8), Start: window(600, 1800)}
		Expect(*SessionLoad(a)).To(Equal(160.0))
		Expect(*SessionLoad(b)).To(Equal(160.0))
		a.Start.EndSeconds = number(1350)
		Expect(*SessionLoad(a)).To(Equal(164.0))
		captureElapsed := 1260.0
		a.Start.ElapsedSeconds = &captureElapsed
		Expect(*SessionLoad(a)).To(Equal(168.0))
	})
	It("withholds unknown duration and absent effort rather than substituting zero", func() {
		Expect(SessionLoad(Workout{Start: window(0, 1200)})).To(BeNil())
		Expect(SessionLoad(Workout{Effort: integer(8), Start: Start{Status: "unresolved"}})).To(BeNil())
		Expect(SessionLoad(Workout{Effort: integer(11), Start: window(0, 1200)})).To(BeNil())
	})
	It("extracts confirmed WOD DurationSeconds preferring elapsed_seconds and returns nil when unresolved", func() {
		Expect(DurationSeconds(Start{Status: "unresolved"})).To(BeNil())
		w := window(120, 660)
		Expect(DurationSeconds(w)).NotTo(BeNil())
		Expect(*DurationSeconds(w)).To(Equal(540.0))
		elapsed := 515.0
		w.ElapsedSeconds = &elapsed
		Expect(*DurationSeconds(w)).To(Equal(515.0))
		zero := 0.0
		w.ElapsedSeconds = &zero
		Expect(DurationSeconds(w)).To(BeNil())
	})
	It("uses overlapping calendar windows including today and excludes future workouts", func() {
		now := time.Date(2026, 10, 13, 12, 0, 0, 0, location)
		entries := []Workout{{At: now, Effort: integer(8), Start: window(0, 1200)}, {At: now.AddDate(0, 0, -7), Effort: integer(8), Start: window(0, 1200)}, {At: now.AddDate(0, 0, -28), Effort: integer(8), Start: window(0, 1200)}, {At: now.Add(time.Hour), Effort: integer(8), Start: window(0, 1200)}}
		c := Compare(entries, now, now.AddDate(0, 0, -40), location)
		Expect(c.Seven.Total).To(Equal(160.0))
		Expect(c.TwentyEight.Total).To(Equal(320.0))
		Expect(*c.ChangePercent).To(BeNumerically("~", 100, 1e-9))
	})
	It("withholds comparisons for immature, empty, or incomplete baselines", func() {
		now := time.Now()
		entry := Workout{At: now, Effort: integer(8), Start: window(0, 1200)}
		Expect(Compare([]Workout{entry}, now, now, location).Status).To(Equal("collecting_baseline"))
		Expect(Compare(nil, now, now.AddDate(0, 0, -40), location).Status).To(Equal("no_baseline"))
		entry.Effort = nil
		c := Compare([]Workout{entry}, now, now.AddDate(0, 0, -40), location)
		Expect(c.ChangePercent).To(BeNil())
		Expect(c.TwentyEight.MissingEffort).To(Equal(1))
		Expect(c.Status).To(Equal("incomplete_records"))
	})
	It("recalculates after effort edits without changing the workout duration", func() {
		entry := Workout{Effort: integer(4), Start: window(120, 1320)}
		Expect(*SessionLoad(entry)).To(Equal(80.0))
		entry.Effort = integer(9)
		Expect(*SessionLoad(entry)).To(Equal(180.0))
	})
	It("normalizes score weights and keeps missing intensity null", func() {
		s := Score{Form: integer(80), Intensity: integer(60), Consistency: integer(90), WODStart: window(10, 100)}
		Normalize(&s, false)
		Expect(*s.Overall).To(Equal(76))
		Normalize(&s, true)
		Expect(*s.Overall).To(Equal(79))
		s.Intensity = nil
		Normalize(&s, false)
		Expect(s.Overall).To(BeNil())
		s.Intensity = integer(101)
		Normalize(&s, false)
		Expect(s.Intensity).To(BeNil())
		s.Intensity = integer(0)
		Normalize(&s, false)
		Expect(s.Intensity).NotTo(BeNil())
		Expect(*s.Intensity).To(Equal(0))
	})
})
