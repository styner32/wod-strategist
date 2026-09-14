package worker_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type mockQueueClient struct {
	enqueuedTasks []*asynq.Task
}

func (m *mockQueueClient) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	m.enqueuedTasks = append(m.enqueuedTasks, task)
	return &asynq.TaskInfo{ID: "mock-task-id"}, nil
}
func (m *mockQueueClient) EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	m.enqueuedTasks = append(m.enqueuedTasks, task)
	return &asynq.TaskInfo{ID: "mock-task-id"}, nil
}
func (m *mockQueueClient) Close() error {
	return nil
}

var _ = Describe("SensorRecovery", func() {
	var (
		dbConn      *gorm.DB
		mockQueue   *mockQueueClient
		recoveryMgr *worker.SensorRecoveryManager
		profile     db.Profile
	)

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		birthYear := 1990
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{
			BirthYear: &birthYear,
		})

		mockQueue = &mockQueueClient{}
		recoveryMgr = worker.NewSensorRecoveryManager(dbConn, mockQueue, zap.NewNop())
	})

	It("marks overdue UPLOADING rows (>24h) as EXPIRED", func() {
		past := time.Now().UTC().Add(-25 * time.Hour)
		sessionID := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		procData := map[string]any{
			"schema_version":    1,
			"request_id":        "d0000000-0000-0000-0000-000000000001",
			"object_name":       "some/path.ndjson",
			"target_generation": "100",
		}
		procJSON, _ := json.Marshal(procData)

		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:           sessionID,
			ProfileID:           profile.ID,
			Status:              "PENDING",
			SensorState:         db.SensorStateUploading,
			SensorVersion:       1,
			SensorProcessing:    db.JSONDocument(procJSON),
			SensorNextAttemptAt: &past,
		})

		recoveryMgr.RunRecovery(context.Background())

		var updated db.AnalysisResult
		Expect(dbConn.First(&updated, analysis.ID).Error).NotTo(HaveOccurred())
		Expect(updated.SensorState).To(Equal(db.SensorStateExpired))
		Expect(updated.SensorNextAttemptAt).To(BeNil())
	})

	It("re-queues overdue PENDING tasks and updates sensor_next_attempt_at", func() {
		past := time.Now().UTC().Add(-10 * time.Minute)
		sessionID := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		reqUUID := "d0000000-0000-0000-0000-000000000002"
		procData := map[string]any{
			"schema_version":    1,
			"request_id":        reqUUID,
			"object_name":       "some/path.ndjson",
			"target_generation": "100",
		}
		procJSON, _ := json.Marshal(procData)

		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:           sessionID,
			ProfileID:           profile.ID,
			Status:              "COMPLETED",
			SensorState:         db.SensorStatePending,
			SensorVersion:       1,
			SensorProcessing:    db.JSONDocument(procJSON),
			SensorNextAttemptAt: &past,
		})

		recoveryMgr.RunRecovery(context.Background())

		// Verify task was enqueued
		Expect(mockQueue.enqueuedTasks).To(HaveLen(1))
		Expect(mockQueue.enqueuedTasks[0].Type()).To(Equal(worker.TypeSensorTelemetry))

		// Verify sensor_next_attempt_at extended
		var updated db.AnalysisResult
		Expect(dbConn.First(&updated, analysis.ID).Error).NotTo(HaveOccurred())
		Expect(updated.SensorNextAttemptAt).NotTo(BeNil())
		Expect(updated.SensorNextAttemptAt.After(time.Now().UTC())).To(BeTrue())
	})

	It("re-queues expired RUNNING tasks and invalidates stale lease token", func() {
		past := time.Now().UTC().Add(-5 * time.Minute)
		sessionID := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		reqUUID := "d0000000-0000-0000-0000-000000000003"
		procData := map[string]any{
			"schema_version":    1,
			"request_id":        reqUUID,
			"object_name":       "some/path.ndjson",
			"target_generation": "100",
			"lease_token":       "old-lease-token",
		}
		procJSON, _ := json.Marshal(procData)

		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:           sessionID,
			ProfileID:           profile.ID,
			Status:              "COMPLETED",
			SensorState:         db.SensorStateRunning,
			SensorVersion:       1,
			SensorProcessing:    db.JSONDocument(procJSON),
			SensorNextAttemptAt: &past,
		})

		recoveryMgr.RunRecovery(context.Background())

		// Verify task was enqueued
		Expect(mockQueue.enqueuedTasks).To(HaveLen(1))

		var updated db.AnalysisResult
		Expect(dbConn.First(&updated, analysis.ID).Error).NotTo(HaveOccurred())
		Expect(updated.SensorState).To(Equal(db.SensorStatePending))

		var procAfter map[string]any
		Expect(json.Unmarshal([]byte(updated.SensorProcessing), &procAfter)).To(Succeed())
		Expect(procAfter["lease_token"]).To(BeNil())
	})

	It("does not recover task if retry_not_before is in the future", func() {
		past := time.Now().UTC().Add(-5 * time.Minute)
		future := time.Now().UTC().Add(10 * time.Minute)
		sessionID := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		reqUUID := "d0000000-0000-0000-0000-000000000004"
		procData := map[string]any{
			"schema_version":    1,
			"request_id":        reqUUID,
			"object_name":       "some/path.ndjson",
			"target_generation": "100",
			"retry_not_before":  future.Format(time.RFC3339),
		}
		procJSON, _ := json.Marshal(procData)

		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:           sessionID,
			ProfileID:           profile.ID,
			Status:              "COMPLETED",
			SensorState:         db.SensorStatePending,
			SensorVersion:       1,
			SensorProcessing:    db.JSONDocument(procJSON),
			SensorNextAttemptAt: &past,
		})

		recoveryMgr.RunRecovery(context.Background())

		// Should not be enqueued
		Expect(mockQueue.enqueuedTasks).To(BeEmpty())

		var updated db.AnalysisResult
		Expect(dbConn.First(&updated, analysis.ID).Error).NotTo(HaveOccurred())
		Expect(updated.SensorState).To(Equal(db.SensorStatePending))
	})
})
