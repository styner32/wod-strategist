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
	"github.com/oklog/ulid/v2"
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
	return map[string]any{
		"session_id":      f.sessionID,
		"profile_id":      f.profile.ID,
		"client_chunk_id": fmt.Sprintf("capture-%d", order),
		"capture_order":   order,
		"gcs_uri":         fmt.Sprintf("gs://test-bucket/videos/%d/%s/upload-%d.mp4", f.profile.ID, f.sessionID, order),
		"workout_type":    "wod",
		"movements":       []string{"Air Squat"},
		"start_secs":      (order - 1) * 10,
		"end_secs":        order * 10,
	}
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
	ExpectWithOffset(1, response.Code).To(Equal(http.StatusAccepted), response.Body.String())
	var receipt controllers.RecordingMediaChunkReceipt
	ExpectWithOffset(1, json.Unmarshal(response.Body.Bytes(), &receipt)).To(Succeed())
	return receipt
}

func (f recordingMediaFixture) mergeBody(receipts ...controllers.RecordingMediaChunkReceipt) map[string]any {
	chunks := make([]controllers.RecordingMediaReceiptInput, 0, len(receipts))
	for _, receipt := range receipts {
		chunks = append(chunks, controllers.RecordingMediaReceiptInput{ChunkID: receipt.ChunkID, SourceGeneration: receipt.SourceGeneration})
	}
	return map[string]any{
		"session_id":           f.sessionID,
		"profile_id":           f.profile.ID,
		"expected_chunk_count": len(chunks),
		"chunks":               chunks,
		"workout_type":         "wod",
	}
}

func (f recordingMediaFixture) createFreshSession(workoutType string) {
	createResp := f.post("/api/v1/sessions", map[string]any{
		"profile_id":      f.profile.ID,
		"session_id":      f.sessionID,
		"idempotency_key": ulid.Make().String(),
		"workout_type":    workoutType,
		"movements":       []string{"Air Squat"},
	})
	ExpectWithOffset(1, createResp.Code).To(Equal(http.StatusOK), createResp.Body.String())
}

var _ = Describe("ChunkComplete", func() {
	var f recordingMediaFixture

	BeforeEach(func() {
		f = newRecordingMediaFixture()
	})

	Context("admission and idempotency", func() {
		It("returns a precise immutable receipt and dispatches preparation and AI once", func() {
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

			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("returns the original receipt without replacing the source object on duplicate client_chunk_id retry", func() {
			receipt := f.admit(1, "9007199254740993", true)

			retryBody := f.chunkBody(1)
			retryBody["gcs_uri"] = fmt.Sprintf("gs://test-bucket/videos/%d/%s/extra-upload.mov", f.profile.ID, f.sessionID)
			retry := f.post("/api/v1/chunk-complete", retryBody)
			Expect(retry.Code).To(Equal(http.StatusAccepted), retry.Body.String())

			var again controllers.RecordingMediaChunkReceipt
			Expect(json.Unmarshal(retry.Body.Bytes(), &again)).To(Succeed())
			Expect(again).To(Equal(receipt))

			var chunkCount, prepCount int64
			Expect(dbConn.Model(&db.RecordingChunk{}).Count(&chunkCount).Error).To(Succeed())
			Expect(chunkCount).To(Equal(int64(1)))
			Expect(dbConn.Model(&db.ChunkMediaPreparation{}).Count(&prepCount).Error).To(Succeed())
			Expect(prepCount).To(Equal(int64(1)))

			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("retains accepted work when immediate queue dispatch fails", func() {
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
			Expect(f.transport.Verify()).To(Succeed())
		})
	})

	Context("capture identity validation and conflicts", func() {
		It("rejects a chunk missing client_chunk_id or capture_order with 400", func() {
			body := f.chunkBody(1)
			delete(body, "client_chunk_id")
			Expect(f.post("/api/v1/chunk-complete", body).Code).To(Equal(http.StatusBadRequest))

			body = f.chunkBody(1)
			delete(body, "capture_order")
			Expect(f.post("/api/v1/chunk-complete", body).Code).To(Equal(http.StatusBadRequest))
			Expect(f.transport.Requests()).To(BeEmpty())
		})

		It("rejects a duplicate client_chunk_id with a different capture interval", func() {
			f.admit(1, "1", true)

			body := f.chunkBody(1)
			body["end_secs"] = 11
			Expect(f.post("/api/v1/chunk-complete", body).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("rejects a different client_chunk_id occupying an existing capture_order", func() {
			f.admit(1, "1", true)

			body := f.chunkBody(1)
			body["client_chunk_id"] = "different-capture"
			Expect(f.post("/api/v1/chunk-complete", body).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Verify()).To(Succeed())
		})
	})

	Context("pre-created fresh sessions and sensor placeholders", func() {
		It("admits chunks and merges a session pre-created via POST /api/v1/sessions", func() {
			f.sessionID = "WARMUP-20261124-01M85EDQRN1YKDV7V2KXVE6HWC"
			f.createFreshSession("warmup")

			first := f.admit(1, "9007199254740993", true)
			second := f.admit(2, "9007199254740994", false)

			var chunks []db.RecordingChunk
			Expect(dbConn.Where("session_id = ?", f.sessionID).Order("capture_order ASC").Find(&chunks).Error).To(Succeed())
			Expect(chunks).To(HaveLen(2))
			byChunkID := make(map[uint]db.RecordingChunk, len(chunks))
			for _, ch := range chunks {
				Expect(ch.AIType).To(Equal(worker.TypeChunkAnalysisWithSession))
				Expect(ch.AIDispatched).To(BeTrue())
				byChunkID[ch.ID] = ch
			}

			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(2))
			seenChunkIDs := make(map[uint]bool, len(pending))
			for _, task := range pending {
				Expect(task.Type).To(Equal(worker.TypeChunkAnalysisWithSession))
				var payload worker.VideoAnalysisWithSessionPayload
				Expect(json.Unmarshal(task.Payload, &payload)).To(Succeed())
				chunk, ok := byChunkID[payload.RecordingChunkID]
				Expect(ok).To(BeTrue())
				Expect(payload.SessionID).To(Equal(f.sessionID))
				Expect(payload.ProfileID).To(Equal(f.profile.ID))
				Expect(payload.FilePath).To(Equal(chunk.SourceURI))
				Expect(payload.SourceGeneration).To(Equal(chunk.SourceGeneration))
				seenChunkIDs[payload.RecordingChunkID] = true
			}
			Expect(seenChunkIDs).To(HaveLen(2))

			mergeResp := f.post("/api/v1/merge-chunks", f.mergeBody(first, second))
			Expect(mergeResp.Code).To(Equal(http.StatusAccepted), mergeResp.Body.String())
			Expect(f.transport.Verify()).To(Succeed())
		})

		It("admits chunks and merges when sensor-upload created a pending placeholder analysis row", func() {
			signingStorage, err := testhelpers.NewStorageClientWithSigning("test-bucket", f.transport)
			Expect(err).NotTo(HaveOccurred())
			queue := testhelpers.NewQueueClient()
			DeferCleanup(queue.Close)
			f.router = newTestRouterWithAuthService(controllers.Config{StorageClient: signingStorage, BucketName: "test-bucket", QueueClient: queue})

			f.sessionID = "WOD-20261124-" + ulid.Make().String()
			f.createFreshSession("wod")

			sensorResp := f.post("/api/v1/sessions/"+f.sessionID+"/sensor-upload", controllers.PrepareSensorUploadRequest{
				ProfileID:       f.profile.ID,
				RequestID:       uuid.NewString(),
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          strings.Repeat("a", 64),
			})
			Expect(sensorResp.Code).To(Equal(http.StatusOK), sensorResp.Body.String())

			var analysis db.AnalysisResult
			Expect(dbConn.Where("session_id = ?", f.sessionID).First(&analysis).Error).To(Succeed())
			Expect(analysis.Status).To(Equal("PENDING"))
			Expect(analysis.SensorState).To(Equal(db.SensorStateUploading))
			Expect(analysis.SensorVersion).To(Equal(int64(1)))

			first := f.admit(1, "9007199254740995", true)

			var run db.RecordingMediaRun
			Expect(dbConn.Where("profile_id = ? AND session_id = ?", f.profile.ID, f.sessionID).First(&run).Error).To(Succeed())
			Expect(run.Version).To(Equal(db.RecordingMediaVersion))
			Expect(run.State).To(Equal("RECORDING"))

			var chunk db.RecordingChunk
			Expect(dbConn.First(&chunk, first.ChunkID).Error).To(Succeed())
			Expect(chunk.RunID).To(Equal(run.ID))

			mergeResp := f.post("/api/v1/merge-chunks", f.mergeBody(first))
			Expect(mergeResp.Code).To(Equal(http.StatusAccepted), mergeResp.Body.String())
			Expect(f.transport.Verify()).To(Succeed())
		})

		DescribeTable("admits chunks when a pending sensor placeholder is in an active or completed sensor state",
			func(state string) {
				f.sessionID = "WOD-20261124-" + ulid.Make().String()
				testhelpers.CreateSession(dbConn, &db.Session{
					ProfileID:   f.profile.ID,
					SessionID:   f.sessionID,
					Status:      db.SessionStatusStarted,
					WorkoutType: "wod",
				})
				testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
					ProfileID:     f.profile.ID,
					SessionID:     f.sessionID,
					Status:        "PENDING",
					Output:        "",
					SensorState:   state,
					SensorVersion: 1,
				})

				f.admit(1, "9007199254740996", true)
				Expect(f.transport.Verify()).To(Succeed())
			},
			Entry("pending sensor", db.SensorStatePending),
			Entry("running sensor", db.SensorStateRunning),
			Entry("completed sensor", db.SensorStateCompleted),
		)
	})

	Context("existing session and legacy data protection", func() {
		It("rejects an existing legacy recording_media_runs row with 409 before storage access", func() {
			testhelpers.CreateRecordingMediaRun(dbConn, &db.RecordingMediaRun{
				ProfileID: f.profile.ID,
				SessionID: f.sessionID,
				Version:   0,
				State:     "LEGACY",
			})

			Expect(f.post("/api/v1/chunk-complete", f.chunkBody(1)).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Requests()).To(BeEmpty())
		})

		It("rejects a session bound to another profile's recording_media_runs row with 409", func() {
			testhelpers.CreateRecordingMediaRun(dbConn, &db.RecordingMediaRun{
				ProfileID: f.profile.ID,
				SessionID: f.sessionID,
				Version:   1,
			})
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

		It("rejects a pre-created session owned by another profile with 409", func() {
			other := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: f.owner.ID})
			f.sessionID = "WOD-20261124-" + ulid.Make().String()
			testhelpers.CreateSession(dbConn, &db.Session{
				ProfileID:   other.ID,
				SessionID:   f.sessionID,
				Status:      db.SessionStatusStarted,
				WorkoutType: "wod",
			})

			resp := f.post("/api/v1/chunk-complete", f.chunkBody(1))
			Expect(resp.Code).To(Equal(http.StatusConflict), resp.Body.String())
			body := decodeMapBody(resp)
			Expect(body["error_code"]).To(Equal("RECORDING_MEDIA_CONFLICT"))
			Expect(body["error"]).To(ContainSubstring("another profile"))
			Expect(f.transport.Requests()).To(BeEmpty())
		})

		It("rejects a cross-profile sensor placeholder with 403", func() {
			other := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: f.owner.ID})
			f.sessionID = "WOD-20261124-" + ulid.Make().String()
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				ProfileID:     other.ID,
				SessionID:     f.sessionID,
				Status:        "PENDING",
				SensorState:   db.SensorStateUploading,
				SensorVersion: 1,
			})

			resp := f.post("/api/v1/chunk-complete", f.chunkBody(1))
			Expect(resp.Code).To(Equal(http.StatusForbidden), resp.Body.String())
			Expect(f.transport.Requests()).To(BeEmpty())
		})

		DescribeTable("rejects non-fresh sessions with 409 before storage access",
			func(build func(profileID uint, sid string) db.Session) {
				f.sessionID = "WOD-20261124-" + ulid.Make().String()
				attr := build(f.profile.ID, f.sessionID)
				testhelpers.CreateSession(dbConn, &attr)

				resp := f.post("/api/v1/chunk-complete", f.chunkBody(1))
				Expect(resp.Code).To(Equal(http.StatusConflict), resp.Body.String())
				body := decodeMapBody(resp)
				Expect(body["error_code"]).To(Equal("RECORDING_MEDIA_CONFLICT"))
				Expect(body["error"]).To(ContainSubstring("only new recordings"))
				Expect(f.transport.Requests()).To(BeEmpty())
			},
			Entry("legacy session without started status", func(profileID uint, sid string) db.Session {
				return db.Session{ProfileID: profileID, SessionID: sid, WorkoutType: "wod"}
			}),
			Entry("completed session", func(profileID uint, sid string) db.Session {
				return db.Session{ProfileID: profileID, SessionID: sid, Status: db.SessionStatusCompleted, WorkoutType: "wod"}
			}),
			Entry("failed session", func(profileID uint, sid string) db.Session {
				return db.Session{ProfileID: profileID, SessionID: sid, Status: db.SessionStatusFailed, WorkoutType: "wod"}
			}),
			Entry("backfill idempotency key", func(profileID uint, sid string) db.Session {
				return db.Session{ProfileID: profileID, SessionID: sid, Status: db.SessionStatusStarted, IdempotencyKey: "backfill:" + sid, WorkoutType: "wod"}
			}),
			Entry("live idempotency key", func(profileID uint, sid string) db.Session {
				return db.Session{ProfileID: profileID, SessionID: sid, Status: db.SessionStatusStarted, IdempotencyKey: "live:" + sid, WorkoutType: "wod"}
			}),
			Entry("wod idempotency key", func(profileID uint, sid string) db.Session {
				return db.Session{ProfileID: profileID, SessionID: sid, Status: db.SessionStatusStarted, IdempotencyKey: "wod:" + sid, WorkoutType: "wod"}
			}),
			Entry("session with effort", func(profileID uint, sid string) db.Session {
				effort := 7
				return db.Session{ProfileID: profileID, SessionID: sid, Status: db.SessionStatusStarted, Effort: &effort, WorkoutType: "wod"}
			}),
			Entry("session with activity_summary", func(profileID uint, sid string) db.Session {
				return db.Session{ProfileID: profileID, SessionID: sid, Status: db.SessionStatusStarted, ActivitySummary: db.NullableJSONDocument(`{"events":[]}`), WorkoutType: "wod"}
			}),
		)

		DescribeTable("rejects non-placeholder or archived/cancelled analysis_results rows with 409 before storage access",
			func(build func(profileID uint, sid string) db.AnalysisResult, expectConflictBody bool) {
				f.sessionID = "WOD-20261124-" + ulid.Make().String()
				testhelpers.CreateSession(dbConn, &db.Session{
					ProfileID:   f.profile.ID,
					SessionID:   f.sessionID,
					Status:      db.SessionStatusStarted,
					WorkoutType: "wod",
				})
				attr := build(f.profile.ID, f.sessionID)
				testhelpers.CreateAnalysisResult(dbConn, &attr)

				resp := f.post("/api/v1/chunk-complete", f.chunkBody(1))
				Expect(resp.Code).To(Equal(http.StatusConflict), resp.Body.String())
				if expectConflictBody {
					body := decodeMapBody(resp)
					Expect(body["error_code"]).To(Equal("RECORDING_MEDIA_CONFLICT"))
					Expect(body["error"]).To(ContainSubstring("only new recordings"))
				}
				Expect(f.transport.Requests()).To(BeEmpty())
			},
			Entry("non-sensor PENDING analysis", func(profileID uint, sid string) db.AnalysisResult {
				return db.AnalysisResult{ProfileID: profileID, SessionID: sid, Status: "PENDING", SensorState: db.SensorStateNone, SensorVersion: 0}
			}, true),
			Entry("PENDING sensor row with non-empty output", func(profileID uint, sid string) db.AnalysisResult {
				return db.AnalysisResult{ProfileID: profileID, SessionID: sid, Status: "PENDING", Output: "partial analysis", SensorState: db.SensorStateUploading, SensorVersion: 1}
			}, true),
			Entry("COMPLETED analysis with sensor completed", func(profileID uint, sid string) db.AnalysisResult {
				return db.AnalysisResult{ProfileID: profileID, SessionID: sid, Status: "COMPLETED", Output: "done", SensorState: db.SensorStateCompleted, SensorVersion: 1}
			}, true),
			Entry("FAILED analysis with sensor completed", func(profileID uint, sid string) db.AnalysisResult {
				return db.AnalysisResult{ProfileID: profileID, SessionID: sid, Status: "FAILED", Output: "err", SensorState: db.SensorStateCompleted, SensorVersion: 1}
			}, true),
			Entry("archived sensor placeholder", func(profileID uint, sid string) db.AnalysisResult {
				now := time.Now().UTC()
				return db.AnalysisResult{ProfileID: profileID, SessionID: sid, Status: "PENDING", SensorState: db.SensorStateUploading, SensorVersion: 1, ArchivedAt: &now}
			}, false),
			Entry("cancelled sensor placeholder", func(profileID uint, sid string) db.AnalysisResult {
				now := time.Now().UTC()
				return db.AnalysisResult{ProfileID: profileID, SessionID: sid, Status: "PENDING", SensorState: db.SensorStateUploading, SensorVersion: 1, AutomaticProcessingCancelledAt: &now}
			}, false),
		)

		It("blocks archived and restored-after-cancel analysis rows before storage access", func() {
			now := time.Now().UTC()
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				ProfileID:                      f.profile.ID,
				SessionID:                      f.sessionID,
				Status:                         "COMPLETED",
				ArchivedAt:                     &now,
				AutomaticProcessingCancelledAt: &now,
			})
			Expect(f.post("/api/v1/chunk-complete", f.chunkBody(1)).Code).To(Equal(http.StatusConflict))
			Expect(dbConn.Model(&db.AnalysisResult{}).Where("session_id = ?", f.sessionID).Update("archived_at", nil).Error).To(Succeed())
			Expect(f.post("/api/v1/chunk-complete", f.chunkBody(1)).Code).To(Equal(http.StatusConflict))
			Expect(f.transport.Requests()).To(BeEmpty())
		})

		It("rejects a session with existing chunk_analysis_results rows with 409", func() {
			f.sessionID = "WOD-20261124-" + ulid.Make().String()
			testhelpers.CreateSession(dbConn, &db.Session{
				ProfileID:   f.profile.ID,
				SessionID:   f.sessionID,
				Status:      db.SessionStatusStarted,
				WorkoutType: "wod",
			})
			testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
				ProfileID: f.profile.ID,
				SessionID: f.sessionID,
				FilePath:  "gs://test-bucket/videos/existing.mp4",
				Status:    "COMPLETED",
			})

			resp := f.post("/api/v1/chunk-complete", f.chunkBody(1))
			Expect(resp.Code).To(Equal(http.StatusConflict), resp.Body.String())
			body := decodeMapBody(resp)
			Expect(body["error_code"]).To(Equal("RECORDING_MEDIA_CONFLICT"))
			Expect(body["error"]).To(ContainSubstring("only new recordings"))
			Expect(f.transport.Requests()).To(BeEmpty())
		})
	})
})

var _ = Describe("MergeChunks", func() {
	var f recordingMediaFixture

	BeforeEach(func() {
		f = newRecordingMediaFixture()
	})

	Context("recording media manifest sealing", func() {
		It("seals and reuses all 238 admitted chunks without repeating preparation or discovery", func() {
			run := testhelpers.CreateRecordingMediaRun(dbConn, &db.RecordingMediaRun{ProfileID: f.profile.ID, SessionID: f.sessionID, Version: 1})
			receipts := make([]controllers.RecordingMediaChunkReceipt, 0, 238)
			for order := 1; order <= 238; order++ {
				chunk := testhelpers.CreateRecordingChunk(dbConn, &db.RecordingChunk{
					RunID:            run.ID,
					CaptureOrder:     order,
					CaptureStartSecs: float64((order - 1) * 10),
					CaptureEndSecs:   float64(order * 10),
					SourceGeneration: int64(1000 + order),
					AIDispatched:     true,
				})
				testhelpers.CreateChunkMediaPreparation(dbConn, &db.ChunkMediaPreparation{
					RunID:            run.ID,
					ChunkID:          chunk.ID,
					State:            "READY",
					OutputObject:     fmt.Sprintf("videos/%d/%s/prepared/%d.mp4", f.profile.ID, f.sessionID, chunk.ID),
					OutputGeneration: int64(2000 + order),
				})
				receipts = append(receipts, controllers.RecordingMediaChunkReceipt{
					ChunkID:          fmt.Sprint(chunk.ID),
					SourceGeneration: fmt.Sprint(chunk.SourceGeneration),
				})
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

		It("requires string receipts and refuses to create a run from merge alone", func() {
			body := f.mergeBody(controllers.RecordingMediaChunkReceipt{ChunkID: "1", SourceGeneration: "1"})
			Expect(f.post("/api/v1/merge-chunks", body).Code).To(Equal(http.StatusConflict))

			f.admit(1, "1", true)
			body["chunks"] = []map[string]any{{"chunk_id": 1, "source_generation": 1}}
			Expect(f.post("/api/v1/merge-chunks", body).Code).To(Equal(http.StatusBadRequest))
		})
	})
})

var _ = Describe("RetryAnalysis", func() {
	var f recordingMediaFixture

	BeforeEach(func() {
		f = newRecordingMediaFixture()
	})

	Context("recording media checkpoints", func() {
		It("retries failed analysis from its ready pinned artifact with a new dispatch epoch", func() {
			run := testhelpers.CreateRecordingMediaRun(dbConn, &db.RecordingMediaRun{
				ProfileID:          f.profile.ID,
				SessionID:          f.sessionID,
				Version:            1,
				State:              "FAILED",
				FailedStage:        "analysis",
				MergedObject:       fmt.Sprintf("videos/%d/%s/merged.mp4", f.profile.ID, f.sessionID),
				MergedGeneration:   7,
				ManifestHash:       "sealed",
				Manifest:           db.JSONDocument(`[]`),
				Payload:            db.JSONDocument(`{}`),
				DispatchVersion:    4,
				AnalysisDispatched: true,
				AnalysisAttempts:   3,
			})
			prep := testhelpers.CreateAnalysisVideoPreparation(dbConn, &db.AnalysisVideoPreparation{
				RunID:            run.ID,
				State:            "READY",
				OutputObject:     fmt.Sprintf("videos/%d/%s/analysis.mp4", f.profile.ID, f.sessionID),
				OutputGeneration: 9,
			})

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

var _ = Describe("ArchiveHistory", func() {
	var f recordingMediaFixture

	BeforeEach(func() {
		f = newRecordingMediaFixture()
	})

	Context("recording media active work", func() {
		It("blocks archive while a durable media run is active and fences failed runs after restore", func() {
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
