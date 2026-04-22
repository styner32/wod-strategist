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

func (h *recordingHandlers) ListMovementGroups(c *gin.Context) {
	h.record("movement-groups", c)
}

func (h *recordingHandlers) ListInjuries(c *gin.Context) {
	h.record("injuries", c)
}

func (h *recordingHandlers) ChunkComplete(c *gin.Context) {
	h.record("chunk-complete", c)
}

func (h *recordingHandlers) GetChunkAnalysis(c *gin.Context) {
	h.record("chunk-analysis", c)
}

func (h *recordingHandlers) CreateProfile(c *gin.Context) {
	h.record("create-profile", c)
}

func (h *recordingHandlers) GetProfile(c *gin.Context) {
	h.record("get-profile", c)
}

func (h *recordingHandlers) ListProfiles(c *gin.Context) {
	h.record("list-profiles", c)
}

func (h *recordingHandlers) UpdateProfile(c *gin.Context) {
	h.record("update-profile", c)
}

func (h *recordingHandlers) ArchiveProfile(c *gin.Context) {
	h.record("archive-profile", c)
}

func (h *recordingHandlers) UnarchiveProfile(c *gin.Context) {
	h.record("unarchive-profile", c)
}

func (h *recordingHandlers) MergeChunks(c *gin.Context) {
	h.record("merge-chunks", c)
}

func (h *recordingHandlers) GetSubtitles(c *gin.Context) {
	h.record("subtitles", c)
}

func (h *recordingHandlers) GenerateHighlight(c *gin.Context) {
	h.record("generate-highlight", c)
}

func (h *recordingHandlers) GetHighlight(c *gin.Context) {
	h.record("highlight", c)
}

func (h *recordingHandlers) GetHighlightDownloadURL(c *gin.Context) {
	h.record("highlight-download", c)
}

func (h *recordingHandlers) VerifyHighlights(c *gin.Context) {
	h.record("verify-highlights", c)
}

func (h *recordingHandlers) ListSessionCatalog(c *gin.Context) {
	h.record("dev-sessions", c)
}

func (h *recordingHandlers) GetSessionAssets(c *gin.Context) {
	h.record("dev-session-assets", c)
}

func (h *recordingHandlers) GetPlayURL(c *gin.Context) {
	h.record("dev-session-play-url", c)
}

func (h *recordingHandlers) GetVideoDownloadURL(c *gin.Context) {
	h.record("video-download", c)
}

func (h *recordingHandlers) RetryAnalysis(c *gin.Context) {
	h.record("retry-analysis", c)
}

func (h *recordingHandlers) GenerateHardSub(c *gin.Context) {
	h.record("generate-hardsub", c)
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
		_, err := server.SetupRouter("test", "secret", nil, nil)
		Expect(err).To(MatchError(server.ErrHandlersRequired))
	})

	It("allows /health without an API key", func() {
		router, err := server.SetupRouter("test", "secret", nil, handlers)
		Expect(err).NotTo(HaveOccurred())
		req := requestForRoute(server.PublicRouteSpecs()[0], "")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(handlers.calls).To(Equal([]string{"health"}))
	})

	It("rejects invalid API keys", func() {
		router, err := server.SetupRouter("test", "secret", nil, handlers)
		Expect(err).NotTo(HaveOccurred())

		req := requestForRoute(server.ProtectedRouteSpecs()[0], "wrong")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusUnauthorized))
		Expect(handlers.calls).To(BeEmpty())
	})

	It("registers every declared route", func() {
		router, err := server.SetupRouter("test", "secret", nil, handlers)
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
		router, err := server.SetupRouter("test", "secret", nil, handlers)
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

	It("handles development CORS preflight before API key auth", func() {
		router, err := server.SetupRouter("development", "secret", []string{"http://localhost:3000"}, handlers)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/history", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		req.Header.Set("Access-Control-Request-Headers", "X-API-Key")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNoContent))
		Expect(w.Header().Get("Access-Control-Allow-Origin")).To(Equal("http://localhost:3000"))
		Expect(w.Header().Get("Access-Control-Allow-Methods")).To(ContainSubstring(http.MethodGet))
		Expect(w.Header().Get("Access-Control-Allow-Headers")).To(ContainSubstring("X-API-Key"))
		Expect(handlers.calls).To(BeEmpty())
	})
})
