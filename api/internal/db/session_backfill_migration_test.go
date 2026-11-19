package db_test

import (
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"gorm.io/gorm"
)

var _ = Describe("000066 backfill_sessions_and_idempotency_index migration", Ordered, ContinueOnFailure, func() {
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

	It("enforces partial unique index on non-empty (profile_id, idempotency_key) while allowing empty keys and cross-profile reuse", func() {
		profile1 := testhelpers.CreateProfile(conn, &db.Profile{Name: "Athlete 1"})
		profile2 := testhelpers.CreateProfile(conn, &db.Profile{Name: "Athlete 2"})

		sessA := db.Session{
			SessionID:      "WOD-20261119-01JIDEMPOTENCYTEST00000001",
			ProfileID:      profile1.ID,
			Status:         db.SessionStatusStarted,
			WorkoutType:    "wod",
			IdempotencyKey: "req-idem-1",
			MovementHints:  db.JSONDocument(`[]`),
		}
		Expect(conn.Create(&sessA).Error).To(Succeed())

		sessB := db.Session{
			SessionID:      "WOD-20261119-01JIDEMPOTENCYTEST00000002",
			ProfileID:      profile1.ID,
			Status:         db.SessionStatusStarted,
			WorkoutType:    "wod",
			IdempotencyKey: "req-idem-1",
			MovementHints:  db.JSONDocument(`[]`),
		}
		Expect(conn.Create(&sessB).Error).To(HaveOccurred())

		sessC := db.Session{
			SessionID:      "WOD-20261119-01JIDEMPOTENCYTEST00000003",
			ProfileID:      profile2.ID,
			Status:         db.SessionStatusStarted,
			WorkoutType:    "wod",
			IdempotencyKey: "req-idem-1",
			MovementHints:  db.JSONDocument(`[]`),
		}
		Expect(conn.Create(&sessC).Error).To(Succeed())

		emptyKey1 := db.Session{
			SessionID:      "WOD-20261119-01JEMPTYKEYTEST0000000001",
			ProfileID:      profile1.ID,
			Status:         db.SessionStatusStarted,
			WorkoutType:    "wod",
			IdempotencyKey: "",
			MovementHints:  db.JSONDocument(`[]`),
		}
		Expect(conn.Create(&emptyKey1).Error).To(Succeed())

		whitespaceKey2 := db.Session{
			SessionID:      "WOD-20261119-01JEMPTYKEYTEST0000000002",
			ProfileID:      profile1.ID,
			Status:         db.SessionStatusStarted,
			WorkoutType:    "wod",
			IdempotencyKey: "   ",
			MovementHints:  db.JSONDocument(`[]`),
		}
		Expect(conn.Create(&whitespaceKey2).Error).To(Succeed())
	})

	It("backfills missing sessions from analysis_results, chunk_analysis_results, and recording_media_runs, disambiguates duplicate keys, and rolls back cleanly", func() {
		upBytes, err := os.ReadFile("migrations/000066_backfill_sessions_and_idempotency_index.up.sql")
		Expect(err).NotTo(HaveOccurred())
		downBytes, err := os.ReadFile("migrations/000066_backfill_sessions_and_idempotency_index.down.sql")
		Expect(err).NotTo(HaveOccurred())
		upSQL := string(upBytes)
		downSQL := string(downBytes)

		tx := conn.Begin()
		Expect(tx.Error).NotTo(HaveOccurred())
		DeferCleanup(func() {
			tx.Rollback()
		})

		Expect(tx.Exec(downSQL).Error).To(Succeed())

		profile := testhelpers.CreateProfile(tx, &db.Profile{Name: "Backfill Athlete"})
		effortEight := 8

		existingSess := testhelpers.CreateSession(tx, &db.Session{
			SessionID:            "WOD-20261101-01JEXISTING0000000000000001",
			ProfileID:            profile.ID,
			Status:               db.SessionStatusCompleted,
			WorkoutType:          "wod",
			WODEvaluationVersion: 1,
			WODDescription:       "Keep Me",
			Effort:               &effortEight,
			IdempotencyKey:       "dup-key",
		})
		testhelpers.CreateAnalysisResult(tx, &db.AnalysisResult{
			SessionID:      existingSess.SessionID,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			WODDescription: "Should Not Overwrite",
		})

		dupSess := testhelpers.CreateSession(tx, &db.Session{
			SessionID:      "WOD-20261101-01JDUPKEY00000000000000002",
			ProfileID:      profile.ID,
			Status:         db.SessionStatusStarted,
			WorkoutType:    "wod",
			IdempotencyKey: "dup-key",
		})

		t1 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
		t2 := time.Date(2025, 5, 1, 10, 0, 0, 0, time.UTC)
		t3 := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
		t4 := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)

		ar1 := testhelpers.CreateAnalysisResult(tx, &db.AnalysisResult{
			SessionID:      "WOD-20261102-01JORPHANWOD000000000000001",
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			WODDescription: "Fran",
			CreatedAt:      t1,
		})
		Expect(tx.Model(&ar1).UpdateColumns(map[string]any{
			"created_at": t1,
			"updated_at": t1.Add(time.Minute),
		}).Error).To(Succeed())

		ar2 := testhelpers.CreateAnalysisResult(tx, &db.AnalysisResult{
			SessionID:      "P1-WARMUP-2025-05-01-10-00",
			ProfileID:      profile.ID,
			Status:         "FAILED",
			WODDescription: "",
			CreatedAt:      t2,
		})
		Expect(tx.Model(&ar2).UpdateColumns(map[string]any{
			"created_at": t2,
			"updated_at": t2,
		}).Error).To(Succeed())

		chunkSID := "ACCESSORY-20261103-01JORPHANCHUNK0000001"
		chunk1 := testhelpers.CreateChunkAnalysisResult(tx, &db.ChunkAnalysisResult{
			SessionID:       chunkSID,
			ProfileID:       profile.ID,
			FilePath:        "gs://test-bucket/videos/1/c1.mp4",
			Status:          "COMPLETED",
			ObservedSignals: "{}",
		})
		Expect(tx.Model(&chunk1).UpdateColumns(map[string]any{
			"created_at": t3,
			"updated_at": t3,
		}).Error).To(Succeed())

		chunk2 := testhelpers.CreateChunkAnalysisResult(tx, &db.ChunkAnalysisResult{
			SessionID:       chunkSID,
			ProfileID:       profile.ID,
			FilePath:        "gs://test-bucket/videos/1/c2.mp4",
			Status:          "COMPLETED",
			ObservedSignals: "{}",
		})
		Expect(tx.Model(&chunk2).UpdateColumns(map[string]any{
			"created_at": t3.Add(time.Minute),
			"updated_at": t3.Add(2 * time.Minute),
		}).Error).To(Succeed())

		runSID := "COOLDOWN-20261104-01JORPHANRUN0000000001"
		run := testhelpers.CreateRecordingMediaRun(tx, &db.RecordingMediaRun{
			SessionID: runSID,
			ProfileID: profile.ID,
			Version:   db.RecordingMediaVersion,
			State:     "RECORDING",
			Manifest:  db.JSONDocument(`[]`),
			Intervals: db.JSONDocument(`[]`),
		})
		Expect(tx.Model(&run).UpdateColumns(map[string]any{
			"created_at": t4,
			"updated_at": t4,
		}).Error).To(Succeed())

		Expect(tx.Exec(upSQL).Error).To(Succeed())

		findSession := func(sid string) db.Session {
			var got db.Session
			Expect(tx.Where("session_id = ?", sid).First(&got).Error).To(Succeed())
			return got
		}

		gotExisting := findSession(existingSess.SessionID)
		Expect(gotExisting.WODEvaluationVersion).To(Equal(1))
		Expect(gotExisting.WODDescription).To(Equal("Keep Me"))
		Expect(gotExisting.Effort).NotTo(BeNil())
		Expect(*gotExisting.Effort).To(Equal(8))
		Expect(gotExisting.IdempotencyKey).To(Equal("dup-key"))

		gotDup := findSession(dupSess.SessionID)
		Expect(gotDup.IdempotencyKey).To(Equal(fmt.Sprintf("dup-key:dup:%d", dupSess.ID)))

		gotAR1 := findSession(ar1.SessionID)
		Expect(gotAR1.ProfileID).To(Equal(profile.ID))
		Expect(gotAR1.Status).To(Equal(db.SessionStatus("completed")))
		Expect(gotAR1.WorkoutType).To(Equal("wod"))
		Expect(gotAR1.WODDescription).To(Equal("Fran"))
		Expect(gotAR1.WODEvaluationVersion).To(Equal(0))
		Expect(gotAR1.IdempotencyKey).To(Equal("backfill:" + ar1.SessionID))
		Expect(string(gotAR1.MovementHints)).To(Equal("[]"))
		Expect(gotAR1.CreatedAt.UTC()).To(Equal(t1))
		Expect(gotAR1.UpdatedAt.UTC()).To(Equal(t1.Add(time.Minute)))

		gotAR2 := findSession(ar2.SessionID)
		Expect(gotAR2.Status).To(Equal(db.SessionStatus("failed")))
		Expect(gotAR2.WorkoutType).To(Equal("warmup"))
		Expect(gotAR2.WODEvaluationVersion).To(Equal(0))

		gotChunk := findSession(chunkSID)
		Expect(gotChunk.ProfileID).To(Equal(profile.ID))
		Expect(gotChunk.Status).To(Equal(db.SessionStatus("started")))
		Expect(gotChunk.WorkoutType).To(Equal("accessory"))
		Expect(gotChunk.WODEvaluationVersion).To(Equal(0))
		Expect(gotChunk.CreatedAt.UTC()).To(Equal(t3))
		Expect(gotChunk.UpdatedAt.UTC()).To(Equal(t3.Add(2 * time.Minute)))

		gotRun := findSession(runSID)
		Expect(gotRun.ProfileID).To(Equal(profile.ID))
		Expect(gotRun.Status).To(Equal(db.SessionStatus("started")))
		Expect(gotRun.WorkoutType).To(Equal("cooldown"))
		Expect(gotRun.WODEvaluationVersion).To(Equal(0))
		Expect(gotRun.CreatedAt.UTC()).To(Equal(t4))

		// Re-running upSQL must remain idempotent.
		Expect(tx.Exec(upSQL).Error).To(Succeed())
		var countAfterSecondUp int64
		Expect(tx.Model(&db.Session{}).Count(&countAfterSecondUp).Error).To(Succeed())
		Expect(countAfterSecondUp).To(Equal(int64(6)))

		// Simulate user rating one backfilled session, then verify rollback deletes only unrated backfilled rows.
		Expect(tx.Model(&db.Session{}).Where("session_id = ?", ar1.SessionID).Update("effort", 7).Error).To(Succeed())
		Expect(tx.Exec(downSQL).Error).To(Succeed())

		var deletedCount int64
		Expect(tx.Model(&db.Session{}).Where("session_id IN ?", []string{ar2.SessionID, chunkSID, runSID}).Count(&deletedCount).Error).To(Succeed())
		Expect(deletedCount).To(Equal(int64(0)))

		var remainingCount int64
		Expect(tx.Model(&db.Session{}).Count(&remainingCount).Error).To(Succeed())
		Expect(remainingCount).To(Equal(int64(3)))
	})
})
