package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ = Describe("Recording media durable orchestration", Ordered, func() {
	var database *gorm.DB
	var queue *asynq.Client
	var inspector *asynq.Inspector
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
		w = &Worker{DB: database, QueueClient: queue, QueueInspector: inspector, BucketName: "test-bucket", logger: zap.NewNop()}
	})
	newRun := func(state string) db.RecordingMediaRun {
		return testhelpers.CreateRecordingMediaRun(database, &db.RecordingMediaRun{Version: db.RecordingMediaVersion, State: state, Payload: recordingJSON(VideoAnalysisPayload{WorkoutType: WorkoutTypeWOD})})
	}
	newChunk := func(run db.RecordingMediaRun, order int, profile chunkStreamProfile, target recordingConcatProfile) (db.RecordingChunk, db.ChunkMediaPreparation) {
		chunk := testhelpers.CreateRecordingChunk(database, &db.RecordingChunk{RunID: run.ID, CaptureOrder: order, ProbeReady: true, SourceProfile: recordingJSON(profile), AIDispatched: true})
		prep := testhelpers.CreateChunkMediaPreparation(database, &db.ChunkMediaPreparation{RunID: run.ID, ChunkID: chunk.ID, State: "READY", ResolvedProfile: recordingJSON(target)})
		return chunk, prep
	}
	seal := func(run *db.RecordingMediaRun, chunks ...db.RecordingChunk) {
		entries := make([]db.RecordingMediaManifestEntry, len(chunks))
		for i, chunk := range chunks {
			entries[i] = db.RecordingMediaManifestEntry{ChunkID: chunk.ID, SourceGeneration: chunk.SourceGeneration}
		}
		run.Manifest = recordingJSON(entries)
		Expect(database.Model(run).Update("manifest", run.Manifest).Error).To(Succeed())
	}

	It("dispatches independent chunk AI and bounded media preparation once", func() {
		run := newRun("RECORDING")
		chunk := testhelpers.CreateRecordingChunk(database, &db.RecordingChunk{RunID: run.ID, AIPayload: recordingJSON(VideoAnalysisPayload{SessionID: run.SessionID, ProfileID: run.ProfileID, FilePath: "gs://test-bucket/source.mp4"})})
		prep := testhelpers.CreateChunkMediaPreparation(database, &db.ChunkMediaPreparation{ChunkID: chunk.ID})
		Expect(DispatchRecordingMedia(ctx, database, queue, run.ID)).To(Succeed())
		Expect(DispatchRecordingMedia(ctx, database, queue, run.ID)).To(Succeed())
		media, err := inspector.ListPendingTasks(RecordingChunkQueueName)
		Expect(err).NotTo(HaveOccurred())
		Expect(media).To(HaveLen(1))
		Expect(media[0].Timeout).To(Equal(5 * time.Minute))
		Expect(media[0].MaxRetry).To(BeZero())
		var body recordingMediaTask
		Expect(json.Unmarshal(media[0].Payload, &body)).To(Succeed())
		Expect(body.PreparationID).To(Equal(prep.ID))
		ai, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(ai).To(HaveLen(1))
		var payload VideoAnalysisPayload
		Expect(json.Unmarshal(ai[0].Payload, &payload)).To(Succeed())
		Expect(payload.MediaRunID).To(Equal(run.ID))
		Expect(payload.RecordingChunkID).To(Equal(chunk.ID))
		Expect(payload.SourceGeneration).To(Equal(chunk.SourceGeneration))
	})

	It("reuses native canonical chunks without changing homogeneous resolution", func() {
		run := newRun("WAITING_PREPARATION")
		profile := chunkStreamProfile{Width: 2160, Height: 3840, DisplayWidth: 2160, DisplayHeight: 3840, VideoCodec: "hevc", PixFmt: "yuv420p", VideoTimeBase: "1/600", VideoTimescale: 600}
		chunk, _ := newChunk(run, 1, profile, recordingConcatProfile{2160, 3840, false})
		seal(&run, chunk)
		Expect(w.coordinateRecordingPreparation(ctx, run)).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("MERGING"))
		var output recordingConcatProfile
		Expect(json.Unmarshal(run.OutputProfile, &output)).To(Succeed())
		Expect(output.Width).To(Equal(2160))
		Expect(output.Height).To(Equal(3840))
		var count int64
		Expect(database.Model(&db.ChunkMediaPreparation{}).Where("run_id = ?", run.ID).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
	})

	It("schedules incompatible audio preparation from the original generation", func() {
		run := newRun("WAITING_PREPARATION")
		profile := chunkStreamProfile{Width: 64, Height: 64, DisplayWidth: 64, DisplayHeight: 64, VideoCodec: "h264", PixFmt: "yuv420p", VideoTimeBase: "1/30000", VideoTimescale: 30000}
		first, _ := newChunk(run, 1, profile, recordingConcatProfile{64, 64, false})
		profile.HasAudio = true
		profile.AudioCodec = "aac"
		profile.SampleRate = 44100
		profile.Channels = 2
		second, _ := newChunk(run, 2, profile, recordingConcatProfile{64, 64, true})
		seal(&run, first, second)
		Expect(w.coordinateRecordingPreparation(ctx, run)).To(Succeed())
		var pending []db.ChunkMediaPreparation
		Expect(database.Where("run_id = ? AND state = ?", run.ID, "PENDING").Find(&pending).Error).To(Succeed())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].ChunkID).To(Equal(first.ID))
		Expect(pending[0].SourceGeneration).To(Equal(first.SourceGeneration))
		Expect(pending[0].OutputObject).To(BeEmpty())
		var output recordingConcatProfile
		Expect(json.Unmarshal(pending[0].ResolvedProfile, &output)).To(Succeed())
		Expect(output.IncludeAudio).To(BeTrue())
	})

	It("allows one claim and preserves verified staging when its lease expires", func() {
		run := newRun("MERGING")
		Expect(database.Model(&run).Updates(map[string]any{"merged_staging_object": "videos/staging.mp4", "merged_staging_generation": int64(17)}).Error).To(Succeed())
		token, err := w.claimRecordingMedia(ctx, run, "recording_media_runs", run.ID, 0, "MERGING")
		Expect(err).NotTo(HaveOccurred())
		_, err = w.claimRecordingMedia(ctx, run, "recording_media_runs", run.ID, 0, "MERGING")
		Expect(err).To(MatchError(errRecordingMediaStale))
		Expect(w.failRecordingMedia(ctx, run, "recording_media_runs", run.ID, token, "merge", errors.New("lease expired"))).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("MERGING"))
		Expect(run.LeaseToken).To(BeEmpty())
		Expect(run.DispatchVersion).To(Equal(int64(1)))
		Expect(run.MergedStagingGeneration).To(Equal(int64(17)))
		Expect(run.MergedStagingObject).To(Equal("videos/staging.mp4"))
		Expect(w.failRecordingMedia(ctx, run, "recording_media_runs", run.ID, token, "merge", errors.New("late failure"))).To(Succeed())
		var fresh db.RecordingMediaRun
		Expect(database.First(&fresh, run.ID).Error).To(Succeed())
		Expect(fresh.DispatchVersion).To(Equal(run.DispatchVersion))
	})

	It("repairs a retained archived delivery that failed before claiming", func() {
		run := newRun("RECORDING")
		chunk := testhelpers.CreateRecordingChunk(database, &db.RecordingChunk{RunID: run.ID, AIDispatched: true})
		prep := testhelpers.CreateChunkMediaPreparation(database, &db.ChunkMediaPreparation{ChunkID: chunk.ID})
		Expect(DispatchRecordingMedia(ctx, database, queue, run.ID)).To(Succeed())
		id := recordingMediaTaskID(TypePrepareRecordingChunk, recordingMediaTask{run.ID, prep.ID, 0})
		Expect(inspector.ArchiveTask(RecordingChunkQueueName, id)).To(Succeed())
		w.recoverRecordingPendingDeliveries(ctx, run)
		Expect(database.First(&prep, prep.ID).Error).To(Succeed())
		Expect(prep.DispatchVersion).To(Equal(int64(1)))
		Expect(DispatchRecordingMedia(ctx, database, queue, run.ID)).To(Succeed())
		pending, err := inspector.ListPendingTasks(RecordingChunkQueueName)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].ID).NotTo(Equal(id))
	})

	It("keeps capture admission open after an early preparation failure and reports it after sealing", func() {
		run := newRun("RECORDING")
		profile := chunkStreamProfile{Width: 64, Height: 64, DisplayWidth: 64, DisplayHeight: 64, VideoCodec: "h264", VideoTimeBase: "1/30000", VideoTimescale: 30000}
		first := testhelpers.CreateRecordingChunk(database, &db.RecordingChunk{RunID: run.ID, ProbeReady: true, SourceProfile: recordingJSON(profile), AIDispatched: true})
		prep := testhelpers.CreateChunkMediaPreparation(database, &db.ChunkMediaPreparation{ChunkID: first.ID, State: "RUNNING", Attempts: recordingMediaMaxAttempts, LeaseToken: "failed-native"})
		Expect(w.failRecordingMedia(ctx, run, "chunk_media_preparations", prep.ID, prep.LeaseToken, "chunk_preparation", errors.New("broken chunk"))).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("RECORDING"))
		Expect(database.First(&prep, prep.ID).Error).To(Succeed())
		Expect(prep.State).To(Equal("FAILED"))
		var beforeSeal int64
		Expect(database.Model(&db.AnalysisResult{}).Where("profile_id = ? AND session_id = ?", run.ProfileID, run.SessionID).Count(&beforeSeal).Error).To(Succeed())
		Expect(beforeSeal).To(BeZero())
		second, _ := newChunk(run, 2, profile, recordingConcatProfile{64, 64, false})
		seal(&run, first, second)
		Expect(database.Model(&run).Update("state", "WAITING_PREPARATION").Error).To(Succeed())
		run.State = "WAITING_PREPARATION"
		Expect(w.HandleCoordinateRecordingMediaTask(ctx, asynq.NewTask(TypeCoordinateRecordingMedia, recordingJSON(recordingMediaTask{RunID: run.ID})))).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("FAILED"))
		Expect(run.FailedStage).To(Equal("chunk_preparation"))
		var failed db.AnalysisResult
		Expect(database.Where("profile_id = ? AND session_id = ?", run.ProfileID, run.SessionID).First(&failed).Error).To(Succeed())
		Expect(failed.Status).To(Equal("FAILED"))
	})

	It("publishes sealed preparation failure in history while retaining verified playback", func() {
		run := newRun("PREPARING_ANALYSIS")
		chunk := testhelpers.CreateRecordingChunk(database, &db.RecordingChunk{RunID: run.ID, AIDispatched: true})
		seal(&run, chunk)
		run.MergedObject = fmt.Sprintf("videos/%d/%s/merged.mp4", run.ProfileID, run.SessionID)
		run.MergedGeneration = 13
		Expect(database.Model(&run).Updates(map[string]any{"merged_object": run.MergedObject, "merged_generation": run.MergedGeneration}).Error).To(Succeed())
		prep := testhelpers.CreateAnalysisVideoPreparation(database, &db.AnalysisVideoPreparation{RunID: run.ID, SourceObject: run.MergedObject, SourceGeneration: run.MergedGeneration, State: "RUNNING", Attempts: recordingMediaMaxAttempts, LeaseToken: "failed-analysis-preparation"})
		Expect(w.failRecordingMedia(ctx, run, "analysis_video_preparations", prep.ID, prep.LeaseToken, "analysis_preparation", errors.New("encode failed"))).To(Succeed())
		var failed db.AnalysisResult
		Expect(database.Where("profile_id = ? AND session_id = ?", run.ProfileID, run.SessionID).First(&failed).Error).To(Succeed())
		Expect(failed.Status).To(Equal("FAILED"))
		Expect(failed.AvailableVideos).To(ContainElement("merged"))
		Expect(database.Model(&failed).Updates(map[string]any{"status": "COMPLETED", "output": "completed after recovery"}).Error).To(Succeed())
		Expect(w.persistRecordingMediaFailure(ctx, run.ID)).To(Succeed())
		Expect(database.First(&failed, failed.ID).Error).To(Succeed())
		Expect(failed.Status).To(Equal("COMPLETED"))
		Expect(failed.Output).To(Equal("completed after recovery"))
	})

	It("fences overlong analysis immediately but finalizes partial activity only after terminal chunks", func() {
		run := newRun("MERGED")
		session := testhelpers.CreateSession(database, &db.Session{ProfileID: run.ProfileID, SessionID: run.SessionID, WorkoutType: WorkoutTypeWOD})
		chunk := testhelpers.CreateRecordingChunk(database, &db.RecordingChunk{RunID: run.ID, AIDispatched: true})
		seal(&run, chunk)
		run.MergedObject = fmt.Sprintf("videos/%d/%s/merged.mp4", run.ProfileID, run.SessionID)
		run.MergedGeneration = 19
		run.MergedBytes = 1024
		run.Intervals = recordingJSON([]concatMediaInterval{{FilePath: chunk.SourceURI, Start: 0, End: 5460}})
		Expect(database.Model(&run).Updates(map[string]any{"merged_object": run.MergedObject, "merged_generation": run.MergedGeneration, "merged_bytes": run.MergedBytes, "intervals": run.Intervals}).Error).To(Succeed())
		w.ActivityCountingEnabled = true
		transport := testhelpers.NewMockTransport()
		storage, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		w.StorageClient = storage
		ai, err := NewChunkAnalysisTask(run.SessionID, chunk.SourceURI, WorkoutTypeWOD, nil, nil, run.ProfileID, 0, 10, 0, "", 0)
		Expect(err).NotTo(HaveOccurred())
		_, err = queue.Enqueue(ai)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.coordinateRecordingAnalysis(ctx, run)).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("FAILED"))
		Expect(run.FailedStage).To(Equal("duration"))
		Expect(run.ActivityDispatched).To(BeFalse())
		Expect(transport.Requests()).To(BeEmpty())
		var main db.AnalysisResult
		Expect(database.Where("session_id = ?", run.SessionID).First(&main).Error).To(Succeed())
		Expect(main.Output).To(Equal(videoTooLongUserMessage))
		Expect(main.AvailableVideos).To(ContainElement("merged"))
		// Even the late-live payload without MediaRunID must respect the fence.
		Expect(w.enqueueActivityReview(ctx, VideoAnalysisPayload{ProfileID: run.ProfileID, SessionID: run.SessionID}, "gs://test-bucket/"+run.MergedObject)).To(Succeed())
		Expect(transport.Requests()).To(BeEmpty())
		row := liveTestRow(0, 0, 10)
		row.ProfileID = run.ProfileID
		row.SessionID = run.SessionID
		row.FilePath = chunk.SourceURI
		row.MediaStartSecs = nil
		row.MediaEndSecs = nil
		saved := testhelpers.CreateChunkAnalysisResult(database, &row)
		transport.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + run.MergedObject).Reply(200).JSON(map[string]any{"name": run.MergedObject, "bucket": "test-bucket", "generation": "19"})
		Expect(w.coordinateRecordingAnalysis(ctx, run)).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.ActivityDispatched).To(BeTrue())
		Expect(database.First(&session, session.ID).Error).To(Succeed())
		var summary activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &summary)).To(Succeed())
		Expect(summary.ReviewState).To(Equal("partial"))
		Expect(summary.MediaGeneration).To(Equal("19"))
		Expect(database.First(&saved, saved.ID).Error).To(Succeed())
		Expect(saved.MediaEndSecs).NotTo(BeNil())
		Expect(*saved.MediaEndSecs).To(Equal(5460.0))
		Expect(transport.Verify()).To(Succeed())
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		for _, task := range pending {
			Expect(task.Type).NotTo(Equal(TypeActivityReview))
			Expect(task.Type).NotTo(Equal(TypeVideoAnalysis))
		}
		var preparationCount int64
		Expect(database.Model(&db.AnalysisVideoPreparation{}).Where("run_id = ?", run.ID).Count(&preparationCount).Error).To(Succeed())
		Expect(preparationCount).To(BeZero())
	})

	It("reuses small merged media without downloading or encoding again", func() {
		run := newRun("PREPARING_ANALYSIS")
		run.MergedObject = "videos/1/recording/merged.mp4"
		run.MergedGeneration = 23
		run.MergedBytes = maxAnalysisVideoSizeBytes
		Expect(database.Model(&run).Updates(map[string]any{"merged_object": run.MergedObject, "merged_generation": run.MergedGeneration, "merged_bytes": run.MergedBytes}).Error).To(Succeed())
		prep := testhelpers.CreateAnalysisVideoPreparation(database, &db.AnalysisVideoPreparation{RunID: run.ID, SourceObject: run.MergedObject, SourceGeneration: run.MergedGeneration, PresetVersion: recordingAnalysisPreset})
		token, err := w.claimRecordingMedia(ctx, run, "analysis_video_preparations", prep.ID, 0, "PENDING")
		Expect(err).NotTo(HaveOccurred())
		Expect(w.prepareRecordingAnalysisVideo(ctx, run, prep, token)).To(Succeed())
		Expect(database.First(&prep, prep.ID).Error).To(Succeed())
		Expect(prep.State).To(Equal("READY"))
		Expect(prep.OutputObject).To(Equal(run.MergedObject))
		Expect(prep.OutputGeneration).To(Equal(run.MergedGeneration))
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("READY_FOR_ANALYSIS"))
		Expect(run.AnalysisDispatched).To(BeFalse())
	})

	It("starts analysis preparation while chunk AI is still pending", func() {
		run := newRun("MERGED")
		chunk := testhelpers.CreateRecordingChunk(database, &db.RecordingChunk{RunID: run.ID, AIDispatched: true})
		seal(&run, chunk)
		run.MergedObject = "videos/1/recording/merged.mp4"
		run.MergedGeneration = 31
		run.MergedBytes = 1024
		run.Intervals = recordingJSON([]concatMediaInterval{{FilePath: chunk.SourceURI, Start: 0, End: 10}})
		Expect(database.Model(&run).Updates(map[string]any{"merged_object": run.MergedObject, "merged_generation": run.MergedGeneration, "merged_bytes": run.MergedBytes, "intervals": run.Intervals}).Error).To(Succeed())
		ai, err := NewChunkAnalysisTask(run.SessionID, chunk.SourceURI, WorkoutTypeWOD, nil, nil, run.ProfileID, 0, 10, 0, "", 0)
		Expect(err).NotTo(HaveOccurred())
		_, err = queue.Enqueue(ai)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.coordinateRecordingAnalysis(ctx, run)).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("PREPARING_ANALYSIS"))
		Expect(run.ActivityDispatched).To(BeFalse())
		var prep db.AnalysisVideoPreparation
		Expect(database.Where("run_id = ?", run.ID).First(&prep).Error).To(Succeed())
		Expect(prep.State).To(Equal("PENDING"))
		Expect(prep.SourceGeneration).To(Equal(int64(31)))
	})

	It("does not promote legacy markers during recovery", func() {
		run := testhelpers.CreateRecordingMediaRun(database, &db.RecordingMediaRun{Version: 0, State: "LEGACY"})
		w.recoverRecordingMedia(ctx)
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("LEGACY"))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})
})
