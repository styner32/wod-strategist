package db_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ = Describe("Session activity_summary_cache_version trigger and concurrency serialization", Ordered, ContinueOnFailure, func() {
	var conn *gorm.DB

	BeforeAll(func() {
		var err error
		conn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if conn == nil {
			return
		}
		sqlDB, err := conn.DB()
		Expect(err).NotTo(HaveOccurred())
		Expect(sqlDB.Close()).To(Succeed())
	})

	BeforeEach(func() {
		testhelpers.CleanupDB(conn)
	})

	It("invalidates activity_summary_cache_version on relevant chunk insert/update/delete and ignores coaching-only updates", func() {
		user := testhelpers.CreateUser(conn, &db.User{})
		profile := testhelpers.CreateProfile(conn, &db.Profile{UserID: user.ID, Name: "Trigger Athlete"})
		sid := fmt.Sprintf("WOD-20261106-TRIG%d", time.Now().UnixNano())
		session := testhelpers.CreateSession(conn, &db.Session{
			SessionID:                   sid,
			ProfileID:                   profile.ID,
			Status:                      db.SessionStatusStarted,
			WorkoutType:                 "wod",
			ActivitySummary:             db.NullableJSONDocument(`{"version":1}`),
			ActivitySummaryCacheVersion: 1,
		})

		s0, e0 := 0.0, 10.0
		// INSERT invalidates cache_version -> 0
		chunk := testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{
			SessionID:      sid,
			ProfileID:      profile.ID,
			FilePath:       fmt.Sprintf("gs://bucket/videos/%d/%s/c1.mp4", profile.ID, sid),
			Status:         "COMPLETED",
			ExerciseType:   "Air Squat",
			StartSecs:      &s0,
			EndSecs:        &e0,
			MediaStartSecs: &s0,
			MediaEndSecs:   &e0,
			Output:         "initial coaching",
		})
		var reloaded db.Session
		Expect(conn.First(&reloaded, session.ID).Error).To(Succeed())
		Expect(reloaded.ActivitySummaryCacheVersion).To(Equal(0))

		// Mark cache valid again (= 1).
		Expect(conn.Model(&db.Session{}).Where("id = ?", session.ID).Update("activity_summary_cache_version", 1).Error).To(Succeed())

		// Coaching-only update (Output / HeartRateBPM) must NOT invalidate summary cache.
		Expect(conn.Model(&db.ChunkAnalysisResult{}).Where("id = ?", chunk.ID).Updates(map[string]any{
			"output":         "updated coaching text",
			"heart_rate_bpm": 162,
		}).Error).To(Succeed())
		Expect(conn.First(&reloaded, session.ID).Error).To(Succeed())
		Expect(reloaded.ActivitySummaryCacheVersion).To(Equal(1))

		// Relevant observation update MUST invalidate cache_version -> 0.
		Expect(conn.Model(&db.ChunkAnalysisResult{}).Where("id = ?", chunk.ID).Update(
			"movement_observations",
			db.NullableJSONDocument(`{"version":1,"duration_secs":10,"target_state":"identified","activity_state":"exercise","events":[{"movement":"Air Squat","unit":"reps","start_secs":1,"end_secs":3,"complete":true,"evidence":"squat"}],"unassessed":[]}`),
		).Error).To(Succeed())
		Expect(conn.First(&reloaded, session.ID).Error).To(Succeed())
		Expect(reloaded.ActivitySummaryCacheVersion).To(Equal(0))

		// Mark cache valid again (= 1), then DELETE chunk -> must invalidate to 0.
		Expect(conn.Model(&db.Session{}).Where("id = ?", session.ID).Update("activity_summary_cache_version", 1).Error).To(Succeed())
		Expect(conn.Delete(&db.ChunkAnalysisResult{}, chunk.ID).Error).To(Succeed())
		Expect(conn.First(&reloaded, session.ID).Error).To(Succeed())
		Expect(reloaded.ActivitySummaryCacheVersion).To(Equal(0))
	})

	It("serializes chunk mutations with FOR UPDATE summary writers even when cache_version is already 0", func() {
		user := testhelpers.CreateUser(conn, &db.User{})
		profile := testhelpers.CreateProfile(conn, &db.Profile{UserID: user.ID, Name: "Race Athlete"})
		sid := fmt.Sprintf("WOD-20261106-RACE%d", time.Now().UnixNano())
		session := testhelpers.CreateSession(conn, &db.Session{
			SessionID:                   sid,
			ProfileID:                   profile.ID,
			Status:                      db.SessionStatusStarted,
			WorkoutType:                 "wod",
			ActivitySummaryCacheVersion: 0, // ALREADY 0 before the race!
		})
		s0, e0 := 0.0, 10.0
		chunk := testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{
			SessionID:    sid,
			ProfileID:    profile.ID,
			FilePath:     fmt.Sprintf("gs://bucket/videos/%d/%s/c1.mp4", profile.ID, sid),
			Status:       "PENDING",
			ExerciseType: "Burpee",
			StartSecs:    &s0,
			EndSecs:      &e0,
		})

		// Interleaving 1: Chunk writer updates chunk first (holding trigger's sessions row lock while cache_version was ALREADY 0).
		// Summary writer starts concurrently and must block on FOR UPDATE until chunk writer commits, thus seeing Status="COMPLETED".
		chunkUpdated := make(chan struct{})
		releaseChunkTx := make(chan struct{})
		writerObservedStatus := make(chan string, 1)

		go func() {
			defer GinkgoRecover()
			err := conn.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&db.ChunkAnalysisResult{}).Where("id = ?", chunk.ID).Update("status", "COMPLETED").Error; err != nil {
					return err
				}
				close(chunkUpdated)
				<-releaseChunkTx
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		}()

		<-chunkUpdated
		writerDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(writerDone)
			err := conn.Transaction(func(tx *gorm.DB) error {
				var locked db.Session
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, session.ID).Error; err != nil {
					return err
				}
				var cur db.ChunkAnalysisResult
				if err := tx.First(&cur, chunk.ID).Error; err != nil {
					return err
				}
				writerObservedStatus <- cur.Status
				return tx.Model(&locked).Updates(map[string]any{
					"activity_summary":               db.NullableJSONDocument(fmt.Sprintf(`{"status_seen":%q}`, cur.Status)),
					"activity_summary_cache_version": 1,
				}).Error
			})
			Expect(err).NotTo(HaveOccurred())
		}()

		Consistently(writerObservedStatus, 80*time.Millisecond).ShouldNot(Receive())
		close(releaseChunkTx)
		Eventually(writerObservedStatus, 2*time.Second).Should(Receive(Equal("COMPLETED")))
		Eventually(writerDone, 2*time.Second).Should(BeClosed())

		// Interleaving 2: Summary writer locks sessions FOR UPDATE first (reading old chunk), while concurrent chunk mutation blocks on trigger;
		// after both commit, cache_version MUST be 0 (invalidated), never 1.
		summaryLocked := make(chan struct{})
		releaseSummaryTx := make(chan struct{})
		summaryTxDone := make(chan struct{})

		go func() {
			defer GinkgoRecover()
			defer close(summaryTxDone)
			err := conn.Transaction(func(tx *gorm.DB) error {
				var locked db.Session
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, session.ID).Error; err != nil {
					return err
				}
				close(summaryLocked)
				<-releaseSummaryTx
				return tx.Model(&locked).Updates(map[string]any{
					"activity_summary":               db.NullableJSONDocument(`{"stale":true}`),
					"activity_summary_cache_version": 1,
				}).Error
			})
			Expect(err).NotTo(HaveOccurred())
		}()

		<-summaryLocked
		chunkTxDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(chunkTxDone)
			s1, e1 := 0.0, 12.0
			err := conn.Model(&db.ChunkAnalysisResult{}).Where("id = ?", chunk.ID).Updates(map[string]any{
				"start_secs": s1,
				"end_secs":   e1,
			}).Error
			Expect(err).NotTo(HaveOccurred())
		}()

		Consistently(chunkTxDone, 80*time.Millisecond).ShouldNot(BeClosed())
		close(releaseSummaryTx)
		Eventually(summaryTxDone, 2*time.Second).Should(BeClosed())
		Eventually(chunkTxDone, 2*time.Second).Should(BeClosed())

		var finalSess db.Session
		Expect(conn.First(&finalSess, session.ID).Error).To(Succeed())
		Expect(finalSess.ActivitySummaryCacheVersion).To(Equal(0), "stale summary must never remain marked valid after concurrent chunk update")
	})
})
