package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("Pass 1 chunk continuity", func() {
	f := func(v float64) *float64 { return &v }
	// chunkAt builds a verified 10-second chunk at index i with continuous clocks.
	chunkAt := func(i int, movement, signals, observations string) db.ChunkAnalysisResult {
		start := 1100.37 + float64(i)*10
		row := db.ChunkAnalysisResult{
			Status: "COMPLETED", ExerciseType: movement, ObservedSignals: signals, Output: movement,
			MediaStartSecs: f(start), MediaEndSecs: f(start + 10), StartSecs: f(start), EndSecs: f(start + 10),
		}
		if observations != "" {
			row.MovementObservations = db.NullableJSONDocument(observations)
		}
		return row
	}
	rowing := func(i int) db.ChunkAnalysisResult {
		return chunkAt(i, "Row", `{"movement":"Row","activity_state":"exercise"}`, "")
	}
	// The exact false rest chunk of WOD-20261026-01M5TWZSMV81CW4DPHQY10DW75.
	falseRest := func(i int) db.ChunkAnalysisResult {
		return chunkAt(i, "", `{}`, `{"version":1,"target_state":"identified","activity_state":"rest","events":[],"unassessed":[]}`)
	}
	build := func(rows ...db.ChunkAnalysisResult) ([]Segment, []Segment) {
		timeline := make([]timelineChunk, 0, len(rows))
		for _, row := range rows {
			timeline = append(timeline, newTimelineChunk(row, WorkoutTypeWOD))
		}
		return buildContinuousSegments(timeline)
	}

	It("classifies explicit rest from movement observations instead of leaving it unanalyzed", func() {
		class, _ := classifyChunkActivity(falseRest(0), WorkoutTypeWOD)
		Expect(class).To(Equal(chunkExplicitRest))
		class, _ = classifyChunkActivity(chunkAt(0, "", `{}`, ""), WorkoutTypeWOD)
		Expect(class).To(Equal(chunkUnclassified), "an empty response is neither exercise nor rest")
	})

	It("observes an unlabelled chunk with visible activity as Unknown", func() {
		class, movement := classifyChunkActivity(chunkAt(0, "", `{"activity_state":"exercise"}`, ""), WorkoutTypeWOD)
		Expect(class).To(Equal(chunkActive))
		Expect(movement).To(Equal("Unknown"))
		class, movement = classifyChunkActivity(chunkAt(0, "", `{}`, `{"version":1,"target_state":"identified","activity_state":"rest","events":[{"movement":"Air Squat","unit":"reps","start_secs":1,"end_secs":3,"complete":true,"evidence":"stands"}],"unassessed":[]}`), WorkoutTypeWOD)
		Expect(class).To(Equal(chunkActive), "visible cycles outrank a contradictory rest label")
		Expect(movement).To(Equal("Air Squat"))
	})

	It("bridges a single false rest chunk between continuous rowing into one bout", func() {
		segments, skipped := build(rowing(0), rowing(1), rowing(2), falseRest(3), rowing(4), rowing(5), rowing(6))
		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Start).To(Equal("18:20.37"))
		Expect(segments[0].End).To(Equal("19:30.37"))
		Expect(segments[0].Type).To(Equal("Row"))
		Expect(skipped).To(BeEmpty())
	})

	It("treats two consecutive rest chunks as real rest", func() {
		segments, skipped := build(rowing(0), falseRest(1), falseRest(2), rowing(3))
		Expect(segments).To(HaveLen(2))
		Expect(skipped).To(Equal([]Segment{{Start: "18:30.37", End: "18:50.37", Type: "Rest"}}))
		Expect(segments[0].End).To(Equal("18:30.37"), "skipped rest stays skipped; bouts are not padded into it")
		Expect(segments[1].Start).To(Equal("18:50.37"))
	})

	It("never cuts a continuous bout at a label change or label jitter", func() {
		thruster := func(i int) db.ChunkAnalysisResult {
			return chunkAt(i, "Thruster", `{"movement":"Thruster","activity_state":"exercise"}`, "")
		}
		segments, _ := build(thruster(0), chunkAt(1, "Unknown", `{"movement":"Unknown","activity_state":"unknown"}`, ""), thruster(2))
		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Type).To(Equal("Thruster"))

		burpee := chunkAt(3, "Burpee", `{"movement":"Burpee","activity_state":"exercise"}`, "")
		segments, _ = build(rowing(0), rowing(1), rowing(2), burpee, chunkAt(4, "Burpee", `{"movement":"Burpee","activity_state":"exercise"}`, ""))
		Expect(segments).To(HaveLen(1), "back-to-back movements without rest stay one observed span")
		Expect(segments[0].Type).To(Equal("Row"))
		Expect(segments[0].Description).To(HavePrefix("청크 후보 종목: Row, Burpee"))
	})

	It("keeps a single deviating label as a candidate when the chunk shows its own complete cycles", func() {
		wallBall := chunkAt(1, "Wall Ball", `{"movement":"Wall Ball","activity_state":"exercise"}`,
			`{"version":1,"target_state":"identified","activity_state":"exercise","events":[`+
				`{"movement":"Wall Ball","unit":"reps","start_secs":1,"end_secs":3,"complete":true,"evidence":"throw"},`+
				`{"movement":"Wall Ball","unit":"reps","start_secs":4,"end_secs":6,"complete":true,"evidence":"throw"}],"unassessed":[]}`)
		timeline := smoothChunkSequence([]timelineChunk{newTimelineChunk(rowing(0), WorkoutTypeWOD), newTimelineChunk(wallBall, WorkoutTypeWOD), newTimelineChunk(rowing(2), WorkoutTypeWOD)})
		Expect(timeline[1].Movement).To(Equal("Wall Ball"))

		oneRep := chunkAt(1, "Wall Ball", `{"movement":"Wall Ball","activity_state":"exercise"}`, "")
		timeline = smoothChunkSequence([]timelineChunk{newTimelineChunk(rowing(0), WorkoutTypeWOD), newTimelineChunk(oneRep, WorkoutTypeWOD), newTimelineChunk(rowing(2), WorkoutTypeWOD)})
		Expect(timeline[1].Movement).To(Equal("Row"))
		Expect(timeline[1].Candidates).To(ContainElements("Row", "Wall Ball"))
	})

	It("never bridges across a recording pause or a saved user correction", func() {
		paused := rowing(2)
		paused.StartSecs, paused.EndSecs = f(*paused.StartSecs+30), f(*paused.EndSecs+30)
		segments, _ := build(rowing(0), falseRest(1), paused)
		Expect(segments).To(HaveLen(2))

		timeline := []timelineChunk{newTimelineChunk(rowing(0), WorkoutTypeWOD), newTimelineChunk(falseRest(1), WorkoutTypeWOD), newTimelineChunk(rowing(2), WorkoutTypeWOD)}
		timeline[1].UserFixed = true
		segments, skipped := buildContinuousSegments(timeline)
		Expect(segments).To(HaveLen(2))
		Expect(skipped).To(HaveLen(1))
	})

	It("bridges an isolated unclassified chunk but leaves a run of them unanalyzed", func() {
		empty := func(i int) db.ChunkAnalysisResult { return chunkAt(i, "", `{}`, "") }
		segments, skipped := build(rowing(0), empty(1), rowing(2))
		Expect(segments).To(HaveLen(1))
		segments, skipped = build(rowing(0), empty(1), empty(2), rowing(3))
		Expect(segments).To(HaveLen(2))
		Expect(skipped).To(BeEmpty(), "unclassified media is not claimed as rest")
	})
})
