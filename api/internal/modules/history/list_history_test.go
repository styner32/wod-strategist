package history

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

func daysAgo(days uint) time.Time {
	return time.Now().UTC().AddDate(0, 0, -int(days))
}

func daysAgoP(days uint) *time.Time {
	t := daysAgo(days)
	return &t
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
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{Name: "History Athlete"})

		session3 = testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261101-01JHISTOUTSIDE0000000001",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(30),
		})

		session2 = testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261108-01JHISTOLDER000000000002",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(22),
		})

		session1 = testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261109-01JHISTNEWER000000000003",
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			CreatedAt: daysAgo(21),
		})

		archivedAt := daysAgo(20)
		archived = testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:  "WOD-20261109-01JHISTNEWER000000000004",
			ProfileID:  profile.ID,
			Status:     "COMPLETED",
			ArchivedAt: &archivedAt,
			CreatedAt:  daysAgo(20),
		})

		incompleted = testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261109-01JHISTNEWER000000000005",
			ProfileID: profile.ID,
			Status:    "PENDING",
			CreatedAt: daysAgo(19),
		})
	})

	It("returns active history records within the inclusive date range ordered newest first", func() {
		ctx := context.Background()

		results, err := ListByDateRange(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Start:     daysAgoP(23),
			End:       daysAgoP(10),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(3))
		Expect(results[0].ID).To(Equal(incompleted.ID))
		Expect(results[1].ID).To(Equal(session1.ID))
		Expect(results[2].ID).To(Equal(session2.ID))
	})

	It("returns archived sessions", func() {
		ctx := context.Background()

		results, err := ListByDateRange(ctx, dbConn, HistoryListQuery{
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

		results, err := ListByDateRange(ctx, dbConn, HistoryListQuery{
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

		results, err := ListByDateRange(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Start:     daysAgoP(2),
			End:       daysAgoP(1),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("returns an error if filter is invalid", func() {
		ctx := context.Background()

		results, err := ListByDateRange(ctx, dbConn, HistoryListQuery{
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

		firstPage, err := ListByDateRange(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Limit:     2,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(firstPage).To(HaveLen(2))
		Expect(firstPage[0].ID).To(Equal(incompleted.ID))
		Expect(firstPage[1].ID).To(Equal(session1.ID))

		secondPage, err := ListByDateRange(ctx, dbConn, HistoryListQuery{
			ProfileID: profile.ID,
			Limit:     2,
			BeforeID:  firstPage[1].ID,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(secondPage).To(HaveLen(2))
		Expect(secondPage[0].ID).To(Equal(session2.ID))
		Expect(secondPage[1].ID).To(Equal(session3.ID))
	})
})
