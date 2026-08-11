package db_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"gorm.io/gorm"
)

var _ = Describe("BuildMobilityHistory", func() {
	var (
		dbConn    *gorm.DB
		profileID uint
	)

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		user := testhelpers.CreateUser(dbConn, &db.User{Username: "testuser_mobility_db"})
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID, Name: "Test Mobility Profile"})
		profileID = profile.ID
	})

	It("groups restrictions across sessions, ranks by session count, and includes single-session provisional items", func() {
		// Session 1: Hip observation
		s1 := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profileID, WODDescription: "Session 1", SessionID: "sess-mob-1"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:            s1.SessionID,
			ProfileID:            profileID,
			Status:               "COMPLETED",
			MobilityObservations: `[{"joint":"Hip","side":"both","observation":"limited_hip_flexion","movement":"Back Squat","evidence":"스쿼트 하단 깊이 제한","confidence":0.9,"assessable":true}]`,
		})

		// Session 2: Hip observation
		s2 := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profileID, WODDescription: "Session 2", SessionID: "sess-mob-2"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:            s2.SessionID,
			ProfileID:            profileID,
			Status:               "COMPLETED",
			MobilityObservations: `[{"joint":"Hip","side":"both","observation":"limited_hip_flexion","movement":"Front Squat","evidence":"프론트 스쿼트 하단 깊이 제한","confidence":0.85,"assessable":true}]`,
		})

		// Session 3: Hip + Shoulder observation
		s3 := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profileID, WODDescription: "Session 3", SessionID: "sess-mob-3"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:            s3.SessionID,
			ProfileID:            profileID,
			Status:               "COMPLETED",
			MobilityObservations: `[
				{"joint":"Hip","side":"both","observation":"limited_hip_flexion","movement":"Overhead Squat","evidence":"오버헤드 스쿼트 하단 깊이 제한","confidence":0.88,"assessable":true},
				{"joint":"Shoulder","side":"left","observation":"limited_overhead_flexion","movement":"Press","evidence":"오버헤드 굴곡 제한","confidence":0.8,"assessable":true}
			]`,
		})

		history, err := db.BuildMobilityHistory(context.Background(), dbConn, profileID, "current-active-session")
		Expect(err).NotTo(HaveOccurred())
		Expect(history).To(HaveLen(2))

		// Hip should be first with SessionCount = 3
		Expect(history[0].Joint).To(Equal("Hip"))
		Expect(history[0].Observation).To(Equal("limited_hip_flexion"))
		Expect(history[0].SessionCount).To(Equal(3))
		Expect(history[0].Movements).To(ConsistOf("Back Squat", "Front Squat", "Overhead Squat"))

		// Shoulder should be second with SessionCount = 1 (provisional candidate)
		Expect(history[1].Joint).To(Equal("Shoulder"))
		Expect(history[1].Observation).To(Equal("limited_overhead_flexion"))
		Expect(history[1].SessionCount).To(Equal(1))
	})
})
