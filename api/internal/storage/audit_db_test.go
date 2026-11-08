package storage_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/testhelpers"
	"gorm.io/gorm"
)

var _ = Describe("AuditObjectInfosAgainstDB", Ordered, ContinueOnFailure, func() {
	var conn *gorm.DB

	BeforeAll(func() {
		var err error
		conn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if conn != nil {
			sqlDB, err := conn.DB()
			Expect(err).NotTo(HaveOccurred())
			Expect(sqlDB.Close()).To(Succeed())
		}
	})

	BeforeEach(func() {
		testhelpers.CleanupDB(conn)
	})

	It("classifies referenced, unreferenced, and ambiguous objects across categories without modifying DB rows", func() {
		profile := testhelpers.CreateProfile(conn, &db.Profile{Name: "Storage Audit Athlete"})
		completedSID := "WOD-20261106-01JAUDITCOMPLETED000001"
		activeSID := "WOD-20261106-01JAUDITACTIVE000000002"
		orphanSID := "WOD-20261106-01JAUDITORPHAN000000003"

		// 1. Completed session with explicit references and leftover staging object.
		testhelpers.CreateSession(conn, &db.Session{
			SessionID:   completedSID,
			ProfileID:   profile.ID,
			Status:      db.SessionStatusStarted, // Remains started in DB while analysis/media are COMPLETED
			WorkoutType: "wod",
		})
		testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{
			SessionID:       completedSID,
			ProfileID:       profile.ID,
			Status:          "COMPLETED",
			AvailableVideos: db.CommaStringArray{"merged", "hardsub"},
		})
		mergedObj := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, completedSID)
		hardsubObj := fmt.Sprintf("videos/%d/%s/hardsub.mp4", profile.ID, completedSID)
		staleStagingObj := fmt.Sprintf("videos/%d/%s/staging/part_01.mp4", profile.ID, completedSID)
		run := testhelpers.CreateRecordingMediaRun(conn, &db.RecordingMediaRun{
			ProfileID:           profile.ID,
			SessionID:           completedSID,
			Version:             db.RecordingMediaVersion,
			State:               "COMPLETED",
			MergedObject:        mergedObj,
			MergedStagingObject: staleStagingObj,
		})
		srcObj := fmt.Sprintf("videos/%d/%s/chunk_0001.mp4", profile.ID, completedSID)
		recChunk := testhelpers.CreateRecordingChunk(conn, &db.RecordingChunk{
			RunID:        run.ID,
			ProfileID:    profile.ID,
			SessionID:    completedSID,
			SourceObject: srcObj,
			SourceURI:    "gs://test-bucket/" + srcObj,
		})
		prepObj := fmt.Sprintf("videos/%d/%s/prepared/v1/chunk_0001.mp4", profile.ID, completedSID)
		testhelpers.CreateChunkMediaPreparation(conn, &db.ChunkMediaPreparation{
			RunID:        run.ID,
			ChunkID:      recChunk.ID,
			State:        "READY",
			OutputObject: prepObj,
		})

		// 2. Active in-progress session: unlinked object under active session is classified as ambiguous (never unreferenced).
		testhelpers.CreateSession(conn, &db.Session{
			SessionID:   activeSID,
			ProfileID:   profile.ID,
			Status:      db.SessionStatusStarted,
			WorkoutType: "wod",
		})
		activeUnlinkedObj := fmt.Sprintf("videos/%d/%s/chunk_inflight.mp4", profile.ID, activeSID)

		// 3. Orphan session absent from DB -> unreferenced.
		orphanObj := fmt.Sprintf("videos/%d/%s/chunk_orphan.mp4", profile.ID, orphanSID)

		objects := []storage.SizedObjectInfo{
			{Name: srcObj, Size: 1000},
			{Name: prepObj, Size: 800},
			{Name: mergedObj, Size: 2500},
			{Name: hardsubObj, Size: 2700},
			{Name: staleStagingObj, Size: 900},
			{Name: activeUnlinkedObj, Size: 600},
			{Name: orphanObj, Size: 500},
		}

		report, err := storage.AuditObjectInfosAgainstDB(context.Background(), "test-bucket", "videos/", objects, conn)
		Expect(err).NotTo(HaveOccurred())

		Expect(report.TotalObjects).To(Equal(7))
		Expect(report.TotalBytes).To(Equal(int64(9000)))

		// Referenced: srcObj (1000) + prepObj (800) + mergedObj (2500) + hardsubObj (2700) = 4 objects, 7000 bytes.
		Expect(report.ByReference[storage.ReferenceReferenced]).To(Equal(storage.AuditBucketSummary{
			ObjectCount: 4,
			TotalBytes:  7000,
		}))
		// Unreferenced: staleStagingObj on completed run (900) + orphanObj (500) = 2 objects, 1400 bytes.
		Expect(report.ByReference[storage.ReferenceUnreferenced]).To(Equal(storage.AuditBucketSummary{
			ObjectCount: 2,
			TotalBytes:  1400,
		}))
		// Ambiguous: activeUnlinkedObj on active session (600) = 1 object, 600 bytes.
		Expect(report.ByReference[storage.ReferenceAmbiguous]).To(Equal(storage.AuditBucketSummary{
			ObjectCount: 1,
			TotalBytes:  600,
		}))
	})
})
