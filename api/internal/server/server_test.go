package server_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"go.uber.org/zap"
)

type fakeQueueClient struct{}

func (f *fakeQueueClient) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{ID: "task-123"}, nil
}

type fakeStorageClient struct {
	bucketName string
}

func (f *fakeStorageClient) GenerateSignedURL(objectName string, method string, expires time.Duration) (string, error) {
	return fmt.Sprintf("https://example.test/%s/%s", f.bucketName, objectName), nil
}

func (f *fakeStorageClient) UploadFile(ctx context.Context, file multipart.File, filename string) (string, error) {
	return fmt.Sprintf("gs://%s/%s", f.bucketName, filename), nil
}

func TestServer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Server Suite")
}

var _ = Describe("API Server", func() {
	var (
		router *gin.Engine
	)

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		logger.Log = zap.NewNop()
		router = server.SetupRouter(&server.ServerConfig{
			QueueClient:   &fakeQueueClient{},
			DB:            nil,
			StorageClient: &fakeStorageClient{bucketName: "test-bucket"},
			BucketName:    "test-bucket",
			APIKey:        "test-api-key",
		})
	})

	Context("POST /api/v1/upload", func() {
		It("should return error if session_id is missing", func() {
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			writer.Close()

			req, _ := http.NewRequest("POST", "/api/v1/upload", body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("X-Api-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(w.Body.String()).To(ContainSubstring("session_id is required"))
		})

		It("should return error if file is missing", func() {
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			writer.WriteField("session_id", "123")
			writer.Close()

			req, _ := http.NewRequest("POST", "/api/v1/upload", body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("X-Api-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(w.Body.String()).To(ContainSubstring("file is required"))
		})

		It("should accept valid upload", func() {
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			writer.WriteField("session_id", "test-session")

			part, err := writer.CreateFormFile("file", "video.mp4")
			Expect(err).NotTo(HaveOccurred())
			_, err = part.Write([]byte("dummy content"))
			Expect(err).NotTo(HaveOccurred())
			writer.Close()

			req, _ := http.NewRequest("POST", "/api/v1/upload", body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("X-Api-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusAccepted))
			Expect(w.Body.String()).To(ContainSubstring("File uploaded and analysis started"))
			Expect(w.Body.String()).To(ContainSubstring("\"task_id\":\"task-123\""))
			Expect(w.Body.String()).To(ContainSubstring("gs://test-bucket/videos/test-session_video.mp4"))
		})
	})
})
