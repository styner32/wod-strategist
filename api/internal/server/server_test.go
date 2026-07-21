package server_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/auth"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

// newTestController creates a Controller with the required storage dependency
// and otherwise minimal config for route-level tests.
func newTestController() *controllers.Controller {
	transport := testhelpers.NewMockTransport()
	storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
	Expect(err).NotTo(HaveOccurred())
	controller, err := controllers.New(controllers.Config{
		StorageClient: storageClient,
	})
	Expect(err).NotTo(HaveOccurred())
	return controller
}

// newTestAuthController creates an AuthController with nil deps.
func newTestAuthController() *controllers.AuthController {
	return controllers.NewAuthController(nil, nil, controllers.CookieConfig{})
}

var _ = Describe("SetupRouter", func() {
	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		logger.Log = zap.NewNop()
	})

	It("returns an error when controller is nil", func() {
		_, err := server.SetupRouter("test", "secret", nil, nil, nil, nil)
		Expect(err).To(MatchError(server.ErrHandlersRequired))
	})

	It("allows /health without an API key", func() {
		router, err := server.SetupRouter("test", "secret", nil, newTestController(), nil, nil)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
	})

	It("rejects protected routes without API key", func() {
		router, err := server.SetupRouter("test", "secret", nil, newTestController(), nil, nil)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/movements", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusUnauthorized))
	})

	It("accepts protected routes with valid API key (no auth middleware)", func() {
		// No authSvc → AuthMiddleware is skipped, so API key alone is enough
		router, err := server.SetupRouter("test", "secret", nil, newTestController(), nil, nil)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/movements", nil)
		req.Header.Set("X-API-Key", "secret")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
	})

	It("registers all expected routes", func() {
		authSvc := auth.NewService(nil, nil)
		router, err := server.SetupRouter("test", "secret", nil, newTestController(), newTestAuthController(), authSvc)
		Expect(err).NotTo(HaveOccurred())

		actualRoutes := make(map[string]struct{}, len(router.Routes()))
		for _, route := range router.Routes() {
			actualRoutes[route.Method+" "+route.Path] = struct{}{}
		}

		expectedRoutes := []string{
			"GET /health",
			// Web auth (no API key)
			"POST /api/v1/auth/web/signup",
			"POST /api/v1/auth/web/login",
			"POST /api/v1/auth/web/logout",
			"GET /api/v1/auth/me",
			// Mobile auth (API key required)
			"POST /api/v1/auth/signup",
			"POST /api/v1/auth/login",
			"POST /api/v1/auth/logout",
			"DELETE /api/v1/auth/account",
			// Data routes
			"POST /api/v1/upload-url",
			"POST /api/v1/upload-complete",
			"POST /api/v1/upload",
			"GET /api/v1/analysis/:session_id",
			"GET /api/v1/history",
			"POST /api/v1/history/:id/archive",
			"POST /api/v1/history/:id/unarchive",
			"GET /api/v1/movements",
			"GET /api/v1/movement-groups",
			"GET /api/v1/injuries",
			"POST /api/v1/chunk-complete",
			"GET /api/v1/chunk-analysis/:session_id",
			"POST /api/v1/profiles",
			"GET /api/v1/profiles/:id",
			"GET /api/v1/profiles",
			"PUT /api/v1/profiles/:id",
			"POST /api/v1/profiles/:id/archive",
			"POST /api/v1/profiles/:id/unarchive",
			"POST /api/v1/merge-chunks",
			"GET /api/v1/subtitles/:session_id",
			"POST /api/v1/generate-highlight",
			"GET /api/v1/highlight/:session_id",
			"GET /api/v1/highlight-download/:id",
			"POST /api/v1/verify-highlights",
			"GET /api/v1/video-download/:session_id",
			"POST /api/v1/retry-analysis",
			"POST /api/v1/generate-hardsub",
			"POST /api/v1/debug/telemetry",
			"POST /api/v1/sessions/:session_id/reanalyses",
			"GET /api/v1/sessions/:session_id/reanalyses",
			"GET /api/v1/sessions/:session_id/reanalyses/:run_id",
		}
		for _, route := range expectedRoutes {
			Expect(actualRoutes).To(HaveKey(route))
		}
	})

	It("handles development CORS preflight before API key auth", func() {
		router, err := server.SetupRouter("development", "secret", []string{"http://localhost:3000"}, newTestController(), nil, nil)
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
	})

	It("includes Access-Control-Allow-Credentials in CORS responses", func() {
		router, err := server.SetupRouter("development", "secret", []string{"http://localhost:5173"}, newTestController(), nil, nil)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/history", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNoContent))
		Expect(w.Header().Get("Access-Control-Allow-Credentials")).To(Equal("true"))
	})

	It("includes DELETE in allowed CORS methods", func() {
		router, err := server.SetupRouter("development", "secret", []string{"http://localhost:5173"}, newTestController(), nil, nil)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/account", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", http.MethodDelete)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNoContent))
		Expect(w.Header().Get("Access-Control-Allow-Methods")).To(ContainSubstring("DELETE"))
	})

	It("includes PATCH in allowed CORS methods", func() {
		router, err := server.SetupRouter("development", "secret", []string{"http://localhost:5173"}, newTestController(), nil, nil)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/sessions/session-1/feedback/1", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", http.MethodPatch)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNoContent))
		Expect(w.Header().Get("Access-Control-Allow-Methods")).To(ContainSubstring("PATCH"))
	})

	It("allows web auth login/signup WITHOUT API key", func() {
		router, err := server.SetupRouter("test", "secret", nil, newTestController(), newTestAuthController(), nil)
		Expect(err).NotTo(HaveOccurred())

		// Web login — no X-API-Key header, expect 400 (no JSON body) not 401
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/web/login", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusBadRequest), "web login should not require API key")

		// Web signup — disabled, returns 403 (not 401 which would mean API key rejection)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/web/signup", nil)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusForbidden), "web signup should not require API key")
	})

	It("requires API key for mobile auth login/signup", func() {
		router, err := server.SetupRouter("test", "secret", nil, newTestController(), newTestAuthController(), nil)
		Expect(err).NotTo(HaveOccurred())

		// Mobile login without API key → 401
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusUnauthorized))

		// Mobile login with API key → 400 (no JSON body, but past middleware)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.Header.Set("X-API-Key", "secret")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusBadRequest))
	})

	It("allows localhost CORS on any port", func() {
		router, err := server.SetupRouter("development", "secret", nil, newTestController(), nil, nil)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/history", nil)
		req.Header.Set("Origin", "http://localhost:9999")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNoContent))
		Expect(w.Header().Get("Access-Control-Allow-Origin")).To(Equal("http://localhost:9999"))
	})

	It("skips API key check when jwt cookie is present", func() {
		// No authSvc → AuthMiddleware is not applied, so the jwt cookie
		// only needs to bypass APIKeyMiddleware (not actually validate).
		router, err := server.SetupRouter("test", "secret", nil, newTestController(), nil, nil)
		Expect(err).NotTo(HaveOccurred())

		// Without cookie or API key → 401
		req := httptest.NewRequest(http.MethodGet, "/api/v1/movements", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusUnauthorized))

		// With jwt cookie (no API key) → passes through to handler
		req = httptest.NewRequest(http.MethodGet, "/api/v1/movements", nil)
		req.AddCookie(&http.Cookie{Name: "jwt", Value: "some-token"})
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
	})
})
