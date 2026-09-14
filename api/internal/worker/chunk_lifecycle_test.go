package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ = Describe("Chunk lifecycle recovery", Ordered, func() {
	var database *gorm.DB
	var w *Worker
	var profile db.Profile
	var queue *asynq.Client
	var inspector *asynq.Inspector
	var transport *testhelpers.MockTransport
	const sid = "sess-chunk-lifecycle"
	const uri = "gs://test-bucket/videos/sess-chunk-lifecycle/chunk_001.mp4"

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
		testhelpers.CleanupQueue(inspector)
		profile = testhelpers.CreateProfile(database, &db.Profile{})
		transport = testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		w = &Worker{DB: database, QueueClient: queue, QueueInspector: inspector,
			StorageClient: storageClient, BucketName: "test-bucket", logger: zap.NewNop()}
	})

	DescribeTable("serializes concurrent terminal writes and preserves success", func(seedFailure bool) {
		start, end := 0.0, 10.0
		var seed db.ChunkAnalysisResult
		if seedFailure {
			seed = testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
				SessionID: sid, ProfileID: profile.ID, FilePath: uri, Status: "FAILED",
				MediaStartSecs: &start, MediaEndSecs: &end,
			})
		}
		var wg sync.WaitGroup
		errs := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				status, output := "FAILED", "failed"
				if i%2 == 0 {
					status, output = "COMPLETED", "good form"
				}
				errs <- w.persistChunkAnalysisResult(context.Background(), &db.ChunkAnalysisResult{
					SessionID: sid, ProfileID: profile.ID, FilePath: uri, Status: status, Output: output,
					StartSecs: &start, EndSecs: &end,
				})
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}
		var rows []db.ChunkAnalysisResult
		Expect(database.Where("session_id = ?", sid).Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))

		Expect(rows[0].Status).To(Equal("COMPLETED"))
		Expect(rows[0].Output).To(Equal("good form"))
		if seedFailure {
			Expect(rows[0].ID).To(Equal(seed.ID))
			Expect(rows[0].MediaStartSecs).To(Equal(&start))
			Expect(rows[0].MediaEndSecs).To(Equal(&end))
		}
	}, Entry("when the row does not exist", false), Entry("when failure has verified offsets", true))

	DescribeTable("retries final status persistence after a real database write failure",
		func(kind string) {
			p := VideoAnalysisPayload{SessionID: sid, ProfileID: profile.ID, FilePath: uri, StartSecs: 0, EndSecs: 10}
			body, err := json.Marshal(p)
			Expect(err).NotTo(HaveOccurred())
			task := asynq.NewTask(kind, body)
			handler := w.HandleChunkAnalysisTask
			if kind == TypeChunkAnalysisWithSession {
				handler = w.HandleChunkAnalysisWithSessionTask
			} else if kind == TypeVideoAnalysis {
				handler = w.HandleVideoAnalysisTask
			}
			tx := database.Begin()
			Expect(tx.Error).NotTo(HaveOccurred())
			DeferCleanup(func() { tx.Rollback() })
			Expect(tx.Exec("SET TRANSACTION READ ONLY").Error).To(Succeed())
			w.DB = tx
			ctx := WithRetryCount(context.Background(), 3)
			err = handler(ctx, task)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, asynq.SkipRetry)).To(BeFalse())
			Expect(tx.Rollback().Error).To(Succeed())
			w.DB = database
			Expect(handler(ctx, task)).To(Equal(asynq.SkipRetry))
		},
		Entry("chunk", TypeChunkAnalysis),
		Entry("chunk with session", TypeChunkAnalysisWithSession),
		Entry("full video", TypeVideoAnalysis),
	)

	It("waits for a scheduled chunk even after the orphan grace and many merge retries", func() {
		chunk, err := NewChunkAnalysisTask(sid, uri, WorkoutTypeWOD, nil, nil, profile.ID, 0, 10, 0, "", 0)
		Expect(err).NotTo(HaveOccurred())
		_, err = queue.Enqueue(chunk, asynq.ProcessIn(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		testhelpers.MockGCSListObjects(transport, "test-bucket", "videos/"+sid+"/", []string{"videos/" + sid + "/chunk_001.mp4"})
		body, err := json.Marshal(VideoAnalysisPayload{SessionID: sid, ProfileID: profile.ID,
			FilePath: "gs://test-bucket/videos/" + sid, MergeRequestedAt: time.Now().Add(-2 * orphanChunkGrace)})
		Expect(err).NotTo(HaveOccurred())
		err = w.HandleMergeChunksTask(WithRetryCount(context.Background(), 20), asynq.NewTask(TypeMergeChunks, body))
		Expect(err).To(MatchError(ContainSubstring("scheduled")))
		var count int64
		Expect(database.Model(&db.ChunkAnalysisResult{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("recovers capture times from archived tasks before merging native filenames", func() {
		malformed, err := queue.Enqueue(asynq.NewTask(TypeChunkAnalysis, []byte("{")), asynq.ProcessIn(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(inspector.ArchiveTask("default", malformed.ID)).To(Succeed())
		chunkURI := "gs://test-bucket/videos/" + sid + "/native-camera-uuid.mov"
		chunk, err := NewChunkAnalysisWithSessionTask(sid, chunkURI, profile.ID, 10, 20, 120, 0.8)
		Expect(err).NotTo(HaveOccurred())
		info, err := queue.Enqueue(chunk, asynq.ProcessIn(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(inspector.ArchiveTask("default", info.ID)).To(Succeed())
		// Stop at download to inspect the real handler's state recovery without
		// making this case depend on another successful FFmpeg round-trip.
		testhelpers.MockGCSListObjects(transport, "test-bucket", "videos/"+sid+"/", []string{"videos/" + sid + "/native-camera-uuid.mov"})
		task, err := NewMergeChunksTask(sid, "gs://test-bucket/videos/"+sid, WorkoutTypeWOD, nil, nil, profile.ID, false, "")
		Expect(err).NotTo(HaveOccurred())
		err = w.HandleMergeChunksTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("failed to download chunk")))
		var result db.ChunkAnalysisResult
		Expect(database.Where("file_path = ?", chunkURI).First(&result).Error).To(Succeed())
		Expect(result.Status).To(Equal("FAILED"))
		Expect(*result.StartSecs).To(Equal(10.0))
		Expect(*result.EndSecs).To(Equal(20.0))
		Expect(result.HeartRateBPM).To(Equal(120))
	})
})

var _ = Describe("orderedMergeChunks", func() {
	It("deduplicates old rows and orders numeric chunks with a missing middle timestamp", func() {
		start, end := 0.0, 20.0
		paths := []string{"chunk_10.mp4", "chunk_2.mp4", "chunk_1.mp4", "chunk_2.mp4"}
		ordered, err := orderedMergeChunks(paths, []db.ChunkAnalysisResult{
			{FilePath: "chunk_1.mp4", StartSecs: &start},
			{FilePath: "chunk_10.mp4", StartSecs: &end},
			{FilePath: "chunk_2.mp4", Status: "FAILED"},
			{FilePath: "chunk_2.mp4", Status: "FAILED"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(ordered).To(Equal([]string{"chunk_1.mp4", "chunk_2.mp4", "chunk_10.mp4"}))
	})
	It("uses capture times for native UUID filenames", func() {
		a, b := 20.0, 10.0
		ordered, err := orderedMergeChunks([]string{"a.mov", "z.mov"}, []db.ChunkAnalysisResult{
			{FilePath: "a.mov", StartSecs: &a}, {FilePath: "z.mov", StartSecs: &b},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(ordered).To(Equal([]string{"z.mov", "a.mov"}))
	})
	It("rejects guessing the order of untimed native filenames", func() {
		_, err := orderedMergeChunks([]string{"a.mov", "z.mov"}, nil)
		Expect(err).To(MatchError(ContainSubstring("cannot verify original chunk order")))
	})
})
