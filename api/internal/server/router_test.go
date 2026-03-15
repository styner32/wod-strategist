package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"go.uber.org/zap"
)

type stubHandlers struct {
	healthCalled    bool
	protectedCalled bool
}

func (h *stubHandlers) Health(c *gin.Context) {
	h.healthCalled = true
	c.JSON(http.StatusOK, gin.H{"route": "health"})
}

func (h *stubHandlers) CreateUploadURL(c *gin.Context) {
	h.protectedCalled = true
	c.JSON(http.StatusOK, gin.H{"route": "upload-url"})
}

func (h *stubHandlers) CompleteUpload(c *gin.Context) {
	h.protectedCalled = true
	c.JSON(http.StatusOK, gin.H{"route": "upload-complete"})
}

func (h *stubHandlers) Upload(c *gin.Context) {
	h.protectedCalled = true
	c.JSON(http.StatusOK, gin.H{"route": "upload"})
}

func (h *stubHandlers) GetAnalysis(c *gin.Context) {
	h.protectedCalled = true
	c.JSON(http.StatusOK, gin.H{"route": "analysis"})
}

func (h *stubHandlers) GetHistory(c *gin.Context) {
	h.protectedCalled = true
	c.JSON(http.StatusOK, gin.H{"route": "history"})
}

func (h *stubHandlers) ListMovements(c *gin.Context) {
	h.protectedCalled = true
	c.JSON(http.StatusOK, gin.H{"route": "movements"})
}

func (h *stubHandlers) ListInjuries(c *gin.Context) {
	h.protectedCalled = true
	c.JSON(http.StatusOK, gin.H{"route": "injuries"})
}

var _ = Describe("SetupRouter", func() {
	var handlers *stubHandlers

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		logger.Log = zap.NewNop()
		handlers = &stubHandlers{}
	})

	It("allows /health without an API key", func() {
		router := server.SetupRouter("secret", handlers)

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(handlers.healthCalled).To(BeTrue())
		Expect(handlers.protectedCalled).To(BeFalse())
	})

	It("rejects protected routes when no API secret is configured", func() {
		original := os.Getenv("API_SECRET")
		Expect(os.Unsetenv("API_SECRET")).To(Succeed())
		DeferCleanup(func() {
			if original == "" {
				_ = os.Unsetenv("API_SECRET")
				return
			}
			_ = os.Setenv("API_SECRET", original)
		})

		router := server.SetupRouter("", handlers)

		body, err := json.Marshal(map[string]string{"session_id": "session-1", "filename": "video.mp4"})
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodPost, "/api/v1/upload-url", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusUnauthorized))
		Expect(handlers.protectedCalled).To(BeFalse())
	})

	It("rejects invalid API keys", func() {
		router := server.SetupRouter("secret", handlers)

		body, err := json.Marshal(map[string]string{"session_id": "session-1", "filename": "video.mp4"})
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodPost, "/api/v1/upload-url", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "wrong")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusUnauthorized))
		Expect(handlers.protectedCalled).To(BeFalse())
	})

	It("accepts the configured API key", func() {
		router := server.SetupRouter("secret", handlers)

		body, err := json.Marshal(map[string]string{"session_id": "session-1", "filename": "video.mp4"})
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodPost, "/api/v1/upload-url", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "secret")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(handlers.protectedCalled).To(BeTrue())
	})

	It("falls back to API_SECRET from the environment", func() {
		original := os.Getenv("API_SECRET")
		Expect(os.Setenv("API_SECRET", "env-secret")).To(Succeed())
		DeferCleanup(func() {
			if original == "" {
				_ = os.Unsetenv("API_SECRET")
				return
			}
			_ = os.Setenv("API_SECRET", original)
		})

		router := server.SetupRouter("", handlers)

		body, err := json.Marshal(map[string]string{"session_id": "session-1", "filename": "video.mp4"})
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodPost, "/api/v1/upload-url", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "env-secret")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(handlers.protectedCalled).To(BeTrue())
	})
})
