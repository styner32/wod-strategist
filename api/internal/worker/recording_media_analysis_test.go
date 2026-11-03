package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ = Describe("Recording media analysis admission and recovery", Ordered, func() {
	var database *gorm.DB
	var queue *asynq.Client
	var inspector *asynq.Inspector
	var transport *testhelpers.MockTransport
	var w *Worker
	ctx := context.Background()

	BeforeAll(func() {
		var err error
		database, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		queue = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		DeferCleanup(queue.Close)
		DeferCleanup(inspector.Close)
		sqlDB, err := database.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
	})
	BeforeEach(func() {
		testhelpers.CleanupDB(database)
		testhelpers.FlushRedis()
		transport = testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		geminiClient, err := gemini.NewClientWithOptions(ctx, zap.NewNop(), gemini.Options{
			APIKey: "test-key", BaseURL: "https://generativelanguage.googleapis.com",
			HTTPClient: &http.Client{Transport: transport},
		})
		Expect(err).NotTo(HaveOccurred())
		w = NewWorker(database, storageClient, "test-bucket", geminiClient, queue, zap.NewNop())
		w.QueueInspector = inspector
	})
	AfterEach(func() {
		Expect(transport.Verify()).To(Succeed())
		// These admission/recovery paths must stop before any download or paid request.
		for _, request := range transport.Requests() {
			Expect(request.URL).NotTo(ContainSubstring("generativelanguage.googleapis.com"))
			Expect(request.URL).NotTo(ContainSubstring("storage.googleapis.com"))
		}
	})

	preparedRun := func(state string) (db.RecordingMediaRun, db.AnalysisVideoPreparation, VideoAnalysisPayload) {
		profile := testhelpers.CreateProfile(database, &db.Profile{})
		const sid = "WOD-20261103-01JRECORDINGANALYSIS000000"
		merged := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, sid)
		run := testhelpers.CreateRecordingMediaRun(database, &db.RecordingMediaRun{
			ProfileID: profile.ID, SessionID: sid, Version: db.RecordingMediaVersion,
			State: state, MergedObject: merged, MergedGeneration: 51, MergedBytes: 1024,
			AnalysisDispatched: true,
		})
		prep := testhelpers.CreateAnalysisVideoPreparation(database, &db.AnalysisVideoPreparation{
			RunID: run.ID, SourceObject: merged, SourceGeneration: run.MergedGeneration,
			PresetVersion: recordingAnalysisPreset, State: "READY",
			OutputObject:     fmt.Sprintf("videos/%d/%s/derived/analysis/%d/prepared.mp4", profile.ID, sid, run.ID),
			OutputGeneration: 52, OutputBytes: 512,
		})
		payload := VideoAnalysisPayload{
			SessionID: sid, ProfileID: profile.ID, WorkoutType: WorkoutTypeWOD,
			MediaRunID: run.ID, AnalysisPreparationID: prep.ID, MediaDispatchVersion: run.DispatchVersion,
			FilePath: "gs://test-bucket/" + prep.OutputObject, SourceGeneration: prep.OutputGeneration,
		}
		return run, prep, payload
	}

	It("ignores a duplicate delivery after the recording analysis run has completed", func() {
		run, _, payload := preparedRun("COMPLETED")
		Expect(w.HandleVideoAnalysisTask(ctx, makeVideoAnalysisTask(payload))).To(Succeed())
		var current db.RecordingMediaRun
		Expect(database.First(&current, run.ID).Error).To(Succeed())
		Expect(current.State).To(Equal("COMPLETED"))
		Expect(current.AnalysisAttempts).To(BeZero())
		Expect(current.LeaseToken).To(BeEmpty())
	})

	It("publishes a history failure when missing AI deliveries exhaust their attempt budget", func() {
		run, _, _ := preparedRun("ANALYZING")
		run.AnalysisAttempts = recordingMediaMaxAttempts
		Expect(database.Model(&run).Update("analysis_attempts", run.AnalysisAttempts).Error).To(Succeed())
		Expect(w.recoverRecordingVideoAnalysis(ctx, run)).To(Succeed())
		var current db.RecordingMediaRun
		Expect(database.First(&current, run.ID).Error).To(Succeed())
		Expect(current.State).To(Equal("FAILED"))
		Expect(current.FailedStage).To(Equal("analysis"))
		var history db.AnalysisResult
		Expect(database.Where("profile_id = ? AND session_id = ?", run.ProfileID, run.SessionID).First(&history).Error).To(Succeed())
		Expect(history.Status).To(Equal("FAILED"))
		Expect(history.AvailableVideos).To(ContainElement("merged"))
	})

	It("repairs a lost completion checkpoint before a retry can call Gemini again", func() {
		run, _, payload := preparedRun("ANALYZING")
		expired := time.Now().UTC().Add(-time.Minute)
		Expect(database.Model(&run).Updates(map[string]any{
			"analysis_attempts": 1, "lease_token": "previous-attempt", "lease_expires_at": expired,
		}).Error).To(Succeed())
		testhelpers.CreateAnalysisResult(database, &db.AnalysisResult{
			SessionID: run.SessionID, ProfileID: run.ProfileID, Status: "COMPLETED", Output: "already published",
		})
		Expect(w.HandleVideoAnalysisTask(WithRetryCount(ctx, 1), makeVideoAnalysisTask(payload))).To(Succeed())
		var current db.RecordingMediaRun
		Expect(database.First(&current, run.ID).Error).To(Succeed())
		Expect(current.State).To(Equal("COMPLETED"))
		Expect(current.AnalysisAttempts).To(Equal(1))
		Expect(current.LeaseToken).To(BeEmpty())
		Expect(current.LeaseExpiresAt).To(BeNil())
	})

	It("rejects a delivery for a different prepared source generation before claiming an attempt", func() {
		run, _, payload := preparedRun("READY_FOR_ANALYSIS")
		payload.SourceGeneration++
		err := w.HandleVideoAnalysisTask(ctx, makeVideoAnalysisTask(payload))
		Expect(errors.Is(err, asynq.SkipRetry)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("does not match prepared generation")))
		var current db.RecordingMediaRun
		Expect(database.First(&current, run.ID).Error).To(Succeed())
		Expect(current.State).To(Equal("READY_FOR_ANALYSIS"))
		Expect(current.AnalysisAttempts).To(BeZero())
		Expect(current.LeaseToken).To(BeEmpty())
	})

	It("ignores an older dispatch epoch even when its Asynq retry count would fail a new analysis", func() {
		run, _, payload := preparedRun("READY_FOR_ANALYSIS")
		Expect(database.Model(&run).Update("dispatch_version", 2).Error).To(Succeed())
		payload.MediaDispatchVersion = 1
		Expect(w.HandleVideoAnalysisTask(WithRetryCount(ctx, 3), makeVideoAnalysisTask(payload))).To(Succeed())
		var current db.RecordingMediaRun
		Expect(database.First(&current, run.ID).Error).To(Succeed())
		Expect(current.State).To(Equal("READY_FOR_ANALYSIS"))
		Expect(current.DispatchVersion).To(Equal(int64(2)))
		Expect(current.AnalysisAttempts).To(BeZero())
		Expect(current.LeaseToken).To(BeEmpty())
	})

	DescribeTable("fences automatic publication after its media lease becomes stale without archive", func(replaceToken bool) {
		run := testhelpers.CreateRecordingMediaRun(database, &db.RecordingMediaRun{
			Version: db.RecordingMediaVersion, State: "MERGING",
		})
		token, err := w.claimRecordingMedia(ctx, run, "recording_media_runs", run.ID, run.DispatchVersion, "MERGING")
		Expect(err).NotTo(HaveOccurred())
		workCtx, done, err := w.recordingMediaContext(ctx, run, "recording_media_runs", run.ID, token)
		Expect(err).NotTo(HaveOccurred())
		defer done()
		if replaceToken {
			Expect(database.Model(&run).Update("lease_token", "replacement-attempt").Error).To(Succeed())
		} else {
			Expect(database.Model(&run).Update("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error).To(Succeed())
		}
		published := false
		err = w.scopedSessionWorker(workCtx).withSessionPublication(workCtx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
			published = true
			return tx.Model(&db.RecordingMediaRun{}).Where("id = ?", run.ID).Update("safe_error", "stale publication").Error
		})
		Expect(err).To(MatchError(db.ErrSessionWorkCancelled))
		Expect(published).To(BeFalse())
		var current db.RecordingMediaRun
		Expect(database.First(&current, run.ID).Error).To(Succeed())
		Expect(current.SafeError).To(BeEmpty())
		Expect(current.State).To(Equal("MERGING"))
	}, Entry("superseded token", true), Entry("expired token", false))

	It("checks a publication lease on the existing transaction without borrowing another connection", func() {
		run, _, _ := preparedRun("MERGING")
		sqlDB, err := database.DB()
		Expect(err).NotTo(HaveOccurred())
		previousLimit := sqlDB.Stats().MaxOpenConnections
		sqlDB.SetMaxOpenConns(1)
		DeferCleanup(sqlDB.SetMaxOpenConns, previousLimit)
		limited, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		token, err := w.claimRecordingMedia(limited, run, "recording_media_runs", run.ID, run.DispatchVersion, "MERGING")
		Expect(err).NotTo(HaveOccurred())
		guarded, done, err := w.recordingMediaContext(limited, run, "recording_media_runs", run.ID, token)
		Expect(err).NotTo(HaveOccurred())
		defer done()
		called := false
		Expect(w.scopedSessionWorker(guarded).withSessionPublication(guarded, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
			called = true
			return tx.Model(&run).Update("safe_error", "lease checked").Error
		})).To(Succeed())
		Expect(called).To(BeTrue())
	})

	DescribeTable("leaves live queue deliveries owned by Asynq", func(delay time.Duration, expectedState asynq.TaskState) {
		run, prep, payload := preparedRun("READY_FOR_ANALYSIS")
		opts := []asynq.Option{asynq.TaskID(recordingAnalysisTaskID(run.ID, prep.ID, run.DispatchVersion))}
		if delay > 0 {
			opts = append(opts, asynq.ProcessIn(delay))
		}
		info, err := queue.Enqueue(makeVideoAnalysisTask(payload), opts...)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.State).To(Equal(expectedState))
		Expect(w.recoverRecordingVideoAnalysis(ctx, run)).To(Succeed())
		var current db.RecordingMediaRun
		Expect(database.First(&current, run.ID).Error).To(Succeed())
		Expect(current.State).To(Equal("READY_FOR_ANALYSIS"))
		Expect(current.DispatchVersion).To(Equal(run.DispatchVersion))
		Expect(current.AnalysisDispatched).To(BeTrue())
	}, Entry("pending", time.Duration(0), asynq.TaskStatePending),
		Entry("scheduled retry ownership", time.Hour, asynq.TaskStateScheduled))

	It("repairs an archived pre-claim analysis delivery instead of stranding the ready outbox", func() {
		run, prep, payload := preparedRun("READY_FOR_ANALYSIS")
		id := recordingAnalysisTaskID(run.ID, prep.ID, run.DispatchVersion)
		_, err := queue.Enqueue(makeVideoAnalysisTask(payload), asynq.TaskID(id), asynq.Retention(24*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(inspector.ArchiveTask("default", id)).To(Succeed())
		Expect(w.recoverRecordingVideoAnalysis(ctx, run)).To(Succeed())
		var current db.RecordingMediaRun
		Expect(database.First(&current, run.ID).Error).To(Succeed())
		Expect(current.State).To(Equal("READY_FOR_ANALYSIS"))
		Expect(current.AnalysisDispatched).To(BeFalse())
		Expect(current.DispatchVersion).To(Equal(run.DispatchVersion + 1))
		Expect(current.AnalysisAttempts).To(BeZero())
		Expect(DispatchRecordingMedia(ctx, database, queue, run.ID)).To(Succeed())
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(ContainElement(HaveField("Type", TypeCoordinateRecordingMedia)))
	})
})
