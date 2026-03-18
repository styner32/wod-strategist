package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
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
		// logger.Init()
		logger.Log = zap.NewNop() // Mock logger to avoid panics

		router = server.SetupRouter(&server.ServerConfig{
				QueueClient:   &fakeQueueClient{},
			DB:            nil, // DB not needed for these handler tests
			StorageClient: &fakeStorageClient{bucketName: "test-bucket"},
			BucketName:    "test-bucket",
			APIKey:        "test-api-key",
		})
	})

	Context("POST /api/v1/upload-complete", func() {
		It("should return error if gcs_uri does not start with gs://", func() {
			body := bytes.NewBufferString(`{"session_id": "123", "gcs_uri": "/etc/passwd", "movements": []}`)

			req, _ := http.NewRequest("POST", "/api/v1/upload-complete", body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(w.Body.String()).To(ContainSubstring("invalid GCS URI"))
		})

		It("should return valid result if gcs_uri starts with gs://", func() {
			body := bytes.NewBufferString(`{"session_id": "123", "gcs_uri": "gs://bucket/file.mp4", "movements": []}`)

			req, _ := http.NewRequest("POST", "/api/v1/upload-complete", body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Expect 500 because QueueClient.Enqueue will fail since Redis isn't running
			Expect(w.Code).To(Equal(http.StatusAccepted))
			Expect(w.Body.String()).To(ContainSubstring("Analysis started"))
		})

		/*
					func TestValidateMovements(t *testing.T) {
				tests := []struct {
					name      string
					requested []string
					want      bool
				}{
					{
						name:      "Valid movement",
						requested: []string{"Burpee"},
						want:      true,
					},
					{
						name:      "Invalid movement",
						requested: []string{"InvalidMove"},
						want:      false,
					},
					{
						name:      "Mixed valid and invalid (should be false)",
						requested: []string{"InvalidMove", "Burpee"},
						want:      false,
					},
					{
						name:      "Empty request",
						requested: []string{},
						want:      true,
					},
					{
						name:      "Multiple valid",
						requested: []string{"Burpee", "Row"},
						want:      true,
					},
				}

				for _, tt := range tests {
					t.Run(tt.name, func(t *testing.T) {
						if got := validateMovements(tt.requested); got != tt.want {
							t.Errorf("validateMovements() = %v, want %v", got, tt.want)
						}
					})
				}
			}
		*/

		DescribeTable("should return error if movements are invalid",
			func(movements []string) {
				body := &bytes.Buffer{}
				json.NewEncoder(body).Encode(map[string]interface{}{
					"session_id": "test-session",
					"gcs_uri":    "gs://test-bucket/videos/test-session_video.mp4",
					"movements":  movements,
				})

				req, _ := http.NewRequest("POST", "/api/v1/upload-complete", body)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Api-Key", "test-api-key")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)

				Expect(w.Code).To(Equal(http.StatusBadRequest))
				Expect(w.Body.String()).To(ContainSubstring("invalid movements"))
			},
			Entry("invalid movements", []string{"InvalidMove", "Burpee"}),
		)
	})

	Context("POST /api/v1/upload", func() {
		It("should return error if session_id is missing", func() {
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			writer.Close()

			req, _ := http.NewRequest("POST", "/api/v1/upload", body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("X-API-Key", "test-api-key")
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
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(w.Body.String()).To(ContainSubstring("file is required"))
		})

		It("should accept valid upload", func() {
			tmpfile, err := os.CreateTemp("", "testvideo.mp4")
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(tmpfile.Name())
			tmpfile.Write([]byte("dummy content"))
			tmpfile.Close()

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
			Expect(w.Body.String()).To(ContainSubstring("gs://test-bucket/videos/test-session_video.mp4"))
		})
	})
})
