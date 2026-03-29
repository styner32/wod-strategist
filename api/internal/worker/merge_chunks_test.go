package worker

import (
	"context"
	"encoding/json"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("NewMergeChunksTask", func() {

	It("creates a task with the correct payload fields", func() {
		task, err := NewMergeChunksTask(
			"session-1",
			"gs://bucket/videos/session-1",
			"wod",
			[]string{"Deadlift", "Pull-up"},
			[]string{"Knee"},
			42,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(task.Type()).To(Equal(TypeMergeChunks))

		var payload VideoAnalysisPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.FilePath).To(Equal("gs://bucket/videos/session-1"))
		Expect(payload.WorkoutType).To(Equal(WorkoutTypeWOD))
		Expect(payload.Movements).To(Equal([]string{"Deadlift", "Pull-up"}))
		Expect(payload.Injuries).To(Equal([]string{"Knee"}))
		Expect(payload.ProfileID).To(Equal(uint(42)))
	})
})

var _ = Describe("HandleMergeChunksTask", func() {
	var (
		dbConn      *gorm.DB
		queueClient *asynq.Client
		inspector   *asynq.Inspector
		w           *Worker
	)

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		queueClient = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		testhelpers.CleanupQueue(inspector)

		w = &Worker{
			DB:          dbConn,
			QueueClient: queueClient,
			BucketName:  "test-bucket",
			logger:      zap.NewNop(),
		}
	})

	It("returns SkipRetry immediately when the GCS URI is invalid", func() {
		w.StorageClient = &fakeStorage{}

		task, err := NewMergeChunksTask(
			"sess-merge-baduri",
			"/not/a/gcs/uri",
			WorkoutTypeWOD, nil, nil, 0,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleMergeChunksTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("invalid GCS URI")))

		pending, _ := inspector.ListPendingTasks("default")
		Expect(pending).To(BeEmpty())
	})

	It("returns SkipRetry when no chunk objects are found", func() {
		w.StorageClient = &fakeStorage{} // ListObjects returns nil

		task, err := NewMergeChunksTask(
			"sess-merge-nochunks",
			"gs://test-bucket/videos/sess-merge-nochunks",
			WorkoutTypeWOD, nil, nil, 0,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleMergeChunksTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("no chunks found")))

		pending, _ := inspector.ListPendingTasks("default")
		Expect(pending).To(BeEmpty())
	})

	It("skips merged/hardsubbed objects when filtering", func() {
		w.StorageClient = &listableStorage{
			objects: []string{
				"videos/sess-filter-001/file_merged_abc.mp4",
				"videos/sess-filter-001/file_hardsubbed_abc.mp4",
			},
		}

		task, err := NewMergeChunksTask(
			"sess-filter-001",
			"gs://test-bucket/videos/sess-filter-001",
			WorkoutTypeWOD, nil, nil, 0,
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleMergeChunksTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("no chunks found")))
	})

	Context("when ffmpeg is available", func() {
		BeforeEach(func() {
			if !hasFfmpeg() {
				Skip("ffmpeg not found in PATH")
			}
		})

		It("enqueues a video:analysis task after merging chunks", func() {
			tmpFile := createTinyMP4(GinkgoT())

			w.StorageClient = &listableStorage{
				objects:      []string{"videos/sess-merge-001/chunk_001.mp4"},
				downloadPath: tmpFile,
			}
			w.GeminiClient = &fakeGemini{} // not called by merge, required as interface

			task, err := NewMergeChunksTask(
				"sess-merge-001",
				"gs://test-bucket/videos/sess-merge-001",
				WorkoutTypeWOD,
				[]string{"Deadlift"},
				nil, 0,
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(w.HandleMergeChunksTask(context.Background(), task)).To(Succeed())

			// Verify exactly one video:analysis task was enqueued to Redis
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))
			Expect(pending[0].Type).To(Equal(TypeVideoAnalysis))

			var enqueued VideoAnalysisPayload
			Expect(json.Unmarshal(pending[0].Payload, &enqueued)).To(Succeed())
			Expect(enqueued.SessionID).To(Equal("sess-merge-001"))
			Expect(enqueued.WorkoutType).To(Equal(WorkoutTypeWOD))
			Expect(enqueued.Movements).To(Equal([]string{"Deadlift"}))
			Expect(enqueued.FilePath).To(ContainSubstring("sess-merge-001"))
		})
	})
})
