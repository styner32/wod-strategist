package history

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/wodeval"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

func daysAgo(days uint) time.Time {
	return time.Now().UTC().AddDate(0, 0, -int(days))
}

func daysAgoP(days uint) *time.Time {
	t := daysAgo(days)
	return &t
}

// return json result for workoutprogress.Progress
func exerciseProgressJSON(sourceID string) (workoutprogress.Progress, db.NullableJSONDocument) {
	p := workoutprogress.Build([]workoutprogress.Interval{
		{
			ID:       "interval-1",
			Start:    0,
			End:      10,
			Activity: "exercise",
			Movement: "Burpee",
			RepEvents: []activity.Observation{
				{Movement: "Burpee", Unit: "reps", Start: 1, End: 4, Complete: true, Evidence: "rep 1"},
			},
		},
	}, nil, sourceID)
	raw, err := json.Marshal(p)
	Expect(err).NotTo(HaveOccurred())
	return p, db.NullableJSONDocument(raw)
}

// create a performance record for the exercise interval
func confirmExerciseInterval(analysis db.AnalysisResult, progress workoutprogress.Progress) {
	reps := 1
	testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{
		ProfileID:     analysis.ProfileID,
		AnalysisID:    analysis.ID,
		SessionID:     analysis.SessionID,
		IntervalID:    "interval-1",
		SourceVersion: progress.SourceVersion,
		Status:        "confirmed",
		Outcome:       "completed",
		Movement:      "Burpee",
		StartSecs:     0,
		EndSecs:       10,
		Reps:          &reps,
	})
}

var _ = Describe("ListByDateRange", func() {
	var (
		profile     db.Profile
		session3    db.AnalysisResult
		session2    db.AnalysisResult
		session1    db.AnalysisResult
		archived    db.AnalysisResult
		incompleted db.AnalysisResult
	)

	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Athlete"})
		testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Another Athlete"}) // to make sure we're not getting results from another athlete

		session3 = testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261101-01JHISTOUTSIDE0000000001",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(30),
		})

		session2 = testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261108-01JHISTOLDER000000000002",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(22),
		})

		session1 = testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261109-01JHISTNEWER000000000003",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(21),
		})

		archivedAt := daysAgo(20)
		archived = testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID:  "WOD-20261109-01JHISTNEWER000000000004",
			ProfileID:  profile.ID,
			Status:     "COMPLETED",
			ArchivedAt: &archivedAt,
			CreatedAt:  daysAgo(20),
		})

		incompleted = testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261109-01JHISTNEWER000000000005",
			ProfileID: profile.ID,
			Status:    "PENDING",
			CreatedAt: daysAgo(19),
		})
	})

	It("returns active history records within the inclusive date range ordered newest first", func() {
		ctx := context.Background()

		results, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Start:     daysAgoP(23),
			End:       daysAgoP(10),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(3))
		Expect(results[0].ID).To(Equal(incompleted.ID))
		Expect(results[0].Session()).NotTo(BeNil())
		Expect(results[0].Session().SessionID).To(Equal(incompleted.SessionID))
		Expect(results[1].ID).To(Equal(session1.ID))
		Expect(results[1].Session()).NotTo(BeNil())
		Expect(results[1].Session().SessionID).To(Equal(session1.SessionID))
		Expect(results[2].ID).To(Equal(session2.ID))
		Expect(results[2].Session()).NotTo(BeNil())
		Expect(results[2].Session().SessionID).To(Equal(session2.SessionID))
	})

	It("returns archived sessions", func() {
		ctx := context.Background()

		results, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Start:     daysAgoP(23),
			End:       daysAgoP(10),
			Filter:    FilterArchived,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].ID).To(Equal(archived.ID))
	})

	It("filters pending sessions if input is needed", func() {
		ctx := context.Background()

		results, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Start:     daysAgoP(23),
			End:       daysAgoP(10),
			Filter:    FilterInputNeeded,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(2))
		Expect(results[0].ID).To(Equal(session1.ID))
		Expect(results[1].ID).To(Equal(session2.ID))
	})

	It("returns an empty array if no session in date range", func() {
		ctx := context.Background()

		results, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Start:     daysAgoP(2),
			End:       daysAgoP(1),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("returns an error if filter is invalid", func() {
		ctx := context.Background()

		results, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Start:     daysAgoP(23),
			End:       daysAgoP(10),
			Filter:    "invalidfilter",
		})

		Expect(err).To(HaveOccurred())
		Expect(results).To(BeZero())
	})

	It("returns active history records ordered newest first and paginates using before_id", func() {
		ctx := context.Background()

		firstPage, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Limit:     2,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(firstPage).To(HaveLen(2))
		Expect(firstPage[0].ID).To(Equal(incompleted.ID))
		Expect(firstPage[1].ID).To(Equal(session1.ID))

		secondPage, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Limit:     2,
			BeforeID:  firstPage[1].ID,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(secondPage).To(HaveLen(2))
		Expect(secondPage[0].ID).To(Equal(session2.ID))
		Expect(secondPage[1].ID).To(Equal(session3.ID))
	})

	It("skips recent input-completed sessions and paginates input-needed sessions up to limit", func() {
		ctx := context.Background()
		effortDone := 8
		// both are finished with effort input
		doneRecent2 := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261109-01JHISTDONERECENT00002",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(2),
		})
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            doneRecent2.SessionID,
			ProfileID:            profile.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			Effort:               &effortDone,
		})
		doneRecent1 := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261110-01JHISTDONERECENT00001",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(1),
		})
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            doneRecent1.SessionID,
			ProfileID:            profile.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			Effort:               &effortDone,
		})

		firstPage, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Filter:    FilterInputNeeded,
			Limit:     2,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(firstPage).To(HaveLen(2))
		Expect(firstPage[0].ID).To(Equal(session1.ID))
		Expect(firstPage[1].ID).To(Equal(session2.ID))
		Expect(firstPage[0].InputStatus).NotTo(BeNil())
		Expect(firstPage[0].InputStatus.EffortMissing).To(BeTrue())

		secondPage, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Filter:    FilterInputNeeded,
			Limit:     2,
			BeforeID:  firstPage[1].ID,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(secondPage).To(HaveLen(1))
		Expect(secondPage[0].ID).To(Equal(session3.ID))
		Expect(secondPage[0].InputStatus).NotTo(BeNil())
		Expect(secondPage[0].InputStatus.EffortMissing).To(BeTrue())
	})

	It("separates FilterEffortNeeded, FilterPerformanceNeeded, and FilterInputNeeded", func() {
		ctx := context.Background()

		effortEight := 8
		repsOne := 1
		profile2 := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Another Athlete 2"})

		// effort done, perf done
		progDone, rawDone := exerciseProgressJSON("done-src")
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            "WOD-20261110-INPUTDONE001",
			ProfileID:            profile2.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			Effort:               &effortEight,
		})
		doneBoth := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:       "WOD-20261110-INPUTDONE001",
			ProfileID:       profile2.ID,
			Status:          "COMPLETED",
			WorkoutProgress: rawDone,
			CreatedAt:       daysAgo(3),
		})
		confirmExerciseInterval(doneBoth, progDone)

		// effort done, perf missing
		progEffortMissing, rawEffortMissing := exerciseProgressJSON("effort-missing-src")
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            "WOD-20261110-MISSINGEFF002",
			ProfileID:            profile2.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			Effort:               nil,
		})
		effortNeededOnly := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:       "WOD-20261110-MISSINGEFF002",
			ProfileID:       profile2.ID,
			Status:          "COMPLETED",
			WorkoutProgress: rawEffortMissing,
			CreatedAt:       daysAgo(2),
		})
		confirmExerciseInterval(effortNeededOnly, progEffortMissing)

		// effort done, perf missing (stale record)
		_, rawPerfMissing := exerciseProgressJSON("perf-missing-v2")
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            "WOD-20261110-MISSINGPERF003",
			ProfileID:            profile2.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			Effort:               &effortEight,
		})
		perfNeededOnly := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:       "WOD-20261110-MISSINGPERF003",
			ProfileID:       profile2.ID,
			Status:          "COMPLETED",
			WorkoutProgress: rawPerfMissing,
			CreatedAt:       daysAgo(1),
		})

		testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{
			ProfileID:     profile2.ID,
			AnalysisID:    perfNeededOnly.ID,
			SessionID:     perfNeededOnly.SessionID,
			IntervalID:    "interval-1",
			SourceVersion: "stale-version-v1", // progress.SourceVersion과 불일치 -> 재확인 필요
			Status:        "confirmed",
			Outcome:       "completed",
			Movement:      "Burpee",
			StartSecs:     0,
			EndSecs:       10,
			Reps:          &repsOne,
		})

		// only effort needed
		effortList, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile2.ID,
			Filter:    FilterEffortNeeded,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(effortList).To(HaveLen(1))
		Expect(effortList[0].ID).To(Equal(effortNeededOnly.ID))
		Expect(effortList[0].InputStatus.EffortMissing).To(BeTrue())
		Expect(effortList[0].InputStatus.PerformanceMissing).To(BeFalse())

		// only perf needed
		perfList, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile2.ID,
			Filter:    FilterPerformanceNeeded,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(perfList).To(HaveLen(1))
		Expect(perfList[0].ID).To(Equal(perfNeededOnly.ID))
		Expect(perfList[0].InputStatus.EffortMissing).To(BeFalse())
		Expect(perfList[0].InputStatus.PerformanceMissing).To(BeTrue())
		Expect(perfList[0].InputStatus.ExerciseIntervalCount).To(Equal(1))
		Expect(perfList[0].InputStatus.ConfirmedIntervalCount).To(Equal(0))
		Expect(perfList[0].InputStatus.NeedsConfirmationCount).To(Equal(1))

		// for both
		anyList, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile2.ID,
			Filter:    FilterInputNeeded,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(anyList).To(HaveLen(2))
		Expect(anyList[0].ID).To(Equal(perfNeededOnly.ID))
		Expect(anyList[1].ID).To(Equal(effortNeededOnly.ID))
	})

	It("does not mark accessory sessions as effort_missing", func() {
		ctx := context.Background()

		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            "ACCESSORY-20261110-001",
			ProfileID:            profile.ID,
			WorkoutType:          "accessory",
			WODEvaluationVersion: wodeval.Version,
			Effort:               nil,
		})
		accessory := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "ACCESSORY-20261110-001",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(2),
		})

		activeList, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Filter:    FilterActive,
		})
		Expect(err).NotTo(HaveOccurred())
		// 5 sessions are active: accessory, wod, wod, wod, wod
		Expect(activeList).To(HaveLen(5))
		Expect(activeList[0].ID).To(Equal(accessory.ID))
		Expect(activeList[0].InputStatus.EffortEligible).To(BeFalse())
		Expect(activeList[0].InputStatus.EffortMissing).To(BeFalse())

		effortList, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Filter:    FilterEffortNeeded,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(effortList).To(HaveLen(3))
		// make sure no pending, accessory are included
		Expect(effortList[0].ID).To(Equal(session1.ID))
		Expect(effortList[1].ID).To(Equal(session2.ID))
		Expect(effortList[2].ID).To(Equal(session3.ID))
	})

	It("backfills missing WODDescription from session and clears omitted/raw columns", func() {
		ctx := context.Background()
		profile2 := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Another Athlete 2"})

		_, rawProg := exerciseProgressJSON("sanitize-src")
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            "WOD-20261110-BACKFILL001",
			ProfileID:            profile2.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			WODDescription:       "Fran 21-15-9",
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:                "WOD-20261110-BACKFILL001",
			ProfileID:                profile2.ID,
			Status:                   "COMPLETED",
			WODDescription:           "",
			WorkoutProgress:          rawProg,
			SensorTimeline:           db.JSONDocument(`[{"t":1,"hr":165}]`),
			AgenticHighlightAnalysis: db.JSONDocument(`{"run_id":"r1"}`),
		})
		archivedAt := daysAgo(1)
		testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID:       "WOD-20261110-ARCHIVED001",
			ProfileID:       profile2.ID,
			Status:          "COMPLETED",
			ArchivedAt:      &archivedAt,
			WorkoutProgress: rawProg,
		})
		activeResults, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile2.ID,
			Filter:    FilterActive,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(activeResults).To(HaveLen(1))
		Expect(activeResults[0].WODDescription).To(Equal("Fran 21-15-9"))
		Expect(activeResults[0].WorkoutProgress).To(BeNil())
		Expect(activeResults[0].SensorTimeline).To(BeEmpty())
		Expect(activeResults[0].AgenticHighlightAnalysis).To(BeEmpty())
		archivedResults, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile2.ID,
			Filter:    FilterArchived,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(archivedResults).To(HaveLen(1))
		Expect(archivedResults[0].InputStatus).To(BeNil())
		Expect(archivedResults[0].WorkoutProgress).To(BeNil())
	})

	It("paginates cursor queries by id desc when timestamps are identical and orders date-range queries by created_at desc", func() {
		ctx := context.Background()
		profile2 := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Ordering Athlete"})

		// cursor mode (no Start/End): identical CreatedAt should paginate without gaps by ID desc
		sameTime := daysAgo(3)
		c1 := testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261111-CURSORORDER001",
			ProfileID: profile2.ID,
			Status:    "COMPLETED",
			CreatedAt: sameTime,
		})

		c2 := testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261111-CURSORORDER002",
			ProfileID: profile2.ID,
			Status:    "COMPLETED",
			CreatedAt: sameTime,
		})

		c3 := testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261109-CURSORORDER003",
			ProfileID: profile2.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(5),
		})

		cursorPage1, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile2.ID,
			Limit:     2,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cursorPage1).To(HaveLen(2))
		Expect(cursorPage1[0].ID).To(Equal(c3.ID))
		Expect(cursorPage1[1].ID).To(Equal(c2.ID))

		cursorPage2, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile2.ID,
			Limit:     2,
			BeforeID:  cursorPage1[1].ID,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cursorPage2).To(HaveLen(1))
		Expect(cursorPage2[0].ID).To(Equal(c1.ID))

		// date range mode (Start/End): Insert order (ID) should be irrelevant, CreatedAt should be newest first
		rangeList, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: profile2.ID,
			Start:     daysAgoP(7),
			End:       daysAgoP(1),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(rangeList).To(HaveLen(3))

		// sameTime(3 days ago) c2, c1 are first (same CreatedAt), c3 is last (5 days ago)
		Expect(rangeList[0].ID).To(Equal(c2.ID))
		Expect(rangeList[1].ID).To(Equal(c1.ID))
		Expect(rangeList[2].ID).To(Equal(c3.ID))
	})

	It("isolates history records and session metadata across different profiles", func() {
		ctx := context.Background()
		owner := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Owner Athlete"})
		stranger := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Stranger Athlete"})
		effortNine := 9
		// stranger's analysis result
		testhelpers.CreateAnalysisResultWithSession(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261110-STRANGERONLY01",
			ProfileID: stranger.ID,
			Status:    "COMPLETED",
		})
		// stranger's session
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            "P1-WOD-2025-01-01-10-00",
			ProfileID:            stranger.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			WODDescription:       "Stranger Secret WOD",
			Effort:               &effortNine,
		})

		mine := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:      "P1-WOD-2025-01-01-10-00",
			ProfileID:      owner.ID,
			Status:         "COMPLETED",
			WODDescription: "",
		})
		results, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: owner.ID,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].ID).To(Equal(mine.ID))
		Expect(results[0].WODDescription).To(BeEmpty())
		Expect(results[0].InputStatus.Effort).To(BeNil())
		Expect(results[0].InputStatus.EffortEligible).To(BeFalse())
	})

	It("returns a non-nil empty slice that marshals to [] when no records match", func() {
		ctx := context.Background()
		emptyProfile := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Empty Athlete"})
		// case 1: no records in the database
		emptyActive, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: emptyProfile.ID,
			Limit:     20,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(emptyActive).NotTo(BeNil())
		rawActive, err := json.Marshal(emptyActive)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(rawActive)).To(Equal("[]"))

		// case 2: completed records but filtered out by input needed filter
		effortDone := 7
		done := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261110-ALLDONE001",
			ProfileID: emptyProfile.ID,
			Status:    "COMPLETED",
		})
		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            done.SessionID,
			ProfileID:            emptyProfile.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			Effort:               &effortDone,
		})
		emptyFiltered, err := List(ctx, dbConn, HistoryListQuery{
			ProfileID: emptyProfile.ID,
			Filter:    FilterInputNeeded,
			Limit:     20,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(emptyFiltered).NotTo(BeNil())

		rawFiltered, err := json.Marshal(emptyFiltered)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(rawFiltered)).To(Equal("[]"))
	})
})
