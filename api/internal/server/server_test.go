package server_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"go.uber.org/zap"
)

type recordingHandlers struct {
	calls []string
}

func (h *recordingHandlers) record(route string, c *gin.Context) {
	h.calls = append(h.calls, route)
	c.JSON(http.StatusOK, gin.H{"route": route})
}

func (h *recordingHandlers) Health(c *gin.Context) {
	h.record("health", c)
}

func (h *recordingHandlers) CreateUploadURL(c *gin.Context) {
	h.record("upload-url", c)
}

func (h *recordingHandlers) CompleteUpload(c *gin.Context) {
	h.record("upload-complete", c)
}

func (h *recordingHandlers) Upload(c *gin.Context) {
	h.record("upload", c)
}

func (h *recordingHandlers) GetAnalysis(c *gin.Context) {
	h.record("analysis", c)
}

func (h *recordingHandlers) GetHistory(c *gin.Context) {
	h.record("history", c)
}

func (h *recordingHandlers) ListMovements(c *gin.Context) {
	h.record("movements", c)
}

func (h *recordingHandlers) ListInjuries(c *gin.Context) {
	h.record("injuries", c)
}

func requestForRoute(spec server.RouteSpec, apiKey string) *http.Request {
	path := regexp.MustCompile(`:[^/]+`).ReplaceAllString(spec.Path, "value")
	req := httptest.NewRequest(spec.Method, path, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	return req
}

func allRouteSpecs() []server.RouteSpec {
	return append(server.PublicRouteSpecs(), server.ProtectedRouteSpecs()...)
}

var _ = Describe("SetupRouter", func() {
	var handlers *recordingHandlers

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		logger.Log = zap.NewNop()
		handlers = &recordingHandlers{}
	})

	It("returns an error when handlers are nil", func() {
		_, err := server.SetupRouter("test", "secret", nil)
		Expect(err).To(MatchError(server.ErrHandlersRequired))
	})

	It("allows /health without an API key", func() {
		router, err := server.SetupRouter("test", "secret", handlers)
		Expect(err).NotTo(HaveOccurred())
		req := requestForRoute(server.PublicRouteSpecs()[0], "")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(handlers.calls).To(Equal([]string{"health"}))
	})

	It("rejects invalid API keys", func() {
		router, err := server.SetupRouter("test", "secret", handlers)
		Expect(err).NotTo(HaveOccurred())

		req := requestForRoute(server.ProtectedRouteSpecs()[0], "wrong")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusUnauthorized))
		Expect(handlers.calls).To(BeEmpty())
	})

	It("registers every declared route", func() {
		router, err := server.SetupRouter("test", "secret", handlers)
		Expect(err).NotTo(HaveOccurred())

		actualRoutes := make(map[string]struct{}, len(router.Routes()))
		for _, route := range router.Routes() {
			actualRoutes[route.Method+" "+route.Path] = struct{}{}
		}

		for _, spec := range allRouteSpecs() {
			Expect(actualRoutes).To(HaveKey(spec.Method + " " + spec.Path))
		}
	})

	It("dispatches every protected route to the matching handler when authorized", func() {
		router, err := server.SetupRouter("test", "secret", handlers)
		Expect(err).NotTo(HaveOccurred())

		for _, spec := range server.ProtectedRouteSpecs() {
			handlers.calls = nil

			req := requestForRoute(spec, "secret")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK), spec.Path)
			Expect(handlers.calls).To(Equal([]string{spec.Name}), spec.Path)
		}
	})
})
