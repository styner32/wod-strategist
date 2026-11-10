package controllers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/wodeval"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

var _ = Describe("history input status helpers", func() {
	sampleProgress := func() workoutprogress.Progress {
		return workoutprogress.Build([]workoutprogress.Interval{
			{
				ID:              "interval-1",
				Start:           0,
				End:             10,
				Activity:        "exercise",
				Movement:        "Power Clean",
				ObservationRefs: []string{"seg-1"},
				RepEvents: []activity.Observation{
					{Movement: "Power Clean", Unit: "reps", Start: 1, End: 3, Complete: true, Evidence: "clean rep"},
				},
			},
		}, nil, "history-helper-src")
	}

	It("returns nil input status for archived analysis rows", func() {
		now := time.Now().UTC()
		p := sampleProgress()
		row := db.AnalysisResult{Status: "COMPLETED", ArchivedAt: &now}
		sess := &db.Session{WorkoutType: "wod", WODEvaluationVersion: wodeval.Version}
		Expect(computeHistoryInputStatus(row, sess, &p, nil)).To(BeNil())
	})

	It("does not mark non-completed rows as missing effort or performance", func() {
		p := sampleProgress()
		row := db.AnalysisResult{Status: "PROCESSING"}
		sess := &db.Session{WorkoutType: "wod", WODEvaluationVersion: wodeval.Version}
		status := computeHistoryInputStatus(row, sess, &p, nil)
		Expect(status).NotTo(BeNil())
		Expect(status.EffortEligible).To(BeTrue())
		Expect(status.EffortMissing).To(BeFalse())
		Expect(status.PerformanceMissing).To(BeFalse())
	})

	It("tracks effort eligibility and performance confirmation independently", func() {
		p := sampleProgress()
		effortSeven := 7

		unenteredWod := computeHistoryInputStatus(
			db.AnalysisResult{Status: "COMPLETED"},
			&db.Session{WorkoutType: "wod", WODEvaluationVersion: wodeval.Version},
			&p,
			nil,
		)
		Expect(unenteredWod).To(Equal(&db.HistoryInputStatus{
			EffortEligible:         true,
			Effort:                 nil,
			EffortMissing:          true,
			ExerciseIntervalCount:  1,
			ConfirmedIntervalCount: 0,
			NeedsConfirmationCount: 0,
			PerformanceMissing:     true,
		}))

		ratedAndConfirmed := computeHistoryInputStatus(
			db.AnalysisResult{Status: "COMPLETED"},
			&db.Session{WorkoutType: "wod", WODEvaluationVersion: wodeval.Version, Effort: &effortSeven},
			&p,
			[]workoutprogress.Record{
				{ID: 1, SessionID: "s1", IntervalID: "interval-1", SourceVersion: p.SourceVersion, Status: "confirmed", Outcome: "completed", Movement: "Power Clean", Start: 0, End: 10},
			},
		)
		Expect(ratedAndConfirmed).To(Equal(&db.HistoryInputStatus{
			EffortEligible:         true,
			Effort:                 &effortSeven,
			EffortMissing:          false,
			ExerciseIntervalCount:  1,
			ConfirmedIntervalCount: 1,
			NeedsConfirmationCount: 0,
			PerformanceMissing:     false,
		}))

		legacyWod := computeHistoryInputStatus(
			db.AnalysisResult{Status: "COMPLETED"},
			&db.Session{WorkoutType: "wod", WODEvaluationVersion: 0},
			nil,
			nil,
		)
		Expect(legacyWod.EffortEligible).To(BeFalse())
		Expect(legacyWod.EffortMissing).To(BeFalse())
		Expect(legacyWod.PerformanceMissing).To(BeFalse())
	})

	DescribeTable("matchesHistoryMissingFilter",
		func(status *db.HistoryInputStatus, missing string, want bool) {
			Expect(matchesHistoryMissingFilter(status, missing)).To(Equal(want))
		},
		Entry("nil status never matches filter", nil, "any", false),
		Entry("empty filter matches non-nil status", &db.HistoryInputStatus{}, "", true),
		Entry("effort filter matches effort_missing only", &db.HistoryInputStatus{EffortMissing: true}, "effort", true),
		Entry("effort filter rejects performance-only missing", &db.HistoryInputStatus{PerformanceMissing: true}, "effort", false),
		Entry("performance filter matches performance_missing only", &db.HistoryInputStatus{PerformanceMissing: true}, "performance", true),
		Entry("any filter matches either effort or performance missing", &db.HistoryInputStatus{PerformanceMissing: true}, "any", true),
		Entry("any filter rejects fully completed item", &db.HistoryInputStatus{EffortEligible: true}, "any", false),
	)
})
