package controllers_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/wodeval"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

var _ = Describe("GET /api/v1/history", func() {
	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		testhelpers.FlushRedis()
	})

	It("reuses backfilled session summaries for 20 sessions (2,500 chunks) with zero chunk queries, matches uncached JSON byte-for-byte, and rebuilds only trigger-invalidated sessions", func() {
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "History Cache Athlete"})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())

		queueClient := testhelpers.NewQueueClient()
		DeferCleanup(queueClient.Close)

		const (
			sessionCount    = 20
			chunksPerSess   = 125
			expectedTotRows = sessionCount * chunksPerSess // 2,500 chunks
		)

		workoutProgressJSON := db.NullableJSONDocument(`{"version":1,"clock":"media","source_version":"v1","intervals":[{"kind":"exercise","activity":"Wallball Shot","start_secs":0,"end_secs":30,"reps":15}],"unassessed":[]}`)
		var invalidatedChunkID uint
		var protectedSessionID string
		var protectedPerfRecord db.PerformanceRecord

		// Bulk-seed 20 diverse sessions with 125 chunks each (2,500 total chunks).
		for sIdx := 0; sIdx < sessionCount; sIdx++ {
			sid := fmt.Sprintf("WOD-20261106-01JHISTCACHE%014d", sIdx+1)
			createdAt := time.Date(2026, 11, 6, 10, sIdx, 0, 0, time.UTC)

			sess := testhelpers.CreateSession(dbConn, &db.Session{
				SessionID:                   sid,
				ProfileID:                   profile.ID,
				Status:                      db.SessionStatusStarted,
				WorkoutType:                 "wod",
				WODEvaluationVersion:        wodeval.Version,
				WODDescription:              fmt.Sprintf("AMRAP %d", sIdx+1),
				ActivitySummaryCacheVersion: 0,
			})

			chunkBatch := make([]db.ChunkAnalysisResult, 0, chunksPerSess)
			for cIdx := 0; cIdx < chunksPerSess; cIdx++ {
				start := float64(cIdx * 10)
				end := start + 10.0
				sCopy, eCopy := start, end
				row := db.ChunkAnalysisResult{
					SessionID:       sid,
					ProfileID:       profile.ID,
					FilePath:        fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_%04d.mp4", profile.ID, sid, cIdx),
					Status:          "COMPLETED",
					ExerciseType:    "Wallball Shot",
					ObservedSignals: "{}",
					StartSecs:       &sCopy,
					EndSecs:         &eCopy,
					MediaStartSecs:  &sCopy,
					MediaEndSecs:    &eCopy,
				}

				switch sIdx {
				case 0:
					// Legacy session without movement_observations (Available == false, ReviewState == "unavailable").
					row.MovementObservations = nil
				case 1:
					// Confirmed 0-rep rest session (Available == true, Movements == [], Unassessed == []).
					rawRest, err := json.Marshal(activity.ConfirmedRest(10.0))
					Expect(err).NotTo(HaveOccurred())
					row.ExerciseType = ""
					row.MovementObservations = rawRest
				default:
					rawObs, err := json.Marshal(activity.Observations{
						Version:       activity.Version,
						DurationSecs:  10,
						TargetState:   "identified",
						ActivityState: "exercise",
						Events: []activity.Observation{
							{Movement: "Wallball Shot", Unit: "reps", Start: 2.0, End: 6.0, Complete: true, Evidence: "full squat and target hit"},
						},
						Unassessed: []activity.Interval{},
					})
					Expect(err).NotTo(HaveOccurred())
					row.MovementObservations = rawObs
				}
				chunkBatch = append(chunkBatch, row)
			}
			Expect(dbConn.CreateInBatches(&chunkBatch, 250).Error).To(Succeed())

			if sIdx == 5 {
				invalidatedChunkID = chunkBatch[0].ID
			}

			// Seed initial ReviewState variants (completed, partial, provisional) before backfill.
			switch sIdx {
			case 2:
				protectedSessionID = sid
				reviews := make([]activity.ReviewedChunk, 0, len(chunkBatch))
				for _, cr := range chunkBatch {
					var obs activity.Observations
					Expect(json.Unmarshal(cr.MovementObservations, &obs)).To(Succeed())
					reviews = append(reviews, activity.ReviewedChunk{
						ChunkID:      cr.ID,
						State:        "completed",
						Observations: obs,
					})
				}
				built := activity.Build(chunkBatch, &activity.Summary{
					SourceVersion: activity.SourceVersion(chunkBatch),
					ReviewVersion: activity.Version,
					ReviewState:   "completed",
					Reviews:       reviews,
				})
				rawSummary, err := json.Marshal(built)
				Expect(err).NotTo(HaveOccurred())
				Expect(dbConn.Model(&sess).Updates(map[string]any{
					"activity_summary":               db.JSONDocument(rawSummary),
					"activity_summary_cache_version": 0, // Still unbackfilled!
				}).Error).To(Succeed())
			case 3:
				reviews := make([]activity.ReviewedChunk, 0, len(chunkBatch))
				for idx, cr := range chunkBatch {
					if idx == 0 {
						reviews = append(reviews, activity.ReviewedChunk{ChunkID: cr.ID, State: "failed"})
						continue
					}
					var obs activity.Observations
					Expect(json.Unmarshal(cr.MovementObservations, &obs)).To(Succeed())
					reviews = append(reviews, activity.ReviewedChunk{
						ChunkID:      cr.ID,
						State:        "completed",
						Observations: obs,
					})
				}
				built := activity.Build(chunkBatch, &activity.Summary{
					SourceVersion: activity.SourceVersion(chunkBatch),
					ReviewVersion: activity.Version,
					ReviewState:   "partial",
					Reviews:       reviews,
				})
				rawSummary, err := json.Marshal(built)
				Expect(err).NotTo(HaveOccurred())
				Expect(dbConn.Model(&sess).Updates(map[string]any{
					"activity_summary":               db.JSONDocument(rawSummary),
					"activity_summary_cache_version": 0,
				}).Error).To(Succeed())
			}

			analysisStatus := "COMPLETED"
			if sIdx == 4 {
				analysisStatus = "PROCESSING" // In-progress session
			}
			var wp db.NullableJSONDocument
			if sIdx == 2 {
				wp = workoutProgressJSON
			}
			createdAnalysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:       sid,
				ProfileID:       profile.ID,
				AnalysisType:    db.AnalysisTypeWOD,
				Status:          analysisStatus,
				Output:          fmt.Sprintf("analysis output %d", sIdx),
				AvailableVideos: db.CommaStringArray{"merged"},
				WorkoutProgress: wp,
				CreatedAt:       createdAt,
			})
			if sIdx == 2 {
				reps := 15
				protectedPerfRecord = testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{
					AnalysisID: createdAnalysis.ID,
					ProfileID:  profile.ID,
					SessionID:  sid,
					Movement:   "Wallball Shot",
					StartSecs:  0,
					EndSecs:    30,
					Reps:       &reps,
				})
				testhelpers.CreateHighlightResult(dbConn, &db.HighlightResult{
					ProfileID: profile.ID,
					SessionID: sid,
					Title:     "Wallball Streak",
					Status:    "COMPLETED",
				})
			}
		}

		// Step 1: Dry-run backfill must report WouldUpdate == 20 and mutate nothing.
		dryStats, err := activity.BackfillSummaries(context.Background(), dbConn, activity.BackfillOptions{
			BatchSize: 8,
			ProfileID: profile.ID,
			Apply:     false,
		}, zap.NewNop())
		Expect(err).NotTo(HaveOccurred())
		Expect(dryStats.ScannedSessions).To(Equal(sessionCount))
		Expect(dryStats.WouldUpdate).To(Equal(sessionCount))
		Expect(dryStats.Updated).To(Equal(0))
		Expect(dryStats.ChunksLoaded).To(Equal(expectedTotRows))

		// Step 2: Apply backfill in small sequential batches -> Updated == 20, workout_progress & performance_records untouched.
		applyStats, err := activity.BackfillSummaries(context.Background(), dbConn, activity.BackfillOptions{
			BatchSize: 8,
			ProfileID: profile.ID,
			Apply:     true,
		}, zap.NewNop())
		Expect(err).NotTo(HaveOccurred())
		Expect(applyStats.ScannedSessions).To(Equal(sessionCount))
		Expect(applyStats.Updated).To(Equal(sessionCount))
		Expect(applyStats.AlreadyValid).To(Equal(0))

		var verifyAnalysis db.AnalysisResult
		Expect(dbConn.Where("profile_id = ? AND session_id = ?", profile.ID, protectedSessionID).First(&verifyAnalysis).Error).To(Succeed())
		Expect([]byte(verifyAnalysis.WorkoutProgress)).To(MatchJSON([]byte(workoutProgressJSON)))
		var verifyPerf db.PerformanceRecord
		Expect(dbConn.First(&verifyPerf, protectedPerfRecord.ID).Error).To(Succeed())
		Expect(*verifyPerf.Reps).To(Equal(15))

		// Helper to invoke GET /api/v1/history with a custom cache setting and capture structured logs.
		callHistory := func(cacheEnabled bool, acceptGzip bool) (*httptest.ResponseRecorder, []byte, *observer.ObservedLogs) {
			core, logs := observer.New(zapcore.InfoLevel)
			zlog := zap.New(core)
			prevLog := logger.Log
			logger.Log = zlog
			defer func() { logger.Log = prevLog }()

			storageClient, sErr := testhelpers.NewStorageClient("test-bucket", testhelpers.NewMockTransport())
			Expect(sErr).NotTo(HaveOccurred())
			boolVal := cacheEnabled
			ctl, err := controllers.New(controllers.Config{
				DB:                         dbConn,
				StorageClient:              storageClient,
				BucketName:                 "test-bucket",
				QueueClient:                queueClient,
				AnalysisResults:            controllers.NewGormAnalysisResultRepository(dbConn),
				Profiles:                   controllers.NewGormProfileRepository(dbConn),
				HighlightResults:           controllers.NewGormHighlightResultRepository(dbConn),
				HistorySummaryCacheEnabled: &boolVal,
			})
			Expect(err).NotTo(HaveOccurred())
			r, err := server.SetupRouter("test", nil, ctl, nil, authService)
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&limit=20", profile.ID), nil)
			if acceptGzip {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			authorizeRequest(req, &user)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

			rawBody := rec.Body.Bytes()
			if rec.Header().Get("Content-Encoding") == "gzip" {
				gr, gErr := gzip.NewReader(rec.Body)
				Expect(gErr).NotTo(HaveOccurred())
				rawBody, gErr = io.ReadAll(gr)
				Expect(gErr).NotTo(HaveOccurred())
				Expect(gr.Close()).To(Succeed())
			}
			return rec, rawBody, logs
		}

		// Step 3: Full cache hit (20/20) vs uncached rebuild -> 0 chunk rows loaded, 0 tasks enqueued, 100% identical JSON, Gzip active!
		cachedRec, cachedBody, cachedLogs := callHistory(true, true)
		Expect(cachedRec.Header().Get("Content-Encoding")).To(Equal("gzip"))
		_, uncachedBody, uncachedLogs := callHistory(false, false)

		Expect(cachedBody).To(MatchJSON(uncachedBody))

		hitEntries := cachedLogs.FilterMessage("history fetched").All()
		Expect(hitEntries).To(HaveLen(1))
		hitFields := hitEntries[0].ContextMap()
		Expect(hitFields["sessions_total"]).To(Equal(int64(20)))
		Expect(hitFields["summary_cache_hits"]).To(Equal(int64(20)))
		Expect(hitFields["summary_rebuilt"]).To(Equal(int64(0)))
		Expect(hitFields["chunk_rows_loaded"]).To(Equal(int64(0)))

		missEntries := uncachedLogs.FilterMessage("history fetched").All()
		Expect(missEntries).To(HaveLen(1))
		Expect(missEntries[0].ContextMap()["summary_cache_hits"]).To(Equal(int64(0)))
		Expect(missEntries[0].ContextMap()["summary_rebuilt"]).To(Equal(int64(20)))
		Expect(missEntries[0].ContextMap()["chunk_rows_loaded"]).To(Equal(int64(expectedTotRows)))

		// GET /api/v1/history must NEVER enqueue background analysis tasks.
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())

		// Step 4: Mutate 1 chunk in DB -> 000064 trigger invalidates ONLY that 1 session -> 19 cache hits, 1 rebuild (125 chunks), identical JSON!
		updatedObs, err := json.Marshal(activity.Observations{
			Version:       activity.Version,
			DurationSecs:  10,
			TargetState:   "identified",
			ActivityState: "exercise",
			Events: []activity.Observation{
				{Movement: "Wallball Shot", Unit: "reps", Start: 1.5, End: 4.5, Complete: true, Evidence: "rep 1"},
				{Movement: "Wallball Shot", Unit: "reps", Start: 5.5, End: 8.5, Complete: true, Evidence: "rep 2"},
			},
			Unassessed: []activity.Interval{},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(dbConn.Model(&db.ChunkAnalysisResult{}).Where("id = ?", invalidatedChunkID).Update("movement_observations", db.NullableJSONDocument(updatedObs)).Error).To(Succeed())

		_, mixedBody, mixedLogs := callHistory(true, false)
		_, uncachedAfterUpdateBody, _ := callHistory(false, false)
		Expect(mixedBody).To(MatchJSON(uncachedAfterUpdateBody))

		mixedFields := mixedLogs.FilterMessage("history fetched").All()[0].ContextMap()
		Expect(mixedFields["sessions_total"]).To(Equal(int64(20)))
		Expect(mixedFields["summary_cache_hits"]).To(Equal(int64(19)))
		Expect(mixedFields["summary_rebuilt"]).To(Equal(int64(1)))
		Expect(mixedFields["chunk_rows_loaded"]).To(Equal(int64(chunksPerSess)))
	})
})

var _ = Describe("POST /api/v1/history/:id/unarchive", func() {
	It("preserves cache validity when archiving and restoring a provisional session so GET /api/v1/history serves unavailable without chunk queries", func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)

		profile := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Archive Restore Athlete"})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())

		sid := "WOD-20261106-01JARCHIVERESTORE0001"
		s0, e0 := 0.0, 10.0
		obsRaw, err := json.Marshal(activity.Observations{
			Version:       activity.Version,
			DurationSecs:  10,
			TargetState:   "identified",
			ActivityState: "exercise",
			Events: []activity.Observation{
				{Movement: "Burpee", Unit: "reps", Start: 2, End: 6, Complete: true, Evidence: "chest to deck"},
			},
			Unassessed: []activity.Interval{},
		})
		Expect(err).NotTo(HaveOccurred())

		sess := testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:   sid,
			ProfileID:   profile.ID,
			Status:      db.SessionStatusCompleted,
			WorkoutType: "wod",
		})
		chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:            sid,
			ProfileID:            profile.ID,
			FilePath:             fmt.Sprintf("gs://test-bucket/videos/%d/%s/c1.mp4", profile.ID, sid),
			Status:               "COMPLETED",
			ExerciseType:         "Burpee",
			ObservedSignals:      "{}",
			StartSecs:            &s0,
			EndSecs:              &e0,
			MediaStartSecs:       &s0,
			MediaEndSecs:         &e0,
			MovementObservations: obsRaw,
		})
		provisional := activity.Build([]db.ChunkAnalysisResult{chunk}, nil)
		Expect(provisional.ReviewState).To(Equal("provisional"))
		rawSummary, err := json.Marshal(provisional)
		Expect(err).NotTo(HaveOccurred())
		Expect(dbConn.Model(&sess).Updates(map[string]any{
			"activity_summary":               db.JSONDocument(rawSummary),
			"activity_summary_cache_version": activity.SummaryCacheVersion,
		}).Error).To(Succeed())

		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: sid,
			ProfileID: profile.ID,
			Status:    "COMPLETED",
			Output:    "done",
		})

		routerCached := newTestRouter(controllers.Config{})
		archiveResp := historyArchiveRequest(routerCached, user, http.MethodPost, fmt.Sprintf("/api/v1/history/%d/archive", analysis.ID), "")
		Expect(archiveResp.Code).To(Equal(http.StatusOK), archiveResp.Body.String())

		unarchiveResp := historyArchiveRequest(routerCached, user, http.MethodPost, fmt.Sprintf("/api/v1/history/%d/unarchive", analysis.ID), "")
		Expect(unarchiveResp.Code).To(Equal(http.StatusOK), unarchiveResp.Body.String())

		var reloaded db.Session
		Expect(dbConn.First(&reloaded, sess.ID).Error).To(Succeed())
		Expect(reloaded.ActivitySummaryCacheVersion).To(Equal(activity.SummaryCacheVersion))

		core, logs := observer.New(zapcore.InfoLevel)
		prevLog := logger.Log
		logger.Log = zap.New(core)
		defer func() { logger.Log = prevLog }()

		histCached := historyArchiveRequest(routerCached, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profile.ID), "")
		Expect(histCached.Code).To(Equal(http.StatusOK))

		disableCache := false
		routerUncached := newTestRouter(controllers.Config{HistorySummaryCacheEnabled: &disableCache})
		histUncached := historyArchiveRequest(routerUncached, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profile.ID), "")
		Expect(histUncached.Code).To(Equal(http.StatusOK))

		Expect(histCached.Body.Bytes()).To(MatchJSON(histUncached.Body.Bytes()))
		fetched := logs.FilterMessage("history fetched").All()
		Expect(fetched).To(HaveLen(1))
		Expect(fetched[0].ContextMap()["summary_cache_hits"]).To(Equal(int64(1)))
		Expect(fetched[0].ContextMap()["chunk_rows_loaded"]).To(Equal(int64(0)))
	})
})
