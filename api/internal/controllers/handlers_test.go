package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
)

type fakeQueueClient struct {
	task   *asynq.Task
	info   *asynq.TaskInfo
	err    error
	called bool
}

func (f *fakeQueueClient) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	f.called = true
	f.task = task
	if f.info == nil {
		f.info = &asynq.TaskInfo{ID: "task-123"}
	}
	return f.info, f.err
}

type fakeStorageClient struct {
	generateURL string
	generateErr error
	uploadURI   string
	uploadErr   error

	generatedObjectName string
	generatedMethod     string
	generatedExpires    time.Duration
	uploadedFilename    string
	uploadedContent     string
}

func (f *fakeStorageClient) GenerateSignedURL(objectName string, method string, expires time.Duration) (string, error) {
	f.generatedObjectName = objectName
	f.generatedMethod = method
	f.generatedExpires = expires
	if f.generateURL == "" {
		f.generateURL = "https://example.test/upload"
	}
	return f.generateURL, f.generateErr
}

func (f *fakeStorageClient) UploadFile(ctx context.Context, file multipart.File, filename string) (string, error) {
	f.uploadedFilename = filename
	body, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	f.uploadedContent = string(body)
	if f.uploadURI == "" {
		f.uploadURI = "gs://test-bucket/" + filename
	}
	return f.uploadURI, f.uploadErr
}

type fakeAnalysisResultRepository struct {
	results      []db.AnalysisResult
	chunkResults []db.ChunkAnalysisResult
	err          error
	sessionID    string
	historyLimit int
	findCalled   bool
	listCalled   bool
}

func (f *fakeAnalysisResultRepository) FindBySessionID(ctx context.Context, sessionID string) ([]db.AnalysisResult, error) {
	f.findCalled = true
	f.sessionID = sessionID
	return f.results, f.err
}

func (f *fakeAnalysisResultRepository) ListRecent(ctx context.Context, limit int, profileID uint) ([]db.AnalysisResult, error) {
	f.listCalled = true
	f.historyLimit = limit
	return f.results, f.err
}

func (f *fakeAnalysisResultRepository) FindChunksBySessionID(ctx context.Context, sessionID string) ([]db.ChunkAnalysisResult, error) {
	f.findCalled = true
	f.sessionID = sessionID
	return f.chunkResults, f.err
}

type taskFactoryCall struct {
	sessionID   string
	filePath    string
	workoutType string
	movements   []string
	injuries    []string
}

type fakeTaskFactory struct {
	call taskFactoryCall
	task *asynq.Task
	err  error
}

func (f *fakeTaskFactory) Build(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint) (*asynq.Task, error) {
	f.call = taskFactoryCall{
		sessionID:   sessionID,
		filePath:    filePath,
		workoutType: workoutType,
		movements:   append([]string(nil), movements...),
		injuries:    append([]string(nil), injuries...),
	}
	if f.task == nil {
		f.task = asynq.NewTask(worker.TypeVideoAnalysis, []byte(`{}`))
	}
	return f.task, f.err
}

type fakeProfileRepository struct {
	profile *db.Profile
	err     error
	called  bool
}

func (f *fakeProfileRepository) Create(ctx context.Context, profile *db.Profile) error {
	f.called = true
	if f.err != nil {
		return f.err
	}
	profile.ID = 1 // simulate auto-increment
	f.profile = profile
	return nil
}

func (f *fakeProfileRepository) FindByID(ctx context.Context, id uint) (*db.Profile, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	if f.profile != nil && f.profile.ID == id {
		return f.profile, nil
	}
	return nil, fmt.Errorf("not found")
}

func newTestRouter(config Config) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger.Log = zap.NewNop()
	controller := New(config)
	router, err := server.SetupRouter("test", "test-api-key", controller)
	Expect(err).NotTo(HaveOccurred())
	return router
}

func newAuthorizedJSONRequest(method string, path string, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-api-key")
	return req
}

func decodeMapBody(w *httptest.ResponseRecorder) map[string]any {
	var body map[string]any
	Expect(json.Unmarshal(w.Body.Bytes(), &body)).To(Succeed())
	return body
}

var _ = Describe("Controller handlers", func() {
	Describe("Health", func() {
		It("returns the configured commit and RFC3339 time", func() {
			router := newTestRouter(Config{GitCommit: "abc123"})

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			body := decodeMapBody(w)
			Expect(body["status"]).To(Equal("ok"))
			Expect(body["commit"]).To(Equal("abc123"))
			_, err := time.Parse(time.RFC3339, body["time"].(string))
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("POST /api/v1/upload-url", func() {
		var storage *fakeStorageClient
		var router *gin.Engine

		BeforeEach(func() {
			storage = &fakeStorageClient{}
			router = newTestRouter(Config{StorageClient: storage, BucketName: "test-bucket"})
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

		It("sanitizes path traversal and returns a signed url", func() {
			storage.generateURL = "https://example.test/signed"

			req := newAuthorizedJSONRequest(
				http.MethodPost,
				"/api/v1/upload-url",
				`{"session_id":"../../session-1","filename":"..\\\\folder\\\\video.mp4"}`,
			)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(storage.generatedObjectName).To(Equal("videos/session-1_video.mp4"))
			Expect(storage.generatedMethod).To(Equal(http.MethodPut))
			Expect(storage.generatedExpires).To(Equal(15 * time.Minute))

			body := decodeMapBody(w)
			Expect(body["upload_url"]).To(Equal("https://example.test/signed"))
			Expect(body["gcs_uri"]).To(Equal("gs://test-bucket/videos/session-1_video.mp4"))
		})

		It("returns internal error when storage fails", func() {
			storage.generateErr = errors.New("boom")

			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-url", `{"session_id":"session-1","filename":"video.mp4"}`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to generate upload URL"))
		})
	})

	Describe("POST /api/v1/upload-complete", func() {
		var queue *fakeQueueClient
		var taskFactory *fakeTaskFactory
		var router *gin.Engine

		BeforeEach(func() {
			queue = &fakeQueueClient{}
			taskFactory = &fakeTaskFactory{}
			router = newTestRouter(Config{
				QueueClient:          queue,
				StorageClient:        &fakeStorageClient{},
				BucketName:           "test-bucket",
				NewVideoAnalysisTask: taskFactory.Build,
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
			Entry("invalid gcs scheme", `{"session_id":"session-1","gcs_uri":"https://bucket/video.mp4","movements":[]}`, http.StatusBadRequest, "invalid GCS URI"),
			Entry("missing gcs bucket", `{"session_id":"session-1","gcs_uri":"gs:///video.mp4","movements":[]}`, http.StatusBadRequest, "invalid GCS URI"),
			Entry("invalid workout type", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[],"workout_type":"strength"}`, http.StatusBadRequest, "invalid workout type"),
			Entry("invalid movement", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":["Invalid"]}`, http.StatusBadRequest, "invalid movements"),
			Entry("too many movements", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":`+repeatedJSONString("Burpee", 100)+`}`, http.StatusBadRequest, "too many movements"),
			Entry("invalid injury", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[],"injuries":["Head"]}`, http.StatusBadRequest, "invalid injuries"),
			Entry("too many injuries", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[],"injuries":`+repeatedJSONString("Left Knee", 100)+`}`, http.StatusBadRequest, "too many injuries"),
		)

		It("returns internal error when task creation fails", func() {
			taskFactory.err = errors.New("boom")

			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-complete", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":["Burpee"]}`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to create task"))
		})

		It("returns internal error when enqueue fails", func() {
			queue.err = errors.New("boom")

			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-complete", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":["Burpee"],"injuries":["Left Knee"],"workout_type":"rehab"}`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(queue.called).To(BeTrue())
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to enqueue task"))
			Expect(taskFactory.call.workoutType).To(Equal(worker.WorkoutTypeRehab))
		})

		It("accepts empty movements and normalizes the default workout type", func() {
			queue.info = &asynq.TaskInfo{ID: "task-999"}

			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/upload-complete", `{"session_id":"session-1","gcs_uri":"gs://bucket/video.mp4","movements":[]}`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusAccepted))

			body := decodeMapBody(w)
			Expect(body["message"]).To(Equal("Analysis started"))
			Expect(body["task_id"]).To(Equal("task-999"))
			Expect(body["session_id"]).To(Equal("session-1"))
			Expect(taskFactory.call.workoutType).To(Equal(worker.WorkoutTypeWOD))
			Expect(taskFactory.call.movements).To(BeEmpty())
		})
	})

	Describe("POST /api/v1/upload", func() {
		var storage *fakeStorageClient
		var queue *fakeQueueClient
		var taskFactory *fakeTaskFactory
		var router *gin.Engine

		BeforeEach(func() {
			storage = &fakeStorageClient{}
			queue = &fakeQueueClient{}
			taskFactory = &fakeTaskFactory{}
			router = newTestRouter(Config{
				QueueClient:          queue,
				StorageClient:        storage,
				BucketName:           "test-bucket",
				NewVideoAnalysisTask: taskFactory.Build,
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

		It("returns internal error when upload fails", func() {
			storage.uploadErr = errors.New("boom")

			body, contentType := multipartRequestBody("session-1", "../../video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to upload file"))
		})

		It("returns internal error when task creation fails", func() {
			taskFactory.err = errors.New("boom")

			body, contentType := multipartRequestBody("session-1", "video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to create task"))
		})

		It("returns internal error when enqueue fails", func() {
			queue.err = errors.New("boom")

			body, contentType := multipartRequestBody("session-1", "video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to enqueue task"))
			Expect(taskFactory.call.workoutType).To(Equal(worker.WorkoutTypeWOD))
		})

		It("uploads the file and sanitizes the object name", func() {
			queue.info = &asynq.TaskInfo{ID: "task-321"}

			body, contentType := multipartRequestBody("../../session-1", "..\\\\folder\\\\video.mp4", "dummy content")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusAccepted))
			Expect(storage.uploadedFilename).To(Equal("videos/session-1_video.mp4"))
			Expect(storage.uploadedContent).To(Equal("dummy content"))
			Expect(taskFactory.call.sessionID).To(Equal("session-1"))
			Expect(taskFactory.call.filePath).To(Equal("gs://test-bucket/videos/session-1_video.mp4"))

			bodyMap := decodeMapBody(w)
			Expect(bodyMap["message"]).To(Equal("File uploaded and analysis started"))
			Expect(bodyMap["task_id"]).To(Equal("task-321"))
			Expect(bodyMap["file_url"]).To(Equal("gs://test-bucket/videos/session-1_video.mp4"))
		})
	})

	Describe("GET /api/v1/analysis/:session_id", func() {
		It("returns repository results for the requested session", func() {
			repo := &fakeAnalysisResultRepository{
				results: []db.AnalysisResult{{ID: 1, SessionID: "session-1", Status: "COMPLETED", Output: "ok"}},
			}
			router := newTestRouter(Config{AnalysisResults: repo})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/analysis/session-1", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(repo.findCalled).To(BeTrue())
			Expect(repo.sessionID).To(Equal("session-1"))

			var results []db.AnalysisResult
			Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
			Expect(results[0].SessionID).To(Equal("session-1"))
		})

		It("returns internal error when repository fails", func() {
			router := newTestRouter(Config{AnalysisResults: &fakeAnalysisResultRepository{err: errors.New("boom")}})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/analysis/session-1", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to fetch results"))
		})
	})

	Describe("GET /api/v1/history", func() {
		It("returns recent history and enforces the limit", func() {
			repo := &fakeAnalysisResultRepository{
				results: []db.AnalysisResult{{ID: 1, SessionID: "session-1", Status: "COMPLETED", Output: "ok"}},
			}
			router := newTestRouter(Config{AnalysisResults: repo})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/history", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(repo.listCalled).To(BeTrue())
			Expect(repo.historyLimit).To(Equal(20))
		})

		It("returns internal error when repository fails", func() {
			router := newTestRouter(Config{AnalysisResults: &fakeAnalysisResultRepository{err: errors.New("boom")}})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/history", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(decodeMapBody(w)["error"]).To(Equal("failed to fetch history"))
		})
	})

	Describe("GET /api/v1/movements and /api/v1/injuries", func() {
		var router *gin.Engine

		BeforeEach(func() {
			router = newTestRouter(Config{})
		})

		It("returns movements", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/movements", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var got []string
			Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
			Expect(got).To(HaveLen(len(movements)))
			Expect(got[0]).To(Equal(movements[0]))
		})

		It("returns injuries", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/injuries", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var got []string
			Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
			Expect(got).To(HaveLen(len(injuries)))
			Expect(got[0]).To(Equal(injuries[0]))
		})
	})
})

var _ = Describe("validation helpers", func() {
	DescribeTable("allowedMovements.containsAll",
		func(values []string, want bool) {
			Expect(allowedMovements.containsAll(values)).To(Equal(want))
		},
		Entry("empty", nil, true),
		Entry("valid single", []string{"Burpee"}, true),
		Entry("valid duplicates", []string{"Burpee", "Burpee"}, true),
		Entry("invalid single", []string{"Invalid"}, false),
		Entry("mixed", []string{"Burpee", "Invalid"}, false),
	)

	DescribeTable("sanitizeObjectPart",
		func(input string, fallback string, want string) {
			Expect(sanitizeObjectPart(input, fallback)).To(Equal(want))
		},
		Entry("unix path", "../../video.mp4", "fallback", "video.mp4"),
		Entry("windows path", `..\\folder\\video.mp4`, "fallback", "video.mp4"),
		Entry("whitespace", "   ", "fallback", "fallback"),
		Entry("dotdot", "..", "fallback", "fallback"),
	)

	It("builds a sanitized video object name", func() {
		Expect(buildVideoObjectName("../../session-1", `..\\videos\\demo.mp4`)).To(Equal("videos/session-1_demo.mp4"))
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
