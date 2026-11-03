package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
)

type recordingMediaFixture struct {
	router    *gin.Engine
	profile   db.Profile
	owner     db.User
	sessionID string
	transport *testhelpers.MockTransport
}

func newRecordingMediaFixture() recordingMediaFixture {
	ensureTestDB()
	testhelpers.CleanupDB(dbConn)
	testhelpers.FlushRedis()
	profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
	var owner db.User
	Expect(dbConn.First(&owner, profile.UserID).Error).To(Succeed())
	transport := testhelpers.NewMockTransport()
	storage, err := testhelpers.NewStorageClient("test-bucket", transport)
	Expect(err).NotTo(HaveOccurred())
	queue := testhelpers.NewQueueClient()
	DeferCleanup(queue.Close)
	router := newTestRouterWithAuthService(controllers.Config{StorageClient: storage, BucketName: "test-bucket", QueueClient: queue})
	return recordingMediaFixture{router: router, profile: profile, owner: owner, sessionID: "WOD-20261103-" + strings.ReplaceAll(uuid.NewString(), "-", ""), transport: transport}
}

func (f recordingMediaFixture) post(path string, body any) *httptest.ResponseRecorder {
	raw, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred())
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, path, string(raw), &f.owner))
	return response
}

func (f recordingMediaFixture) chunkBody(order int) map[string]any {
	return map[string]any{"session_id": f.sessionID, "profile_id": f.profile.ID, "media_pipeline_version": 1, "live_analysis_version": 1,
		"client_chunk_id": fmt.Sprintf("capture-%d", order), "capture_order": order,
		"gcs_uri":      fmt.Sprintf("gs://test-bucket/videos/%d/%s/upload-%d.mp4", f.profile.ID, f.sessionID, order),
		"workout_type": "wod", "movements": []string{"Air Squat"}, "start_secs": (order - 1) * 10, "end_secs": order * 10}
}

func (f recordingMediaFixture) mockUpload(order int, generation string, initial bool) {
	object := fmt.Sprintf("videos/%d/%s/upload-%d.mp4", f.profile.ID, f.sessionID, order)
	if initial {
		testhelpers.MockGCSListObjects(f.transport, "test-bucket", fmt.Sprintf("videos/%d/%s/", f.profile.ID, f.sessionID), []string{object})
	}
	f.transport.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + url.PathEscape(object)).Reply(200).JSON(map[string]any{"name": object, "generation": generation, "size": "2048"})
}

func (f recordingMediaFixture) admit(order int, generation string, initial bool) controllers.RecordingMediaChunkReceipt {
	f.mockUpload(order, generation, initial)
	response := f.post("/api/v1/chunk-complete", f.chunkBody(order))
	Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
	var receipt controllers.RecordingMediaChunkReceipt
	Expect(json.Unmarshal(response.Body.Bytes(), &receipt)).To(Succeed())
	return receipt
}

func (f recordingMediaFixture) mergeBody(receipts ...controllers.RecordingMediaChunkReceipt) map[string]any {
	chunks := make([]controllers.RecordingMediaReceiptInput, 0, len(receipts))
	for _, receipt := range receipts {
		chunks = append(chunks, controllers.RecordingMediaReceiptInput{ChunkID: receipt.ChunkID, SourceGeneration: receipt.SourceGeneration})
	}
	return map[string]any{"session_id": f.sessionID, "profile_id": f.profile.ID, "media_pipeline_version": 1, "expected_chunk_count": len(chunks), "chunks": chunks, "workout_type": "wod"}
}

var _ = Describe("POST /api/v1/chunk-complete", func() {
	Context("recording media v1 admission", func() {
		It("returns a precise immutable receipt and creates one durable preparation and AI dispatch", func() {
			f := newRecordingMediaFixture()
			receipt := f.admit(1, "9007199254740993", true)
			Expect(receipt.ChunkID).NotTo(BeEmpty())
			Expect(receipt.SourceGeneration).To(Equal("9007199254740993"))
			var chunk db.RecordingChunk
			Expect(dbConn.First(&chunk, receipt.ChunkID).Error).To(Succeed())
			Expect(chunk.SourceGeneration).To(Equal(int64(9007199254740993)))
			Expect(chunk.AIDispatched).To(BeTrue())
			var payload worker.VideoAnalysisPayload
			Expect(json.Unmarshal(chunk.AIPayload, &payload)).To(Succeed())
			Expect(payload.RecordingChunkID).To(Equal(chunk.ID))
			Expect(payload.SourceGeneration).To(Equal(chunk.SourceGeneration))
			Expect(payload.FilePath).To(Equal(receipt.GCSURI))
			Expect(payload.MediaRunID).To(Equal(chunk.RunID))
			body := f.chunkBody(1)
			body["gcs_uri"] = fmt.Sprintf("gs://test-bucket/videos/%d/%s/extra-upload.mov", f.profile.ID, f.sessionID)
			retry := f.post("/api/v1/chunk-complete", body)
			Expect(retry.Code).To(Equal(http.StatusAccepted), retry.Body.String())
			var again controllers.RecordingMediaChunkReceipt
			Expect(json.Unmarshal(retry.Body.Bytes(), &again)).To(Succeed())
			Expect(again).To(Equal(receipt))
			var count int64
			Expect(dbConn.Model(&db.RecordingChunk{}).Count(&count).Error).To(Succeed())
			Expect(count).To(Equal(int64(1)))
			Expect(dbConn.Model(&db.ChunkMediaPreparation{}).Count(&count).Error).To(Succeed())
			Expect(count).To(Equal(int64(1)))
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("rejects changed capture identity and a second logical ID for an occupied order", func() {
			f := newRecordingMediaFixture()
			f.admit(1, "1", true)
			body := f.chunkBody(1)
			body["end_secs"] = 11
			Expect(f.post("/api/v1/chunk-complete", body).Code).To(Equal(http.StatusConflict))
			body = f.chunkBody(1)
			body["client_chunk_id"] = "different-capture"
			Expect(f.post("/api/v1/chunk-complete", body).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("rejects existing legacy sessions and accepted legacy work before its AI result exists", func() {
			f := newRecordingMediaFixture()
			testhelpers.CreateSession(dbConn, &db.Session{ProfileID: f.profile.ID, SessionID: f.sessionID})
			Expect(f.post("/api/v1/chunk-complete", f.chunkBody(1)).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Requests()).To(BeEmpty())
			f.sessionID = "WOD-20261103-" + strings.ReplaceAll(uuid.NewString(), "-", "")
			legacy := f.chunkBody(1)
			delete(legacy, "media_pipeline_version")
			delete(legacy, "client_chunk_id")
			delete(legacy, "capture_order")
			Expect(f.post("/api/v1/chunk-complete", legacy).Code).To(Equal(http.StatusAccepted))
			var marker db.RecordingMediaRun
			Expect(dbConn.Where("session_id = ?", f.sessionID).First(&marker).Error).To(Succeed())
			Expect(marker.Version).To(BeZero())
			Expect(marker.State).To(Equal("LEGACY"))
			Expect(marker.NextAttemptAt).To(BeNil())
			Expect(f.post("/api/v1/chunk-complete", f.chunkBody(1)).Code).To(Equal(http.StatusConflict))
		})

		It("rejects a legacy notification for an admitted v1 recording", func() {
			f := newRecordingMediaFixture()
			f.admit(1, "1", true)
			body := f.chunkBody(1)
			delete(body, "media_pipeline_version")
			delete(body, "client_chunk_id")
			delete(body, "capture_order")
			Expect(f.post("/api/v1/chunk-complete", body).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("keeps a session identity bound to one profile before any AI result exists", func() {
			f := newRecordingMediaFixture()
			testhelpers.CreateRecordingMediaRun(dbConn, &db.RecordingMediaRun{ProfileID: f.profile.ID, SessionID: f.sessionID, Version: 1})
			other := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: f.owner.ID})
			body := f.chunkBody(1)
			body["profile_id"] = other.ID
			body["gcs_uri"] = fmt.Sprintf("gs://test-bucket/videos/%d/%s/upload-1.mp4", other.ID, f.sessionID)
			Expect(f.post("/api/v1/chunk-complete", body).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Requests()).To(BeEmpty())
			var count int64
			Expect(dbConn.Model(&db.RecordingMediaRun{}).Where("session_id = ?", f.sessionID).Count(&count).Error).To(Succeed())
			Expect(count).To(Equal(int64(1)))
		})

		It("retains accepted work when the immediate queue dispatch fails", func() {
			f := newRecordingMediaFixture()
			queue := newBrokenQueueClient()
			DeferCleanup(queue.Close)
			storage, err := testhelpers.NewStorageClient("test-bucket", f.transport)
			Expect(err).NotTo(HaveOccurred())
			f.router = newTestRouter(controllers.Config{StorageClient: storage, BucketName: "test-bucket", QueueClient: queue})
			receipt := f.admit(1, "1", true)
			var chunk db.RecordingChunk
			Expect(dbConn.First(&chunk, receipt.ChunkID).Error).To(Succeed())
			Expect(chunk.AIDispatched).To(BeFalse())
			var prep db.ChunkMediaPreparation
			Expect(dbConn.Where("chunk_id = ?", chunk.ID).First(&prep).Error).To(Succeed())
			Expect(prep.State).To(Equal("PENDING"))
			Expect(prep.NextAttemptAt).NotTo(BeNil())
		})

		It("blocks archived and permanently cancelled recording ingress before storage access", func() {
			f := newRecordingMediaFixture()
			now := time.Now().UTC()
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: f.profile.ID, SessionID: f.sessionID, Status: "COMPLETED", ArchivedAt: &now, AutomaticProcessingCancelledAt: &now})
			Expect(f.post("/api/v1/chunk-complete", f.chunkBody(1)).Code).To(Equal(http.StatusConflict))
			Expect(dbConn.Model(&db.AnalysisResult{}).Where("session_id = ?", f.sessionID).Update("archived_at", nil).Error).To(Succeed())
			Expect(f.post("/api/v1/chunk-complete", f.chunkBody(1)).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Requests()).To(BeEmpty())
		})
	})
})

var _ = Describe("POST /api/v1/merge-chunks", func() {
	Context("recording media v1 manifest sealing", func() {
		It("seals and reuses all 238 admitted chunks without repeating preparation or discovery", func() {
			f := newRecordingMediaFixture()
			run := testhelpers.CreateRecordingMediaRun(dbConn, &db.RecordingMediaRun{ProfileID: f.profile.ID, SessionID: f.sessionID, Version: 1})
			receipts := make([]controllers.RecordingMediaChunkReceipt, 0, 238)
			for order := 1; order <= 238; order++ {
				chunk := testhelpers.CreateRecordingChunk(dbConn, &db.RecordingChunk{RunID: run.ID, CaptureOrder: order, CaptureStartSecs: float64((order - 1) * 10), CaptureEndSecs: float64(order * 10), SourceGeneration: int64(1000 + order), AIDispatched: true})
				testhelpers.CreateChunkMediaPreparation(dbConn, &db.ChunkMediaPreparation{RunID: run.ID, ChunkID: chunk.ID, State: "READY", OutputObject: fmt.Sprintf("videos/%d/%s/prepared/%d.mp4", f.profile.ID, f.sessionID, chunk.ID), OutputGeneration: int64(2000 + order)})
				receipts = append(receipts, controllers.RecordingMediaChunkReceipt{ChunkID: fmt.Sprint(chunk.ID), SourceGeneration: fmt.Sprint(chunk.SourceGeneration)})
			}
			Expect(f.post("/api/v1/merge-chunks", f.mergeBody(receipts[:237]...)).Code).To(Equal(http.StatusConflict))
			response := f.post("/api/v1/merge-chunks", f.mergeBody(receipts...))
			Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
			Expect(dbConn.First(&run, run.ID).Error).To(Succeed())
			var manifest []db.RecordingMediaManifestEntry
			Expect(json.Unmarshal(run.Manifest, &manifest)).To(Succeed())
			Expect(manifest).To(HaveLen(238))
			Expect(fmt.Sprint(manifest[237].ChunkID)).To(Equal(receipts[237].ChunkID))
			Expect(manifest[237].SourceGeneration).To(Equal(int64(1238)))
			sealedHash := run.ManifestHash
			Expect(f.post("/api/v1/merge-chunks", f.mergeBody(receipts...)).Code).To(Equal(http.StatusAccepted))
			Expect(dbConn.First(&run, run.ID).Error).To(Succeed())
			Expect(run.ManifestHash).To(Equal(sealedHash))
			var preparations int64
			Expect(dbConn.Model(&db.ChunkMediaPreparation{}).Where("run_id = ? AND state = 'READY'", run.ID).Count(&preparations).Error).To(Succeed())
			Expect(preparations).To(Equal(int64(238)))
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))
			Expect(pending[0].Type).To(Equal(worker.TypeCoordinateRecordingMedia))
			Expect(f.transport.Requests()).To(BeEmpty())
		})

		It("rejects an omitted tail, wrong order, wrong generation and non-contiguous captures", func() {
			f := newRecordingMediaFixture()
			first := f.admit(1, "1", true)
			second := f.admit(2, "2", false)
			Expect(f.post("/api/v1/merge-chunks", f.mergeBody(first)).Code).To(Equal(http.StatusConflict))
			Expect(f.post("/api/v1/merge-chunks", f.mergeBody(second, first)).Code).To(Equal(http.StatusConflict))
			wrong := second
			wrong.SourceGeneration = "3"
			Expect(f.post("/api/v1/merge-chunks", f.mergeBody(first, wrong)).Code).To(Equal(http.StatusConflict))
			Expect(dbConn.Model(&db.RecordingChunk{}).Where("id = ?", second.ChunkID).Update("capture_order", 3).Error).To(Succeed())
			Expect(f.post("/api/v1/merge-chunks", f.mergeBody(first, second)).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("seals an immutable manifest and resumes that same manifest from History", func() {
			f := newRecordingMediaFixture()
			first := f.admit(1, "1", true)
			second := f.admit(2, "2", false)
			response := f.post("/api/v1/merge-chunks", f.mergeBody(first, second))
			Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
			Expect(decodeMapBody(response)["media_run_id"]).To(Equal(first.MediaRunID))
			var sealed db.RecordingMediaRun
			Expect(dbConn.First(&sealed, first.MediaRunID).Error).To(Succeed())
			Expect(sealed.State).To(Equal("WAITING_PREPARATION"))
			Expect(sealed.ManifestHash).NotTo(BeEmpty())
			history := f.post("/api/v1/merge-chunks", map[string]any{"session_id": f.sessionID, "profile_id": f.profile.ID, "workout_type": "wod"})
			Expect(history.Code).To(Equal(http.StatusAccepted), history.Body.String())
			Expect(f.post("/api/v1/merge-chunks", f.mergeBody(second, first)).Code).To(Equal(http.StatusConflict))
			Expect(f.post("/api/v1/chunk-complete", f.chunkBody(3)).Code).To(Equal(http.StatusConflict))
			var after db.RecordingMediaRun
			Expect(dbConn.First(&after, sealed.ID).Error).To(Succeed())
			Expect(after.Manifest).To(Equal(sealed.Manifest))
			Expect(after.ManifestHash).To(Equal(sealed.ManifestHash))
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("requires string receipts and refuses to create a v1 run from merge alone", func() {
			f := newRecordingMediaFixture()
			body := f.mergeBody(controllers.RecordingMediaChunkReceipt{ChunkID: "1", SourceGeneration: "1"})
			Expect(f.post("/api/v1/merge-chunks", body).Code).To(Equal(http.StatusConflict))
			f.admit(1, "1", true)
			body["chunks"] = []map[string]any{{"chunk_id": 1, "source_generation": 1}}
			Expect(f.post("/api/v1/merge-chunks", body).Code).To(Equal(http.StatusBadRequest))
		})
	})
})

var _ = Describe("POST /api/v1/retry-analysis", func() {
	Context("recording media v1 checkpoints", func() {
		It("retries failed analysis from its ready pinned artifact with a new dispatch epoch", func() {
			f := newRecordingMediaFixture()
			run := testhelpers.CreateRecordingMediaRun(dbConn, &db.RecordingMediaRun{ProfileID: f.profile.ID, SessionID: f.sessionID, Version: 1, State: "FAILED", FailedStage: "analysis", MergedObject: fmt.Sprintf("videos/%d/%s/merged.mp4", f.profile.ID, f.sessionID), MergedGeneration: 7, ManifestHash: "sealed", Manifest: db.JSONDocument(`[]`), Payload: db.JSONDocument(`{}`), DispatchVersion: 4, AnalysisDispatched: true, AnalysisAttempts: 3})
			prep := testhelpers.CreateAnalysisVideoPreparation(dbConn, &db.AnalysisVideoPreparation{RunID: run.ID, State: "READY", OutputObject: fmt.Sprintf("videos/%d/%s/analysis.mp4", f.profile.ID, f.sessionID), OutputGeneration: 9})
			response := f.post("/api/v1/retry-analysis", map[string]any{"session_id": f.sessionID, "profile_id": f.profile.ID})
			Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
			Expect(dbConn.First(&run, run.ID).Error).To(Succeed())
			Expect(run.State).To(Equal("READY_FOR_ANALYSIS"))
			Expect(run.AnalysisDispatched).To(BeFalse())
			Expect(run.AnalysisAttempts).To(BeZero())
			Expect(run.DispatchVersion).To(Equal(int64(5)))
			Expect(dbConn.First(&prep, prep.ID).Error).To(Succeed())
			Expect(prep.OutputGeneration).To(Equal(int64(9)))
			Expect(f.transport.Requests()).To(BeEmpty())
		})
	})
})

var _ = Describe("POST /api/v1/history/:id/archive", func() {
	Context("recording media v1 active work", func() {
		It("blocks archive while a durable media run is active and fences failed runs after restore", func() {
			f := newRecordingMediaFixture()
			analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: f.profile.ID, SessionID: f.sessionID, Status: "FAILED"})
			run := testhelpers.CreateRecordingMediaRun(dbConn, &db.RecordingMediaRun{ProfileID: f.profile.ID, SessionID: f.sessionID, Version: 1, State: "PREPARING_ANALYSIS"})
			path := fmt.Sprintf("/api/v1/history/%d/archive", analysis.ID)
			Expect(f.post(path, map[string]any{}).Code).To(Equal(http.StatusConflict))
			Expect(dbConn.Model(&run).Update("state", "FAILED").Error).To(Succeed())
			Expect(f.post(path, map[string]any{}).Code).To(Equal(http.StatusOK))
			Expect(dbConn.First(&run, run.ID).Error).To(Succeed())
			Expect(run.State).To(Equal("CANCELLED"))
			Expect(f.post(fmt.Sprintf("/api/v1/history/%d/unarchive", analysis.ID), map[string]any{}).Code).To(Equal(http.StatusOK))
			Expect(f.post("/api/v1/retry-analysis", map[string]any{"session_id": f.sessionID, "profile_id": f.profile.ID}).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Requests()).To(BeEmpty())
		})
	})
})
