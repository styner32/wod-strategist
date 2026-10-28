package recognitionreview_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/recognitionreview"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

func reviewProgress(intervals []workoutprogress.Interval, gaps []workoutprogress.Gap) workoutprogress.Progress {
	if gaps == nil {
		gaps = []workoutprogress.Gap{}
	}
	return workoutprogress.Progress{Version: 1, Clock: "media", SourceVersion: "source", Intervals: intervals, Unassessed: gaps}
}

func reviewRecord(id uint, start, end float64, movement string, reps int) workoutprogress.Record {
	return workoutprogress.Record{ID: id, Status: "confirmed", SourceVersion: "source", Start: start, End: end, Movement: movement, Reps: reps}
}

func reviewCycle(start, end float64, movement string) activity.Observation {
	return activity.Observation{Start: start, End: end, Movement: movement, Unit: "reps", Complete: true, Evidence: "visible cycle"}
}

var _ = Describe("Recognition evaluation", func() {
	It("compares corrected movement against all raw overlapping intervals and reports separate movement and count differences", func() {
		p := reviewProgress([]workoutprogress.Interval{{Start: 0, End: 20, Activity: "exercise", Movement: "Deadlift", RepEvents: []activity.Observation{reviewCycle(1, 3, "Deadlift"), reviewCycle(5, 7, "Deadlift")}}}, nil)
		e := recognitionreview.BuildEvaluation(p, []workoutprogress.Record{reviewRecord(1, 0, 20, "Power Clean", 3)}, nil, "")
		Expect(e.Summary.Mismatch).To(Equal(1))
		r := e.Records[0]
		Expect(*r.MovementMatch).To(BeFalse())
		Expect(*r.RepsDelta).To(Equal(-1))
		Expect(r.PredictedMovements).To(Equal([]recognitionreview.MovementCount{{Movement: "Deadlift", Reps: 2}}))
	})
	It("keeps unavailable coverage distinct from an observed zero", func() {
		p := reviewProgress([]workoutprogress.Interval{}, []workoutprogress.Gap{{Start: 0, End: 20, Clock: "media", Reason: "not_analyzed"}})
		e := recognitionreview.BuildEvaluation(p, []workoutprogress.Record{reviewRecord(1, 0, 20, "Power Clean", 0)}, nil, "")
		Expect(e.Summary.InsufficientEvidence).To(Equal(1))
		Expect(e.Records[0].ObservedCompleteReps).To(BeNil())
		Expect(e.Records[0].MovementMatch).To(BeNil())
		Expect(e.Records[0].RepsDelta).To(BeNil())
	})
	It("compares an observed incomplete attempt with a confirmed zero", func() {
		p := reviewProgress([]workoutprogress.Interval{{Start: 0, End: 20, Activity: "exercise", Movement: "Power Clean", RepEvents: []activity.Observation{{Start: 1, End: 3, Movement: "Power Clean", Unit: "reps", Complete: false, Evidence: "failed rack"}}}}, []workoutprogress.Gap{{Start: 1, End: 3, Clock: "media", Reason: "incomplete_cycle"}})
		e := recognitionreview.BuildEvaluation(p, []workoutprogress.Record{reviewRecord(1, 0, 20, "Power Clean", 0)}, nil, "")
		Expect(e.Summary.Matched).To(Equal(1))
		Expect(*e.Records[0].ObservedCompleteReps).To(BeZero())
	})
	It("counts a merged confirmation once across exercise and observed rest", func() {
		p := reviewProgress([]workoutprogress.Interval{{Start: 0, End: 10, Activity: "exercise", Movement: "Power Clean", RepEvents: []activity.Observation{reviewCycle(1, 3, "Power Clean")}}, {Start: 10, End: 15, Activity: "rest"}, {Start: 15, End: 20, Activity: "exercise", Movement: "Power Clean", RepEvents: []activity.Observation{reviewCycle(16, 18, "Power Clean")}}}, nil)
		e := recognitionreview.BuildEvaluation(p, []workoutprogress.Record{reviewRecord(1, 0, 20, "Power Clean", 2)}, nil, "")
		Expect(e.Summary.Total).To(Equal(1))
		Expect(e.Summary.Matched).To(Equal(1))
		Expect(*e.Records[0].ObservedCompleteReps).To(Equal(2))
	})
	It("owns repetitions by completion and does not double count events at touching record boundaries", func() {
		p := reviewProgress([]workoutprogress.Interval{{Start: 0, End: 20, Activity: "exercise", Movement: "Power Clean", RepEvents: []activity.Observation{reviewCycle(8, 10, "Power Clean"), reviewCycle(18, 20, "Power Clean")}}}, nil)
		e := recognitionreview.BuildEvaluation(p, []workoutprogress.Record{reviewRecord(1, 0, 10, "Power Clean", 1), reviewRecord(2, 10, 20, "Power Clean", 1)}, nil, "")
		Expect(e.Summary.Matched).To(Equal(2))
		Expect(*e.Records[0].ObservedCompleteReps).To(Equal(1))
		Expect(*e.Records[1].ObservedCompleteReps).To(Equal(1))
	})
	It("does not count duplicated references or repeated event objects twice", func() {
		cycle := reviewCycle(1, 3, "Power Clean")
		p := reviewProgress([]workoutprogress.Interval{{Start: 0, End: 10, Activity: "exercise", Movement: "Power Clean", ObservationRefs: []string{"chunk-1:event-1", "chunk-1:event-1"}, RepEvents: []activity.Observation{cycle, cycle}}}, nil)
		e := recognitionreview.BuildEvaluation(p, []workoutprogress.Record{reviewRecord(1, 0, 10, "Power Clean", 1)}, nil, "")
		Expect(e.Summary.Matched).To(Equal(1))
		Expect(e.Records[0].SourceRefs).To(Equal([]string{"chunk-1:event-1"}))
	})
	It("flags mixed original movements even when the total repetition count agrees", func() {
		p := reviewProgress([]workoutprogress.Interval{{Start: 0, End: 10, Activity: "exercise", Movement: "Power Clean", RepEvents: []activity.Observation{reviewCycle(1, 3, "Power Clean")}}, {Start: 10, End: 20, Activity: "exercise", Movement: "Deadlift", RepEvents: []activity.Observation{reviewCycle(11, 13, "Deadlift")}}}, nil)
		e := recognitionreview.BuildEvaluation(p, []workoutprogress.Record{reviewRecord(1, 0, 20, "Power Clean", 2)}, nil, "")
		Expect(e.Summary.Mismatch).To(Equal(1))
		Expect(*e.Records[0].RepsDelta).To(BeZero())
		Expect(*e.Records[0].MovementMatch).To(BeFalse())
	})
	It("excludes stale source confirmations and treats unsupported timed counts as unavailable", func() {
		stale := reviewRecord(2, 0, 10, "Run", 2)
		stale.SourceVersion = "old"
		p := reviewProgress([]workoutprogress.Interval{{Start: 0, End: 10, Activity: "exercise", Movement: "Run"}}, nil)
		e := recognitionreview.BuildEvaluation(p, []workoutprogress.Record{reviewRecord(1, 0, 10, "Run", 0), stale}, nil, "")
		Expect(e.Summary.Total).To(Equal(1))
		Expect(e.Summary.InsufficientEvidence).To(Equal(1))
		Expect(e.Records[0].ObservedCompleteReps).To(BeNil())
	})
	It("invalidates the evaluation fingerprint when confirmation or historical chunk evidence changes", func() {
		p := reviewProgress([]workoutprogress.Interval{}, nil)
		records := []workoutprogress.Record{reviewRecord(1, 0, 10, "Power Clean", 1)}
		a := recognitionreview.BuildEvaluation(p, records, []recognitionreview.ChunkEvidence{{ID: 1, SkipReason: "rest"}}, "old")
		b := recognitionreview.BuildEvaluation(p, records, []recognitionreview.ChunkEvidence{{ID: 1, SkipReason: "unknown"}}, "old")
		records[0].Reps = 2
		c := recognitionreview.BuildEvaluation(p, records, []recognitionreview.ChunkEvidence{{ID: 1, SkipReason: "rest"}}, "old")
		Expect(a.Fingerprint).NotTo(Equal(b.Fingerprint))
		Expect(a.Fingerprint).NotTo(Equal(c.Fingerprint))
	})
})

var _ = Describe("Recognition review batches", func() {
	It("excludes neither a crossing record silently nor a partial count", func() {
		rows := []recognitionreview.RecordEvaluation{{Record: reviewRecord(1, 10, 30, "Power Clean", 3)}}
		_, err := recognitionreview.BuildBatches(rows, 20, 40, []uint{1})
		Expect(err).To(HaveOccurred())
		batches, err := recognitionreview.BuildBatches(rows, 10, 30, []uint{1})
		Expect(err).NotTo(HaveOccurred())
		Expect(batches).To(HaveLen(1))
		Expect(batches[0].StartSecs).To(Equal(10.0))
		Expect(batches[0].EndSecs).To(Equal(30.0))
	})
	It("splits long records with bounded context and unique chronological core ownership", func() {
		rows := []recognitionreview.RecordEvaluation{{Record: reviewRecord(1, 20, 420, "Power Clean", 20)}}
		batches, err := recognitionreview.BuildBatches(rows, 20, 420, []uint{1})
		Expect(err).NotTo(HaveOccurred())
		Expect(batches).To(HaveLen(3))
		Expect(batches[0].StartSecs).To(Equal(20.0))
		Expect(batches[2].EndSecs).To(Equal(420.0))
		Expect(batches[0].CoreEndSecs).To(Equal(batches[1].CoreStartSecs))
		Expect(batches[1].CoreEndSecs).To(Equal(batches[2].CoreStartSecs))
		var input float64
		for _, b := range batches {
			input += b.EndSecs - b.StartSecs
			Expect(b.CoreEndSecs - b.CoreStartSecs).To(BeNumerically("<=", 150))
		}
		Expect(input).To(BeNumerically("~", 420.0, epsilonForTest))
	})
	It("rejects duplicates and records outside the active evaluation", func() {
		rows := []recognitionreview.RecordEvaluation{{Record: reviewRecord(1, 0, 10, "Power Clean", 1)}}
		_, err := recognitionreview.BuildBatches(rows, 0, 20, []uint{1, 1})
		Expect(err).To(HaveOccurred())
		_, err = recognitionreview.BuildBatches(rows, 0, 20, []uint{2})
		Expect(err).To(HaveOccurred())
	})
})

const epsilonForTest = 1e-6
