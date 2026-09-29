package activity

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

func observationJSON(events ...Observation) db.NullableJSONDocument {
	raw, err := json.Marshal(Observations{DurationSecs: 10, Version: 1, TargetState: "identified", ActivityState: "exercise", Events: events, Unassessed: []Interval{}})
	Expect(err).NotTo(HaveOccurred())
	return raw
}

func cycle(start, end float64) Observation {
	return Observation{Movement: "Air Squat", Unit: "reps", Start: start, End: end, Complete: true, Evidence: "descends and stands"}
}
func source(id uint, start, end float64) db.ChunkAnalysisResult {
	return db.ChunkAnalysisResult{ID: id, ProfileID: 1, SessionID: "s", FilePath: string(rune('a' + id)), Status: "COMPLETED", StartSecs: &start, EndSecs: &end, MediaStartSecs: &start, MediaEndSecs: &end, MovementObservations: observationJSON(cycle(1, 3))}
}

var _ = Describe("Movement evidence and accumulation", func() {
	It("assigns a boundary completion once and clips timed activity", func() {
		doc, err := Decode(observationJSON(cycle(8, 10), cycle(11, 13)), 0, 20)
		Expect(err).NotTo(HaveOccurred())
		Expect(Own(doc, 0, 10).Events).To(HaveLen(1))
		Expect(Own(doc, 10, 20).Events).To(HaveLen(1))
		timed, err := Decode(observationJSON(Observation{"Run", "seconds", 2, 18, true, "continuous running"}), 0, 20)
		Expect(err).NotTo(HaveOccurred())
		Expect(Own(timed, 0, 10).Events[0].End).To(Equal(10.0))
		Expect(Own(timed, 10, 20).Events[0].Start).To(Equal(10.0))
	})
	It("never counts a partial cycle or supplies a default estimate", func() {
		partial := cycle(8, 10)
		partial.Complete = false
		doc, err := Decode(observationJSON(partial), 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Events).To(BeEmpty())
		Expect(doc.Unassessed).To(HaveLen(1))
		row := source(1, 0, 10)
		row.MovementObservations = nil
		row.ObservedSignals = `{"movement":"Air Squat","rep_count":99}`
		summary := Build([]db.ChunkAnalysisResult{row}, nil)
		Expect(summary.Available).To(BeFalse())
		Expect(summary.Movements).To(BeEmpty())
	})
	DescribeTable("rejects unsafe evidence", func(raw string) {
		doc, err := Decode([]byte(raw), 0, 10)
		Expect(err).To(HaveOccurred())
		Expect(doc.Events).To(BeEmpty())
	},
		Entry("malformed", "not json"),
		Entry("missing arrays", `{"version":1,"target_state":"identified","activity_state":"exercise"}`),
		Entry("negative interval", `{"version":1,"target_state":"identified","activity_state":"exercise","events":[{"movement":"Air Squat","unit":"reps","start_secs":-1,"end_secs":2,"complete":true,"evidence":"squat"}],"unassessed":[]}`),
	)
	It("does not count overlapping events, unknown movements or unobservable repetitions", func() {
		_, err := Decode(observationJSON(cycle(1, 4), cycle(3, 6)), 0, 10)
		Expect(err).To(HaveOccurred())
		event := cycle(1, 3)
		event.Movement = "Invented Squat"
		doc, err := Decode(observationJSON(event), 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Events).To(BeEmpty())
		Expect(doc.Unassessed).NotTo(BeEmpty())
		doc, err = Decode([]byte(`{"version":1,"target_state":"ambiguous","activity_state":"exercise","events":[],"unassessed":[]}`), 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Unassessed).NotTo(BeEmpty())
	})
	It("deduplicates retries and includes observations older than the coaching window", func() {
		first, last := source(1, 0, 10), source(2, 70, 80)
		duplicate := first
		duplicate.ID = 3
		summary := Build([]db.ChunkAnalysisResult{last, duplicate, first}, nil)
		Expect(summary.Movements).To(HaveLen(1))
		Expect(summary.Movements[0].Count).To(Equal(2))
		Expect(summary.Unassessed).To(HaveLen(1))
		Expect(summary.SourceVersion).To(Equal(Build([]db.ChunkAnalysisResult{first, last}, nil).SourceVersion))
	})
	It("keeps distinct variants and multiple movements in one chunk", func() {
		a, b := cycle(1, 3), cycle(4, 6)
		a.Movement = "Power Clean"
		b.Movement = "Squat Clean"
		row := source(1, 0, 10)
		row.MovementObservations = observationJSON(a, b)
		summary := Build([]db.ChunkAnalysisResult{row}, nil)
		Expect(summary.Movements).To(HaveLen(2))
	})
	It("replaces provisional counts and ignores reviews of superseded sources", func() {
		row := source(1, 0, 10)
		rows := []db.ChunkAnalysisResult{row}
		stored := Build(rows, nil)
		doc, err := Decode(observationJSON(cycle(1, 3), cycle(4, 6)), 0, 10)
		Expect(err).NotTo(HaveOccurred())
		stored.ReviewState = "completed"
		stored.Reviews = []ReviewedChunk{{row.ID, "completed", doc}}
		Expect(Build(rows, &stored).Movements[0].Count).To(Equal(2))
		Expect(Build(rows, &stored).Movements[0].Count).To(Equal(2))
		rows[0].MovementObservations = observationJSON(cycle(2, 4))
		Expect(Build(rows, &stored).ReviewState).To(Equal("provisional"))
		Expect(Build(rows, &stored).Movements[0].Count).To(Equal(1))
	})
	It("does not count conflicting sources or bridge capture downtime", func() {
		first, second := source(1, 0, 10), source(2, 9, 20)
		Expect(Build([]db.ChunkAnalysisResult{first, second}, nil).Movements).To(BeEmpty())
		start := 11.0
		second.StartSecs = &start
		media := 10.0
		second.MediaStartSecs = &media
		Expect(Adjacent(first, second)).To(BeFalse())
	})
})
