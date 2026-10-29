package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/redis/go-redis/v9"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
	"gorm.io/gorm"
)

const archiveIngressSessionID = "WOD-20261029-01JARCHIVEINGRESS00000000"
const archiveIngressRequestID = "a0000000-0000-0000-0000-000000000029"
const archiveIngressSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type archiveIngressFixture struct {
	router    *gin.Engine
	profile   db.Profile
	owner     db.User
	analysis  db.AnalysisResult
	transport *testhelpers.MockTransport
}

func newArchiveIngressFixture() archiveIngressFixture {
	ensureTestDB()
	testhelpers.CleanupDB(dbConn)
	testhelpers.CleanupQueue(inspector)
	profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
	var owner db.User
	Expect(dbConn.First(&owner, profile.UserID).Error).To(Succeed())
	testhelpers.CreateSession(dbConn, &db.Session{SessionID: archiveIngressSessionID, ProfileID: profile.ID, WorkoutType: "wod", WODDescription: "preserved original plan"})
	generation := "1"
	processing, _ := json.Marshal(controllers.SensorProcessingData{
		SchemaVersion: 1, RequestID: archiveIngressRequestID, BaseVersion: "0",
		ObjectName:       fmt.Sprintf("videos/%d/%s/sensor_telemetry_v1_%s.ndjson", profile.ID, archiveIngressSessionID, archiveIngressRequestID),
		TargetGeneration: &generation, ExpectedSizeBytes: 1024, ExpectedSHA256: archiveIngressSHA,
		CalculationInputs: controllers.CalculationInputsSnapshot{CalculationVersion: 3},
		RequestedAt:       time.Now().UTC(), UploadExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	})
	_, progress := progressFixture("archive-ingress-progress")
	analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: archiveIngressSessionID, ProfileID: profile.ID, Status: "COMPLETED", Output: "preserved original analysis", WorkoutProgress: progress, SensorVersion: 1, SensorState: db.SensorStateUploading, SensorProcessing: db.JSONDocument(processing)})
	queue := testhelpers.NewQueueClient()
	DeferCleanup(queue.Close)
	transport := testhelpers.NewMockTransport()
	storage, err := testhelpers.NewStorageClient("test-bucket", transport)
	Expect(err).NotTo(HaveOccurred())
	router := newTestRouterWithAuthService(controllers.Config{QueueClient: queue, StorageClient: storage, BucketName: "test-bucket", EnableChunkReanalysis: true, EnableSessionReanalysis: true, EnableAgenticHighlights: true})
	return archiveIngressFixture{router: router, profile: profile, owner: owner, analysis: analysis, transport: transport}
}

func (f archiveIngressFixture) post(path string, body any) *httptest.ResponseRecorder {
	encoded, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred())
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, path, string(encoded), &f.owner))
	return response
}
func (f archiveIngressFixture) archive() {
	response := f.post(fmt.Sprintf("/api/v1/history/%d/archive", f.analysis.ID), map[string]any{})
	Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
}
func (f archiveIngressFixture) restore() {
	response := f.post(fmt.Sprintf("/api/v1/history/%d/unarchive", f.analysis.ID), map[string]any{})
	Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
}
func (f archiveIngressFixture) originalBody() map[string]any {
	return map[string]any{"session_id": archiveIngressSessionID, "profile_id": f.profile.ID,
		"gcs_uri":      fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_001.mp4", f.profile.ID, archiveIngressSessionID),
		"workout_type": "wod", "movements": []string{"Air Squat"}, "injuries": []string{},
		"start_secs": 0, "end_secs": 10, "live_analysis_version": 1, "max_duration": 60,
		"wod_description": "must not replace original plan", "appearance_hints": "must not replace session appearance"}
}
func (f archiveIngressFixture) expectNoWork() {
	Expect(f.transport.Requests()).To(BeEmpty())
	queues, err := inspector.Queues()
	Expect(err).NotTo(HaveOccurred())
	for _, name := range queues {
		info, err := inspector.GetQueueInfo(name)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Pending+info.Active+info.Scheduled+info.Retry).To(BeZero(), name)
	}
	var current db.AnalysisResult
	Expect(dbConn.First(&current, f.analysis.ID).Error).To(Succeed())
	Expect(current.Output).To(Equal("preserved original analysis"))
	var session db.Session
	Expect(dbConn.Where("session_id = ?", archiveIngressSessionID).First(&session).Error).To(Succeed())
	Expect(session.WODDescription).To(Equal("preserved original plan"))
	Expect(string(session.MovementHints)).To(MatchJSON(`[]`))
	var appearances int64
	Expect(dbConn.Model(&db.SessionAppearanceHint{}).Where("session_id = ?", archiveIngressSessionID).Count(&appearances).Error).To(Succeed())
	Expect(appearances).To(BeZero())
}

func expectArchiveWinningMetadataRace(route string) {
	f := newArchiveIngressFixture()
	entered, release := make(chan struct{}), make(chan struct{})
	var paused atomic.Bool
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	// Pause after the non-locking ingress check has observed the active row.
	// Archive can then win before this request acquires the publication fence.
	const callbackName = "test:archive_ingress_state_read"
	Expect(dbConn.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*db.AnalysisResult); ok && strings.Contains(tx.Statement.SQL.String(), "automatic_processing_cancelled_at") && paused.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	})).To(Succeed())
	defer dbConn.Callback().Query().Remove(callbackName)
	body, err := json.Marshal(f.originalBody())
	Expect(err).NotTo(HaveOccurred())
	response := httptest.NewRecorder()
	request := newAuthorizedJSONRequest(http.MethodPost, route, string(body), &f.owner)
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		f.router.ServeHTTP(response, request)
	}()
	DeferCleanup(func() { unblock(); Eventually(done, 5*time.Second).Should(BeClosed()) })
	Eventually(entered, 5*time.Second).Should(BeClosed())
	f.archive()
	unblock()
	Eventually(done, 5*time.Second).Should(BeClosed())
	Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
	Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_PROCESSING_CANCELLED"))
	f.expectNoWork()
}

func expectIngressMetadataRollbackOnQueueFailure(route string) {
	f := newArchiveIngressFixture()
	queue := newBrokenQueueClient()
	DeferCleanup(queue.Close)
	f.router = newTestRouter(controllers.Config{QueueClient: queue})
	response := f.post(route, f.originalBody())
	Expect(response.Code).To(Equal(http.StatusInternalServerError), response.Body.String())
	f.expectNoWork()
}

func expectAcceptedIngressMetadataAndTask(route, taskType string) {
	f := newArchiveIngressFixture()
	body := f.originalBody()
	// Redis retains uniqueness keys longer than SQL fixtures; keep independent
	// acceptance examples distinct even when this suite is rerun immediately.
	plan := "accepted plan " + uuid.NewString()
	body["wod_description"] = plan
	response := f.post(route, body)
	expectIngressMetadataAndTask(f, response, taskType, plan)
}

// Observe the real Redis transport so cancellation occurs after acceptance and
// before the controller's SQL transaction can commit.
type archiveIngressCancelHook struct {
	cancel context.CancelFunc
	fired  atomic.Bool
}

func (h *archiveIngressCancelHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *archiveIngressCancelHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *archiveIngressCancelHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, command redis.Cmder) error {
		err := next(ctx, command)
		if err == nil && (command.Name() == "eval" || command.Name() == "evalsha") && h.fired.CompareAndSwap(false, true) {
			h.cancel()
		}
		return err
	}
}

func expectIngressMetadataCommitAfterDisconnect(route, taskType string) {
	f := newArchiveIngressFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hook := &archiveIngressCancelHook{cancel: cancel}
	address := os.Getenv("TEST_REDIS_URL")
	if address == "" {
		address = os.Getenv("REDIS_URL")
	}
	if address == "" {
		port := os.Getenv("REDIS_PORT")
		if port == "" {
			port = "6379"
		}
		address = "localhost:" + port
	}
	redisClient := redis.NewClient(&redis.Options{Addr: address, DB: 15})
	DeferCleanup(redisClient.Close)
	redisClient.AddHook(hook)
	f.router = newTestRouter(controllers.Config{QueueClient: asynq.NewClientFromRedisClient(redisClient)})
	payload := f.originalBody()
	plan := "accepted plan " + uuid.NewString()
	payload["wod_description"] = plan
	body, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	request := newAuthorizedJSONRequest(http.MethodPost, route, string(body), &f.owner).WithContext(ctx)
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	Expect(hook.fired.Load()).To(BeTrue())
	Expect(ctx.Err()).To(MatchError(context.Canceled))
	expectIngressMetadataAndTask(f, response, taskType, plan)
}

func expectIngressMetadataAndTask(f archiveIngressFixture, response *httptest.ResponseRecorder, taskType, plan string) {
	Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
	var session db.Session
	Expect(dbConn.Where("session_id = ?", archiveIngressSessionID).First(&session).Error).To(Succeed())
	Expect(session.WODDescription).To(Equal(plan))
	Expect(string(session.MovementHints)).To(MatchJSON(`["Air Squat"]`))
	var appearance db.SessionAppearanceHint
	Expect(dbConn.Where("session_id = ?", archiveIngressSessionID).First(&appearance).Error).To(Succeed())
	Expect(string(appearance.Hints)).To(MatchJSON(`{"appearance":"must not replace session appearance"}`))
	tasks, err := inspector.ListPendingTasks("default")
	Expect(err).NotTo(HaveOccurred())
	Expect(tasks).To(HaveLen(1))
	Expect(tasks[0].Type).To(Equal(taskType))
	if taskType == worker.TypeChunkAnalysisWithSession {
		var payload worker.VideoAnalysisWithSessionPayload
		Expect(json.Unmarshal(tasks[0].Payload, &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal(archiveIngressSessionID))
		Expect(payload.ProfileID).To(Equal(f.profile.ID))
		Expect(payload.LiveAnalysisVersion).To(Equal(1))
	} else {
		var payload worker.VideoAnalysisPayload
		Expect(json.Unmarshal(tasks[0].Payload, &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal(archiveIngressSessionID))
		Expect(payload.ProfileID).To(Equal(f.profile.ID))
		Expect(payload.Movements).To(Equal([]string{"Air Squat"}))
		Expect(payload.WODDescription).To(Equal(session.WODDescription))
	}
	Expect(f.transport.Requests()).To(BeEmpty())
}
func expectOriginalArchiveIngressBlocked(route string) {
	f := newArchiveIngressFixture()
	body := f.originalBody()
	encoded, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred())
	anonymous := httptest.NewRequest(http.MethodPost, route, nil)
	anonymous.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, anonymous)
	Expect(response.Code).To(Equal(http.StatusUnauthorized))
	response = httptest.NewRecorder()
	f.router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, route, `{"session_id":`, &f.owner))
	Expect(response.Code).To(Equal(http.StatusBadRequest), response.Body.String())
	other := testhelpers.CreateProfile(dbConn, &db.Profile{})
	var otherOwner db.User
	Expect(dbConn.First(&otherOwner, other.UserID).Error).To(Succeed())
	response = httptest.NewRecorder()
	f.router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, route, string(encoded), &otherOwner))
	Expect(response.Code).To(Equal(http.StatusForbidden), response.Body.String())
	f.archive()
	response = f.post(route, body)
	Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
	Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_ARCHIVED"))
	f.expectNoWork()
	f.restore()
	response = f.post(route, body)
	Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
	Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_PROCESSING_CANCELLED"))
	f.expectNoWork()
}

var _ = Describe("POST /api/v1/upload-complete", func() {
	It("fences archived and restored original processing without outbound requests or queue tasks", func() { expectOriginalArchiveIngressBlocked("/api/v1/upload-complete") })
	It("preserves source metadata when archive wins after the first state check", func() { expectArchiveWinningMetadataRace("/api/v1/upload-complete") })
	It("rolls back source metadata when the queue rejects processing", func() { expectIngressMetadataRollbackOnQueueFailure("/api/v1/upload-complete") })
	It("commits source metadata with accepted processing", func() { expectAcceptedIngressMetadataAndTask("/api/v1/upload-complete", worker.TypeVideoAnalysis) })
	It("commits accepted source metadata when the caller disconnects before SQL commit", func() {
		expectIngressMetadataCommitAfterDisconnect("/api/v1/upload-complete", worker.TypeVideoAnalysis)
	})
})

var _ = Describe("POST /api/v1/chunk-complete", func() {
	It("fences archived and restored original processing without outbound requests or queue tasks", func() { expectOriginalArchiveIngressBlocked("/api/v1/chunk-complete") })
	It("preserves source metadata when archive wins after the first state check", func() { expectArchiveWinningMetadataRace("/api/v1/chunk-complete") })
	It("rolls back source metadata when the queue rejects processing", func() { expectIngressMetadataRollbackOnQueueFailure("/api/v1/chunk-complete") })
	It("commits source metadata with accepted processing", func() {
		expectAcceptedIngressMetadataAndTask("/api/v1/chunk-complete", worker.TypeChunkAnalysisWithSession)
	})
	It("commits accepted source metadata when the caller disconnects before SQL commit", func() {
		expectIngressMetadataCommitAfterDisconnect("/api/v1/chunk-complete", worker.TypeChunkAnalysisWithSession)
	})
})

var _ = Describe("POST /api/v1/merge-chunks", func() {
	It("fences archived and restored original processing without outbound requests or queue tasks", func() { expectOriginalArchiveIngressBlocked("/api/v1/merge-chunks") })
	It("preserves source metadata when archive wins after the first state check", func() { expectArchiveWinningMetadataRace("/api/v1/merge-chunks") })
	It("rolls back source metadata when the queue rejects processing", func() { expectIngressMetadataRollbackOnQueueFailure("/api/v1/merge-chunks") })
	It("commits source metadata with accepted processing", func() { expectAcceptedIngressMetadataAndTask("/api/v1/merge-chunks", worker.TypeMergeChunks) })
	It("commits accepted source metadata when the caller disconnects before SQL commit", func() {
		expectIngressMetadataCommitAfterDisconnect("/api/v1/merge-chunks", worker.TypeMergeChunks)
	})
})

var _ = Describe("POST /api/v1/retry-analysis", func() {
	It("fences archived and restored original processing without outbound requests or queue tasks", func() { expectOriginalArchiveIngressBlocked("/api/v1/retry-analysis") })
})

var _ = Describe("POST /api/v1/generate-highlight", func() {
	It("fences archived and restored original processing without outbound requests or queue tasks", func() { expectOriginalArchiveIngressBlocked("/api/v1/generate-highlight") })
})

var _ = Describe("POST /api/v1/verify-highlights", func() {
	It("fences archived and restored original processing without outbound requests or queue tasks", func() { expectOriginalArchiveIngressBlocked("/api/v1/verify-highlights") })
})

var _ = Describe("POST /api/v1/generate-hardsub", func() {
	It("fences archived and restored original processing without outbound requests or queue tasks", func() { expectOriginalArchiveIngressBlocked("/api/v1/generate-hardsub") })
})

func expectSensorArchiveIngressBlocked(suffix string) {
	f := newArchiveIngressFixture()
	path := "/api/v1/sessions/" + archiveIngressSessionID + suffix
	f.archive()
	for _, restored := range []bool{false, true} {
		if restored {
			f.restore()
		}
		var current db.AnalysisResult
		Expect(dbConn.First(&current, f.analysis.ID).Error).To(Succeed())
		version := strconv.FormatInt(current.SensorVersion, 10)
		body := map[string]any{"profile_id": f.profile.ID, "request_id": archiveIngressRequestID, "version": version,
			"expected_version": version, "size_bytes": 1024, "sha256": archiveIngressSHA, "calculation_version": 3}
		expectedCode := "SESSION_ARCHIVED"
		if suffix == "/sensor-upload" {
			// A fresh upload request cannot be rejected merely by an old
			// idempotency payload conflict; only the archive fence may stop it.
			body["request_id"] = "a0000000-0000-0000-0000-000000000030"
		} else if suffix == "/sensor-complete" {
			expectedCode = "SENSOR_REQUEST_SUPERSEDED"
		} else if restored {
			expectedCode = "SESSION_PROCESSING_CANCELLED"
		}
		response := f.post(path, body)
		Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		Expect(decodeMapBody(response)["error_code"]).To(Equal(expectedCode))
		var after db.AnalysisResult
		Expect(dbConn.First(&after, f.analysis.ID).Error).To(Succeed())
		Expect(after.SensorVersion).To(Equal(current.SensorVersion))
		Expect(after.SensorState).To(Equal(db.SensorStateCancelled))
		Expect(after.SensorNextAttemptAt).To(BeNil())
		Expect(after.AutomaticProcessingCancelledAt).NotTo(BeNil())
		f.expectNoWork()
	}
}

var _ = Describe("POST /api/v1/sessions/:session_id/sensor-upload", func() {
	It("does not resume a stopped original sensor request after restore even with the current version", func() { expectSensorArchiveIngressBlocked("/sensor-upload") })
})

var _ = Describe("POST /api/v1/sessions/:session_id/sensor-complete", func() {
	It("does not resume a stopped original sensor request after restore even with the current version", func() { expectSensorArchiveIngressBlocked("/sensor-complete") })
})

var _ = Describe("POST /api/v1/sessions/:session_id/sensor-reprocess", func() {
	It("does not resume a stopped original sensor request after restore even with the current version", func() { expectSensorArchiveIngressBlocked("/sensor-reprocess") })
})

var _ = Describe("POST /api/v1/sessions/:session_id/reanalyses", func() {
	It("rejects new explicit work while archived before reading source media", func() {
		f := newArchiveIngressFixture()

		f.archive()
		response := f.post("/api/v1/sessions/"+archiveIngressSessionID+"/reanalyses", map[string]any{"client_request_id": "archived-manual"})
		Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_ARCHIVED"))
		f.expectNoWork()
	})
})

var _ = Describe("POST /api/v1/sessions/:session_id/chunks/:chunk_id/reanalyses", func() {
	It("rejects new explicit work while archived before reading source media", func() {
		f := newArchiveIngressFixture()
		start, end := 0.0, 10.0
		chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{SessionID: archiveIngressSessionID, ProfileID: f.profile.ID, FilePath: fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_001.mp4", f.profile.ID, archiveIngressSessionID), Status: "COMPLETED", MediaStartSecs: &start, MediaEndSecs: &end})

		f.archive()
		response := f.post(fmt.Sprintf("/api/v1/sessions/%s/chunks/%d/reanalyses", archiveIngressSessionID, chunk.ID), map[string]any{"client_request_id": "archived-chunk-manual"})
		Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_ARCHIVED"))
		f.expectNoWork()
	})
})

var _ = Describe("POST /api/v1/sessions/:session_id/chunks/batch-reanalyses", func() {
	It("rejects new explicit work while archived before reading source media", func() {
		f := newArchiveIngressFixture()
		start, end := 0.0, 10.0
		chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{SessionID: archiveIngressSessionID, ProfileID: f.profile.ID, FilePath: fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_001.mp4", f.profile.ID, archiveIngressSessionID), Status: "COMPLETED", MediaStartSecs: &start, MediaEndSecs: &end})

		f.archive()
		response := f.post("/api/v1/sessions/"+archiveIngressSessionID+"/chunks/batch-reanalyses", map[string]any{"client_request_id": "archived-batch-manual", "chunk_ids": []uint{chunk.ID}})
		Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_ARCHIVED"))
		f.expectNoWork()
	})
})

var _ = Describe("POST /api/v1/sessions/:session_id/recognition-reviews", func() {
	It("rejects new explicit work while archived before reading source media", func() {
		f := newArchiveIngressFixture()

		f.archive()
		response := f.post("/api/v1/sessions/"+archiveIngressSessionID+"/recognition-reviews", map[string]any{"client_request_id": "archived-recognition-manual", "source_version": "archive-ingress-progress", "evaluation_fingerprint": "archived-fingerprint", "record_ids": []uint{1}, "start_secs": 0, "end_secs": 10})
		Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_ARCHIVED"))
		f.expectNoWork()
	})
})

var _ = Describe("POST /api/v1/sessions/:session_id/analysis-summary", func() {
	It("rejects new explicit work while archived before reading source media", func() {
		f := newArchiveIngressFixture()

		f.archive()
		response := f.post("/api/v1/sessions/"+archiveIngressSessionID+"/analysis-summary", map[string]any{})
		Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_ARCHIVED"))
		f.expectNoWork()
	})
})

var _ = Describe("POST /api/v1/sessions/:session_id/agentic-highlights", func() {
	It("rejects new explicit work while archived before reading source media", func() {
		f := newArchiveIngressFixture()

		f.archive()
		response := f.post("/api/v1/sessions/"+archiveIngressSessionID+"/agentic-highlights", map[string]any{})
		Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_ARCHIVED"))
		f.expectNoWork()
	})
})

var _ = Describe("POST /api/v1/sessions/:session_id/reanalyses/:run_id/apply", func() {
	It("rejects applying a completed manual candidate while its original record is archived", func() {
		f := newArchiveIngressFixture()
		run := testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{SessionID: archiveIngressSessionID, ProfileID: f.profile.ID, Status: db.SessionReanalysisStatusCompleted, Output: "must not replace original analysis"})
		f.archive()
		response := f.post(fmt.Sprintf("/api/v1/sessions/%s/reanalyses/%d/apply", archiveIngressSessionID, run.ID), map[string]any{})
		Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_ARCHIVED"))
		f.expectNoWork()
	})
})
