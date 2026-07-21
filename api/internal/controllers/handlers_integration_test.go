package controllers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/auth"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	dbConn      *gorm.DB
	inspector   *asynq.Inspector
	authService *auth.Service
)

var _ = BeforeSuite(func() {
	var err error
	dbConn, err = testhelpers.InitDB()
	Expect(err).NotTo(HaveOccurred())
	inspector = testhelpers.NewQueueInspector()

	testJWTSecret := []byte(os.Getenv("JWT_SIGNING_SECRET"))
	authService = auth.NewService(dbConn, []byte(testJWTSecret))

	testhelpers.InitLogger()
})

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestRouter(config controllers.Config) *gin.Engine {
	gin.SetMode(gin.TestMode)

	config.DB = dbConn
	config.AnalysisResults = controllers.NewGormAnalysisResultRepository(dbConn)
	config.Profiles = controllers.NewGormProfileRepository(dbConn)
	config.HighlightResults = controllers.NewGormHighlightResultRepository(dbConn)
	if config.StorageClient == nil {
		storageClient, err := testhelpers.NewStorageClient("test-bucket", testhelpers.NewMockTransport())
		Expect(err).NotTo(HaveOccurred())
		config.StorageClient = storageClient
	}

	controller, err := controllers.New(config)
	Expect(err).NotTo(HaveOccurred())
	router, err := server.SetupRouter("test", "test-api-key", nil, controller, nil, nil)
	Expect(err).NotTo(HaveOccurred())
	return router
}

func newTestRouterWithAuthService(config controllers.Config) *gin.Engine {
	gin.SetMode(gin.TestMode)
	if os.Getenv("SHOW_LOG") == "true" {
		loggerConfig := zap.NewDevelopmentConfig()
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
		loggerConfig.DisableStacktrace = true
		logger.Log, _ = loggerConfig.Build()
	} else {
		logger.Log = zap.NewNop()
	}

	config.DB = dbConn
	config.AnalysisResults = controllers.NewGormAnalysisResultRepository(dbConn)
	config.Profiles = controllers.NewGormProfileRepository(dbConn)
	config.HighlightResults = controllers.NewGormHighlightResultRepository(dbConn)
	if config.StorageClient == nil {
		storageClient, err := testhelpers.NewStorageClient("test-bucket", testhelpers.NewMockTransport())
		Expect(err).NotTo(HaveOccurred())
		config.StorageClient = storageClient
	}

	controller, err := controllers.New(config)
	Expect(err).NotTo(HaveOccurred())
	router, err := server.SetupRouter("test", "test-api-key", nil, controller, nil, authService)
	Expect(err).NotTo(HaveOccurred())
	return router
}

func newAuthorizedJSONRequest(method string, path string, body string, user ...*db.User) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-api-key")

	if len(user) > 0 {
		token, err := authService.IssueTokenByUser(user[0])
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return req
}

func decodeMapBody(w *httptest.ResponseRecorder) map[string]any {
	var body map[string]any
	Expect(json.Unmarshal(w.Body.Bytes(), &body)).To(Succeed())
	return body
}

// newBrokenQueueClient returns an asynq.Client that will fail on Enqueue
// because it points at an unreachable Redis address.
func newBrokenQueueClient() *asynq.Client {
	return asynq.NewClient(asynq.RedisClientOpt{Addr: "localhost:1"})
}

// gcsUploadURL builds the exact URL path the GCS Go client sends for a
// multipart upload to the given bucket and object name.
func gcsUploadURL(bucket, objectName string) string {
	return "/upload/storage/v1/b/" + bucket + "/o?alt=json&name=" + objectName + "&prettyPrint=false&projection=full&uploadType=multipart"
}

// gcsListObjectsURL builds the URL path the GCS JSON API uses to list objects
// under a prefix. MockTransport checks only the query params specified in the
// expectation, so other params (alt, prettyPrint, projection) are ignored.
func gcsListObjectsURL(bucket, prefix string) string {
	return "/storage/v1/b/" + bucket + "/o?prefix=" + url.QueryEscape(prefix)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

var _ = Describe("Controller handlers", func() {
	var router *gin.Engine
	var profileID uint

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)

		p := testhelpers.CreateProfile(dbConn, &db.Profile{})
		profileID = p.ID
	})

	Describe("Health", func() {
		It("returns the configured commit and RFC3339 time", func() {
			router := newTestRouter(controllers.Config{GitCommit: "abc123"})

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			body := decodeMapBody(w)
			Expect(body["status"]).To(Equal("ok"))
			Expect(body["commit"]).To(Equal("abc123"))
			Expect(body).To(HaveKey("time"))
		})
	})

	Describe("POST /api/v1/upload-url", func() {
		var router *gin.Engine

		BeforeEach(func() {
			transport := testhelpers.NewMockTransport()
			storageClient, err := testhelpers.NewStorageClientWithSigning("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())
			router = newTestRouter(controllers.Config{StorageClient: storageClient, BucketName: "test-bucket"})
		})

		It("returns bad request for malformed json", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-url", `{"session_id":`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(Equal("invalid request body"))
		})

		It("requires session_id", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-url", `{"filename":"video.mp4"}`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(Equal("session_id is required"))
		})

		It("requires filename", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-url", `{"session_id":"session-1"}`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(Equal("filename is required"))
		})

		It("rejects invalid session_id and path traversal", func() {
			req := newAuthorizedJSONRequest(
				http.MethodPost,
				"/api/v1/upload-url",
				`{"session_id":"../../session-1","filename":"..\\\\folder\\\\video.mp4"}`,
			)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			body := decodeMapBody(w)
			Expect(body["error"]).To(Equal("invalid session_id format"))
		})

		It("returns internal error when storage fails", func() {
			// Client WITHOUT signing credentials → GenerateSignedURL will fail.
			transport := testhelpers.NewMockTransport()
			noSignClient, err := testhelpers.NewStorageClient("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())
			failRouter := newTestRouter(controllers.Config{StorageClient: noSignClient, BucketName: "test-bucket"})

			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-url", `{"session_id":"session-1","filename":"video.mp4"}`)
			w := httptest.NewRecorder()
			failRouter.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to generate upload URL"))
		})
	})

	Describe("POST /api/v1/upload-complete", func() {
		var queueClient *asynq.Client
		var router *gin.Engine

		BeforeEach(func() {
			queueClient = testhelpers.NewQueueClient()
			transport := testhelpers.NewMockTransport()
			storageClient, err := testhelpers.NewStorageClientWithSigning("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())
			router = newTestRouter(controllers.Config{
				QueueClient:          queueClient,
				StorageClient:        storageClient,
				BucketName:           "test-bucket",
				NewVideoAnalysisTask: worker.NewVideoAnalysisTask,
			})
		})

		DescribeTable("returns validation errors for bad input",
			func(body string, wantStatus int, wantError string) {
				req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-complete", body)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)

				Expect(w.Code).To(Equal(wantStatus))
				Expect(decodeMapBody(w)["error"]).To(Equal(wantError))
			},
			Entry("malformed json", `{"session_id":`, http.StatusBadRequest, "invalid request body"),
			Entry("missing session id", `{"gcs_uri":"gs://bucket/video.mp4","movements":[]}`, http.StatusBadRequest, "session_id is required"),
			Entry("missing gcs uri", `{"session_id":"session-1","movements":[]}`, http.StatusBadRequest, "gcs_uri is required"),
			Entry("missing movements", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4"}`, http.StatusBadRequest, "movements is required"),
			Entry("missing profile id", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[]}`, http.StatusBadRequest, "profile_id is required"),
			Entry("invalid gcs scheme", `{"session_id":"session-1","gcs_uri":"https://bucket/video.mp4","movements":[],"profile_id":1}`, http.StatusBadRequest, "invalid GCS URI"),
			Entry("missing gcs bucket", `{"session_id":"session-1","gcs_uri":"gs:///video.mp4","movements":[],"profile_id":1}`, http.StatusBadRequest, "invalid GCS URI"),
			Entry("too many movements", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":`+repeatedJSONString("Burpee", 101)+`,"profile_id":1}`, http.StatusBadRequest, "too many movements"),
			Entry("empty movement name", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[""],"profile_id":1}`, http.StatusBadRequest, "movement name cannot be empty"),
			Entry("invalid injury", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[],"injuries":["Head"],"profile_id":1}`, http.StatusBadRequest, "invalid injuries"),
			Entry("too many injuries", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[],"injuries":`+repeatedJSONString("Left Knee", 100)+`,"profile_id":1}`, http.StatusBadRequest, "too many injuries"),
		)

		It("returns internal error when enqueue fails", func() {
			failRouter := newTestRouter(controllers.Config{
				QueueClient:          newBrokenQueueClient(),
				StorageClient:        nil,
				BucketName:           "test-bucket",
				NewVideoAnalysisTask: worker.NewVideoAnalysisTask,
			})

			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-complete", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":["Burpee"],"injuries":["Left Knee"],"workout_type":"wod","profile_id":1}`)
			w := httptest.NewRecorder()
			failRouter.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to enqueue task"))
		})

		It("accepts empty movements and normalizes the default workout type", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-complete", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[],"profile_id":1}`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusAccepted))

			body := decodeMapBody(w)
			Expect(body["message"]).To(Equal("Analysis started"))
			Expect(body["task_id"]).NotTo(BeEmpty())
			Expect(body["session_id"]).To(Equal("session-1"))

			// Verify the enqueued task payload via Redis inspector.
			// Filter to our session to avoid cross-suite contamination when
			// go test runs worker and controller suites in parallel.
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())

			var matched []asynq.TaskInfo
			for _, t := range pending {
				if t.Type != worker.TypeVideoAnalysis {
					continue
				}
				var p worker.VideoAnalysisPayload
				if json.Unmarshal(t.Payload, &p) == nil && p.SessionID == "session-1" {
					matched = append(matched, *t)
				}
			}
			Expect(matched).To(HaveLen(1))

			var payload worker.VideoAnalysisPayload
			Expect(json.Unmarshal(matched[0].Payload, &payload)).To(Succeed())
			Expect(payload.WorkoutType).To(Equal(worker.WorkoutTypeWOD))
			Expect(payload.Movements).To(BeEmpty())
		})

		It("updates movement hints for an existing matching session", func() {
			testhelpers.CreateSession(dbConn, &db.Session{
				SessionID:     "session-hints",
				ProfileID:     profileID,
				MovementHints: db.JSONDocument(`["Old hint"]`),
			})

			body := fmt.Sprintf(`{"session_id":"session-hints","gcs_uri":"gs://bucket/video.mp4","movements":["Pull-up","Sandbag Over Shoulder"],"profile_id":%d}`, profileID)
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-complete", body)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusAccepted), w.Body.String())
			var session db.Session
			Expect(dbConn.Where("session_id = ?", "session-hints").First(&session).Error).NotTo(HaveOccurred())
			Expect(string(session.MovementHints)).To(MatchJSON(`["Pull-up","Sandbag Over Shoulder"]`))
		})
	})

	Describe("POST /api/v1/upload", func() {
		var queueClient *asynq.Client
		var transport *testhelpers.MockTransport
		var router *gin.Engine

		BeforeEach(func() {
			queueClient = testhelpers.NewQueueClient()
			transport = testhelpers.NewMockTransport()
			storageClient, err := testhelpers.NewStorageClientWithSigning("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())
			router = newTestRouter(controllers.Config{
				QueueClient:          queueClient,
				StorageClient:        storageClient,
				BucketName:           "test-bucket",
				NewVideoAnalysisTask: worker.NewVideoAnalysisTask,
			})
			testhelpers.CreateSession(dbConn, &db.Session{
				SessionID: "session-1",
				ProfileID: profileID,
			})
		})

		It("requires session_id", func() {
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			Expect(writer.WriteField("session_id", "   ")).To(Succeed())
			Expect(writer.Close()).To(Succeed())

			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(Equal("session_id is required"))
		})

		It("requires a file", func() {
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			Expect(writer.WriteField("session_id", "session-1")).To(Succeed())
			Expect(writer.Close()).To(Succeed())

			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(Equal("file is required"))
		})

		It("rejects a current session that has no resolvable profile", func() {
			body, contentType := multipartRequestBody("WOD-20260715-01J00000000000000000000000", "video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(Equal("profile_id is required"))
			Expect(transport.Requests()).To(BeEmpty())
		})

		It("returns internal error when upload fails", func() {
			// Register a mock that returns 500 for the upload request.
			transport.New("https://storage.googleapis.com").
				Post(gcsUploadURL("test-bucket", "videos/0/session-1/video.mp4")).
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{"error": map[string]any{"message": "boom"}})

			body, contentType := multipartRequestBody("session-1", "../../video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to upload file"))
		})

		It("returns internal error when enqueue fails", func() {
			// Storage upload succeeds, but queue enqueue fails.
			transport.New("https://storage.googleapis.com").
				Post(gcsUploadURL("test-bucket", "videos/0/session-1/video.mp4")).
				Reply(http.StatusOK).
				JSON(map[string]any{"name": "videos/0/session-1/video.mp4"})

			failRouter := newTestRouter(controllers.Config{
				QueueClient: newBrokenQueueClient(),
				StorageClient: func() controllers.ObjectStorage {
					t := testhelpers.NewMockTransport()
					t.New("https://storage.googleapis.com").
						Post(gcsUploadURL("test-bucket", "videos/0/session-1/video.mp4")).
						Reply(http.StatusOK).
						JSON(map[string]any{"name": "videos/0/session-1/video.mp4"})
					c, err := testhelpers.NewStorageClientWithSigning("test-bucket", t)
					Expect(err).NotTo(HaveOccurred())
					return c
				}(),
				BucketName:           "test-bucket",
				NewVideoAnalysisTask: worker.NewVideoAnalysisTask,
			})

			body, contentType := multipartRequestBody("session-1", "video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			failRouter.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to enqueue task"))
		})

		It("uploads the file and sanitizes the object name", func() {
			// Register success for the GCS upload with sanitized object name.
			transport.New("https://storage.googleapis.com").
				Post(gcsUploadURL("test-bucket", "videos/0/session-1/video.mp4")).
				Reply(http.StatusOK).
				JSON(map[string]any{"name": "videos/0/session-1/video.mp4"})

			body, contentType := multipartRequestBody("session-1", "..\\\\folder\\\\video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusAccepted))

			// Verify the upload went to the correct GCS path.
			Expect(transport.Verify()).To(Succeed())
			reqs := transport.Requests()
			Expect(reqs).To(HaveLen(1))
			Expect(reqs[0].URL).To(ContainSubstring("session-1%2Fvideo.mp4"))

			bodyMap := decodeMapBody(w)
			Expect(bodyMap["message"]).To(Equal("File uploaded and analysis started"))
			Expect(bodyMap["task_id"]).NotTo(BeEmpty())
			Expect(bodyMap["file_url"]).To(Equal("gs://test-bucket/videos/0/session-1/video.mp4"))

			// Verify enqueued task payload.
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))

			var payload worker.VideoAnalysisPayload
			Expect(json.Unmarshal(pending[0].Payload, &payload)).To(Succeed())
			Expect(payload.SessionID).To(Equal("session-1"))
			Expect(payload.FilePath).To(Equal("gs://test-bucket/videos/0/session-1/video.mp4"))
			Expect(payload.ProfileID).To(Equal(profileID))
		})

		It("rejects legacy upload when sessionID is invalid", func() {
			body, contentType := multipartRequestBody("../../session-1", "video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(Equal("invalid session_id format"))
		})

		It("resolves the profile from an old-format session ID", func() {
			sessionID := fmt.Sprintf("P%d-WOD-2026-07-15-18-21", profileID)
			objectName := "videos/0/" + sessionID + "/video.mp4"
			transport.New("https://storage.googleapis.com").
				Post(gcsUploadURL("test-bucket", objectName)).
				Reply(http.StatusOK).
				JSON(map[string]any{"name": objectName})

			body, contentType := multipartRequestBody(sessionID, "video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusAccepted))
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))

			var payload worker.VideoAnalysisPayload
			Expect(json.Unmarshal(pending[0].Payload, &payload)).To(Succeed())
			Expect(payload.ProfileID).To(Equal(profileID))
		})
	})

	Describe("GET /api/v1/analysis/:session_id", func() {
		const sessionA = "session-sanitize-a"
		const sessionB = "session-sanitize-b"

		BeforeEach(func() {
			router = newTestRouter(controllers.Config{})

			user := testhelpers.CreateUser(dbConn, &db.User{
				Username: "test-user",
			})

			profile := testhelpers.CreateProfile(dbConn, &db.Profile{
				UserID:       user.ID,
				FitnessLevel: "intermediate",
			})

			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         sessionA,
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				Output:            "output-a",
				HighlightSegments: `[{"start_time":1.5,"end_time":4.5,"description":"Good rep"}]`,
			})
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         sessionB,
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				Output:            "output-b",
				HighlightSegments: `[{"start":"0:10","end":"0:09","type":"best_form","movement":"Air Squat","reason":"reversed"}]`,
			})
		})

		It("returns repository results for the requested session", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/analysis/"+sessionA, nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var results []db.AnalysisResult
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
			Expect(results[0].SessionID).To(Equal(sessionA))
			Expect(results[0].Output).To(Equal("output-a"))
		})

		It("preserves result field types while normalizing legacy highlights", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/analysis/"+sessionA, nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var results []map[string]any
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(1))

			result := results[0]
			Expect(result["id"]).To(BeAssignableToTypeOf(float64(0)))
			Expect(result["session_id"]).To(Equal(sessionA))
			Expect(result["profile_id"]).To(BeAssignableToTypeOf(float64(0)))
			Expect(result["status"]).To(Equal("COMPLETED"))
			Expect(result["output"]).To(Equal("output-a"))
			Expect(result["created_at"]).To(BeAssignableToTypeOf(""))
			Expect(result["updated_at"]).To(BeAssignableToTypeOf(""))

			highlightSegments, ok := result["highlight_segments"].(string)
			Expect(ok).To(BeTrue(), "highlight_segments must remain a JSON-encoded string")
			Expect(json.Valid([]byte(highlightSegments))).To(BeTrue())
			var normalized []worker.HighlightSegment
			Expect(json.Unmarshal([]byte(highlightSegments), &normalized)).To(Succeed())
			Expect(normalized).To(HaveLen(1))
			Expect(normalized[0].Version).To(Equal(2))
			Expect(normalized[0].Type).To(Equal("key_moment"))
			Expect(normalized[0].Observations).To(HaveLen(1))
			Expect(normalized[0].Observations[0].Type).To(Equal(worker.HighlightObservationTechnique))
			Expect(normalized[0].Observations[0].Reason).To(Equal("Good rep"))
		})

		It("GET /analysis/:session_id returns only the requested session", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/analysis/"+sessionB, nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var results []db.AnalysisResult
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
			Expect(results[0].SessionID).To(Equal(sessionB))
			Expect(results[0].Output).To(Equal("output-b"))
			Expect(results[0].HighlightSegments).To(MatchJSON(`[]`))
		})
	})

	Describe("GET /api/v1/history", func() {
		It("does not list GCS objects while loading history", func() {
			transport := testhelpers.NewMockTransport()
			storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())
			router := newTestRouter(controllers.Config{
				StorageClient: storageClient,
				BucketName:    "test-bucket",
			})

			Expect(dbConn.Create(&db.AnalysisResult{
				SessionID:       "session-with-video",
				Status:          "COMPLETED",
				Output:          "ok",
				ProfileID:       profileID,
				AvailableVideos: db.CommaStringArray{"merged"},
			}).Error).NotTo(HaveOccurred())

			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profileID), nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
			Expect(transport.Requests()).To(BeEmpty())

			var results []map[string]any
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
			Expect(results[0]).To(HaveKey("available_videos"))
			Expect(results[0]["available_videos"]).To(ContainElement("merged"))
		})

		It("returns recent history and enforces the limit", func() {
			// Seed one result so we get a non-empty response.
			Expect(dbConn.Create(&db.AnalysisResult{
				SessionID: "session-1",
				Status:    "COMPLETED",
				Output:    "ok",
				ProfileID: profileID,
			}).Error).NotTo(HaveOccurred())

			req := httptest.NewRequest(http.MethodGet, "/api/v1/history?profile_id=1", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var results []db.AnalysisResult
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
		})

		It("supports cursor pagination using before_id and limit parameters", func() {
			// Seed 3 results with distinct session IDs
			for i := 1; i <= 3; i++ {
				Expect(dbConn.Create(&db.AnalysisResult{
					SessionID: fmt.Sprintf("paginated-session-%d", i),
					Status:    "COMPLETED",
					Output:    fmt.Sprintf("workout-%d", i),
					ProfileID: profileID,
				}).Error).NotTo(HaveOccurred())
			}

			// First request: no before_id, limit=2
			req1 := httptest.NewRequest(http.MethodGet, "/api/v1/history?profile_id=1&limit=2", nil)
			req1.Header.Set("X-API-Key", "test-api-key")
			w1 := httptest.NewRecorder()
			router.ServeHTTP(w1, req1)

			Expect(w1.Code).To(Equal(http.StatusOK))
			var results1 []db.AnalysisResult
			Expect(json.Unmarshal(w1.Body.Bytes(), &results1)).To(Succeed())
			Expect(results1).To(HaveLen(2))
			// Since results are ordered by id desc, expected results1 contains the latest two seeded
			Expect(results1[0].SessionID).To(Equal("paginated-session-3"))
			Expect(results1[1].SessionID).To(Equal("paginated-session-2"))

			// Second request: before_id = results1[1].ID (paginated-session-2), limit=2
			cursorID := results1[1].ID
			req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=1&before_id=%d&limit=2", cursorID), nil)
			req2.Header.Set("X-API-Key", "test-api-key")
			w2 := httptest.NewRecorder()
			router.ServeHTTP(w2, req2)

			Expect(w2.Code).To(Equal(http.StatusOK))
			var results2 []db.AnalysisResult
			Expect(json.Unmarshal(w2.Body.Bytes(), &results2)).To(Succeed())
			// Should return paginated-session-1 (and potentially older seeded results from other tests)
			Expect(len(results2)).To(BeNumerically(">=", 1))
			Expect(results2[0].SessionID).To(Equal("paginated-session-1"))
		})

		It("returns bad request when profile_id is missing", func() {
			router := newTestRouter(controllers.Config{AnalysisResults: controllers.NewGormAnalysisResultRepository(dbConn)})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/history", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(Equal("profile_id is required"))
		})

		It("returns only sessions within the from/to date range", func() {
			now := time.Now()
			yesterday := now.AddDate(0, 0, -1)
			lastWeek := now.AddDate(0, 0, -7)

			Expect(dbConn.Create(&db.AnalysisResult{
				SessionID: "session-today", Status: "COMPLETED", Output: "today workout", ProfileID: profileID, CreatedAt: now,
			}).Error).NotTo(HaveOccurred())
			Expect(dbConn.Create(&db.AnalysisResult{
				SessionID: "session-yesterday", Status: "COMPLETED", Output: "yesterday workout", ProfileID: profileID, CreatedAt: yesterday,
			}).Error).NotTo(HaveOccurred())
			Expect(dbConn.Create(&db.AnalysisResult{
				SessionID: "session-lastweek", Status: "COMPLETED", Output: "last week workout", ProfileID: profileID, CreatedAt: lastWeek,
			}).Error).NotTo(HaveOccurred())

			from := now.AddDate(0, 0, -2).Format("2006-01-02")
			to := now.Format("2006-01-02")
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=1&from=%s&to=%s", from, to), nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var results []json.RawMessage
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(2)) // today + yesterday, not last week
		})

		It("returns bad request for invalid from date", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/history?profile_id=1&from=bad-date&to=2026-06-01", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(ContainSubstring("invalid from date"))
		})

		It("returns bad request for invalid to date", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/history?profile_id=1&from=2026-06-01&to=bad-date", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(decodeMapBody(w)["error"]).To(ContainSubstring("invalid to date"))
		})

		It("clamps and normalizes the limit query parameter", func() {
			// Seed 5 results
			for i := 1; i <= 5; i++ {
				Expect(dbConn.Create(&db.AnalysisResult{
					SessionID: fmt.Sprintf("limit-session-%d", i),
					Status:    "COMPLETED",
					ProfileID: profileID,
				}).Error).NotTo(HaveOccurred())
			}

			// 1. Malformed limit: fallback to default (20) which returns all 5
			req := httptest.NewRequest(http.MethodGet, "/api/v1/history?profile_id=1&limit=abc", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))
			var results []db.AnalysisResult
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(5))

			// 2. Negative/Zero limit: fallback to default (20) which returns all 5
			req = httptest.NewRequest(http.MethodGet, "/api/v1/history?profile_id=1&limit=0", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w = httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(5))

			// 3. Respected limit
			req = httptest.NewRequest(http.MethodGet, "/api/v1/history?profile_id=1&limit=2", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w = httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(2))

			// 4. Oversized limit: clamp to 100
			// Let's seed 102 results in total (we already have 5, so seed 97 more)
			for i := 6; i <= 102; i++ {
				Expect(dbConn.Create(&db.AnalysisResult{
					SessionID: fmt.Sprintf("limit-session-%d", i),
					Status:    "COMPLETED",
					ProfileID: profileID,
				}).Error).NotTo(HaveOccurred())
			}
			req = httptest.NewRequest(http.MethodGet, "/api/v1/history?profile_id=1&limit=150", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w = httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(100))
		})
	})

	Describe("GET /api/v1/movements and /api/v1/injuries", func() {
		var router *gin.Engine

		BeforeEach(func() {
			router = newTestRouter(controllers.Config{})
		})

		It("keeps the legacy top-level movement string-array shape", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/movements", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var got []string
			Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
			Expect(len(got)).To(BeNumerically(">", 0))
			Expect(got[0]).NotTo(BeEmpty())
		})

		It("keeps the legacy top-level movement-group array shape", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/movement-groups", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var got []controllers.MovementGroup
			Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
			Expect(len(got)).To(BeNumerically(">", 0))
			Expect(got[0].Category).To(Equal("Barbell"))
			Expect(got[0].Movements).NotTo(BeEmpty())
		})

		It("returns injuries", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/injuries", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var got []string
			Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
			Expect(len(got)).To(BeNumerically(">", 0))
			Expect(got[0]).NotTo(BeEmpty())
		})
	})

	Describe("GET /api/v1/subtitles/:session_id", func() {
		It("returns SRT with correct format for completed chunks sorted by start_secs", func() {
			start1, end1 := 10.0, 20.0
			start2, end2 := 0.0, 10.0
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: "session-1", Status: "COMPLETED", Output: "Second chunk",
				ProfileID: profileID,
				StartSecs: &start1, EndSecs: &end1,
			}).Error).NotTo(HaveOccurred())
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				ProfileID: profileID,
				SessionID: "session-1", Status: "COMPLETED", Output: "First chunk",
				StartSecs: &start2, EndSecs: &end2,
			}).Error).NotTo(HaveOccurred())

			router := newTestRouter(controllers.Config{AnalysisResults: controllers.NewGormAnalysisResultRepository(dbConn)})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/subtitles/session-1", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(w.Header().Get("Content-Type")).To(Equal("text/plain; charset=utf-8"))
			Expect(w.Header().Get("Content-Disposition")).To(ContainSubstring("session-1.srt"))

			body := w.Body.String()
			Expect(body).To(ContainSubstring("1\n00:00:00,000 --> 00:00:10,000\nFirst chunk\n"))
			Expect(body).To(ContainSubstring("2\n00:00:10,000 --> 00:00:20,000\nSecond chunk\n"))
		})

		It("returns empty body when no completed chunks exist", func() {
			start, end := 0.0, 10.0
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: "session-1", Status: "FAILED", Output: "error",
				ProfileID: profileID,
				StartSecs: &start, EndSecs: &end,
			}).Error).NotTo(HaveOccurred())

			router := newTestRouter(controllers.Config{AnalysisResults: controllers.NewGormAnalysisResultRepository(dbConn)})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/subtitles/session-1", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(w.Body.String()).To(BeEmpty())
		})

		It("skips chunks without start/end timestamps", func() {
			start, end := 0.0, 10.0
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: "session-1", Status: "COMPLETED", Output: "has timestamps",
				ProfileID: profileID,
				StartSecs: &start, EndSecs: &end,
			}).Error).NotTo(HaveOccurred())
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: "session-1", Status: "COMPLETED", Output: "no timestamps",
				ProfileID: profileID,
			}).Error).NotTo(HaveOccurred())

			router := newTestRouter(controllers.Config{AnalysisResults: controllers.NewGormAnalysisResultRepository(dbConn)})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/subtitles/session-1", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			body := w.Body.String()
			Expect(body).To(ContainSubstring("has timestamps"))
			Expect(body).NotTo(ContainSubstring("no timestamps"))
		})
	})

	Describe("GET /api/v1/dev/sessions", func() {
		It("lists existing sessions discovered from stored assets", func() {
			transport := testhelpers.NewMockTransport()
			storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())

			transport.New("https://storage.googleapis.com").
				Get(gcsListObjectsURL("test-bucket", "videos/")).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"kind": "storage#objects",
					"items": []map[string]any{
						{"name": "videos/WOD-2026-03-30-10-34_chunk_0001_front.mp4", "timeCreated": "2026-03-30T10:35:00Z"},
						{"name": "videos/WOD-2026-03-30-10-34_merged_20260330110000.mp4", "timeCreated": "2026-03-30T11:00:00Z"},
						{"name": "videos/session-1_video.mp4", "timeCreated": "2026-03-30T09:00:00Z"},
						{"name": "videos/session-1_hardsubbed_20260330100500.mp4", "timeCreated": "2026-03-30T10:05:00Z"},
					},
				})

			transport.New("https://storage.googleapis.com").
				Get(gcsListObjectsURL("test-bucket", "highlights/")).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"kind": "storage#objects",
					"items": []map[string]any{
						{"name": "highlights/WOD-2026-03-30-10-34_hl_best_20260330112000.mp4", "timeCreated": "2026-03-30T11:20:00Z"},
						{"name": "highlights/WOD-2026-03-30-10-34_music_20260330112000.mp3", "timeCreated": "2026-03-30T11:20:01Z"},
					},
				})

			router := newTestRouter(controllers.Config{StorageClient: storageClient, BucketName: "test-bucket"})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/sessions", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var response controllers.SessionCatalogResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
			Expect(response.Sessions).To(HaveLen(2))

			Expect(response.Sessions[0].SessionID).To(Equal("WOD-2026-03-30-10-34"))
			Expect(response.Sessions[0].ChunkCount).To(Equal(1))
			Expect(response.Sessions[0].HasMerged).To(BeTrue())
			Expect(response.Sessions[0].HasHardsubbed).To(BeFalse())
			Expect(response.Sessions[0].HighlightCount).To(Equal(1))
			Expect(response.Sessions[0].LatestCreatedAt).To(Equal("2026-03-30T11:20:00Z"))

			Expect(response.Sessions[1].SessionID).To(Equal("session-1"))
			Expect(response.Sessions[1].ChunkCount).To(Equal(1))
			Expect(response.Sessions[1].HasMerged).To(BeFalse())
			Expect(response.Sessions[1].HasHardsubbed).To(BeTrue())
			Expect(response.Sessions[1].HighlightCount).To(Equal(0))
			Expect(response.Sessions[1].LatestCreatedAt).To(Equal("2026-03-30T10:05:00Z"))
		})

		It("returns internal error when storage listing fails", func() {
			transport := testhelpers.NewMockTransport()
			storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())

			transport.New("https://storage.googleapis.com").
				Get(gcsListObjectsURL("test-bucket", "videos/")).
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{"code": 500, "message": "Internal Server Error"},
				})

			router := newTestRouter(controllers.Config{
				StorageClient: storageClient,
				BucketName:    "test-bucket",
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/sessions", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to list sessions"))
		})
	})

	Describe("GET /api/v1/chunk-analysis/:session_id", func() {
		const sessionA = "session-sanitize-a"

		BeforeEach(func() {
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID: sessionA, ProfileID: profileID, Status: "COMPLETED", Output: "output-a",
			})

			start, end := 0.0, 10.0
			testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
				SessionID:       sessionA,
				ProfileID:       profileID,
				FilePath:        "gs://bucket/videos/1/session-sanitize-a/chunk_0001.mp4",
				ExerciseType:    "Pull-up",
				Status:          "COMPLETED",
				Output:          "chunk-a",
				ObservedSignals: `{"movement":"Pull-up"}`,
				StartSecs:       &start,
				EndSecs:         &end,
			})
		})

		It("GET /chunk-analysis/:session_id returns only the requested session", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/chunk-analysis/"+sessionA, nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var results []db.ChunkAnalysisResult
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
			Expect(results[0].SessionID).To(Equal(sessionA))
		})

		It("preserves the legacy chunk array, free-form movement, and created-at ordering", func() {
			olderCreatedAt := time.Now().UTC().Add(-time.Hour)
			Expect(dbConn.Model(&db.ChunkAnalysisResult{}).
				Where("session_id = ?", sessionA).
				UpdateColumn("created_at", olderCreatedAt).Error).NotTo(HaveOccurred())

			start, end := 10.0, 20.0
			testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
				SessionID:         sessionA,
				ProfileID:         profileID,
				FilePath:          "gs://bucket/videos/1/session-sanitize-a/chunk_0002.mp4",
				ExerciseType:      "Atlas Stone Complex",
				Status:            "COMPLETED",
				Output:            "Keep the legacy coaching output as text.",
				ObservedSignals:   `{"movement":"Atlas Stone Complex"}`,
				HeartRateBPM:      142,
				StartSecs:         &start,
				EndSecs:           &end,
				WorkoutConfidence: 0.91,
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/chunk-analysis/"+sessionA, nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var results []map[string]any
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(2))

			newer := results[0]
			older := results[1]
			Expect(newer["exercise_type"]).To(Equal("Atlas Stone Complex"))
			Expect(newer["status"]).To(Equal("COMPLETED"))
			Expect(newer["output"]).To(Equal("Keep the legacy coaching output as text."))
			Expect(newer["id"]).To(BeAssignableToTypeOf(float64(0)))
			Expect(newer["session_id"]).To(Equal(sessionA))
			Expect(newer["profile_id"]).To(BeAssignableToTypeOf(float64(0)))
			Expect(newer["file_path"]).To(BeAssignableToTypeOf(""))
			Expect(newer["heart_rate_bpm"]).To(BeAssignableToTypeOf(float64(0)))
			Expect(newer["start_secs"]).To(BeAssignableToTypeOf(float64(0)))
			Expect(newer["end_secs"]).To(BeAssignableToTypeOf(float64(0)))
			Expect(newer["workout_confidence"]).To(BeAssignableToTypeOf(float64(0)))

			newerTime, err := time.Parse(time.RFC3339Nano, newer["created_at"].(string))
			Expect(err).NotTo(HaveOccurred())
			olderTime, err := time.Parse(time.RFC3339Nano, older["created_at"].(string))
			Expect(err).NotTo(HaveOccurred())
			Expect(newerTime).To(BeTemporally(">", olderTime))
		})
	})

	Describe("GET /api/v1/highlight/:session_id", func() {
		const sessionA = "session-sanitize-a"

		BeforeEach(func() {
			Expect(dbConn.Create(&db.AnalysisResult{
				SessionID: sessionA, ProfileID: profileID, Status: "COMPLETED", Output: "output-a",
			}).Error).NotTo(HaveOccurred())

			Expect(dbConn.Create(&db.HighlightResult{
				SessionID: sessionA, ProfileID: profileID, Status: "COMPLETED", Title: "highlight-a",
			}).Error).NotTo(HaveOccurred())
		})

		It("GET /highlight/:session_id returns only the requested session", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/highlight/"+sessionA, nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var results []db.HighlightResult
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
			Expect(results[0].SessionID).To(Equal(sessionA))
		})
	})

	Describe("POST /api/v1/sessions", func() {
		var profile db.Profile
		var user db.User

		BeforeEach(func() {
			router = newTestRouterWithAuthService(controllers.Config{})
			profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
			err := dbConn.Where("id = ?", profile.UserID).First(&user).Error
			Expect(err).To(BeNil())
		})

		It("creates a session", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/sessions", fmt.Sprintf(`{"profile_id": %d, "movements": ["Pull-up", "Sandbag Over Shoulder"]}`, profile.ID), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var newSession db.Session
			Expect(dbConn.Where("profile_id = ?", profile.ID).First(&newSession).Error).To(BeNil())
			Expect(newSession.SessionID).NotTo(BeEmpty())
			Expect(newSession.Status).To(Equal(db.SessionStatus("started")))
			Expect(string(newSession.MovementHints)).To(MatchJSON(`["Pull-up", "Sandbag Over Shoulder"]`))

			formattedTime := "WOD-" + time.Now().Format("200601021504")
			Expect(newSession.SessionID).To(HavePrefix(formattedTime))

			var response controllers.CreateSessionResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
			Expect(response.SessionID).To(Equal(newSession.SessionID))
		})

		It("returns an error if profile id does not exists", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/sessions", fmt.Sprintf(`{"profile_id": %d}`, 999999), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(w.Body.String()).To(ContainSubstring("profile not found"))

			var newSession db.Session
			Expect(dbConn.First(&newSession).Error).To(Equal(gorm.ErrRecordNotFound))
		})

		It("returns an error if profile does not belong to current user", func() {
			profile2 := testhelpers.CreateProfile(dbConn, &db.Profile{})
			user2 := db.User{}
			err := dbConn.Where("id = ?", profile2.UserID).First(&user2).Error
			Expect(err).To(BeNil())

			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/sessions", fmt.Sprintf(`{"profile_id": %d}`, profile.ID), &user2)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusForbidden))
			Expect(w.Body.String()).To(ContainSubstring("not authorized for this profile"))

			var newSession db.Session
			Expect(dbConn.First(&newSession).Error).To(Equal(gorm.ErrRecordNotFound))
		})

		It("stores workout_type when provided", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/sessions", fmt.Sprintf(`{"profile_id": %d, "workout_type": "warmup"}`, profile.ID), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var response controllers.CreateSessionResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
			Expect(response.WorkoutType).To(Equal("warmup"))

			var newSession db.Session
			Expect(dbConn.Where("profile_id = ?", profile.ID).First(&newSession).Error).To(BeNil())
			Expect(newSession.WorkoutType).To(Equal("warmup"))
		})

		It("defaults workout_type to wod when not provided", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/sessions", fmt.Sprintf(`{"profile_id": %d}`, profile.ID), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var response controllers.CreateSessionResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
			Expect(response.WorkoutType).To(Equal("wod"))

			var newSession db.Session
			Expect(dbConn.Where("profile_id = ?", profile.ID).First(&newSession).Error).To(BeNil())
			Expect(newSession.WorkoutType).To(Equal("wod"))
		})

		It("normalizes unknown workout_type to wod", func() {
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/sessions", fmt.Sprintf(`{"profile_id": %d, "workout_type": "rehab"}`, profile.ID), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var response controllers.CreateSessionResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
			Expect(response.WorkoutType).To(Equal("wod"))
		})
	})

})

func repeatedJSONString(value string, count int) string {
	parts := make([]string, count)
	encoded, err := json.Marshal(value)
	Expect(err).NotTo(HaveOccurred())
	for i := range parts {
		parts[i] = string(encoded)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func multipartRequestBody(sessionID string, filename string, content string) (*bytes.Buffer, string) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	Expect(writer.WriteField("session_id", sessionID)).To(Succeed())

	part, err := writer.CreateFormFile("file", filename)
	Expect(err).NotTo(HaveOccurred())
	_, err = io.Copy(part, strings.NewReader(content))
	Expect(err).NotTo(HaveOccurred())

	Expect(writer.Close()).To(Succeed())
	return body, writer.FormDataContentType()
}
