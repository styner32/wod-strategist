package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

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

func newTestAuthService() *auth.Service {
	return auth.NewService(nil, []byte("test-jwt-signing-secret-for-router"))
}

var _ = Describe("SetupRouter", func() {
	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		logger.Log = zap.NewNop()
	})

	It("returns an error when controller is nil", func() {
		_, err := server.SetupRouter("test", nil, nil, nil, newTestAuthService())
		Expect(err).To(MatchError(server.ErrHandlersRequired))
	})

	It("returns an error when auth service is nil", func() {
		_, err := server.SetupRouter("test", nil, newTestController(), nil, nil)
		Expect(err).To(MatchError(server.ErrAuthServiceRequired))
	})

	It("allows /health without a JWT", func() {
		router, err := server.SetupRouter("test", nil, newTestController(), nil, newTestAuthService())
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
	})

	It("rejects protected routes without a JWT", func() {
		router, err := server.SetupRouter("test", nil, newTestController(), nil, newTestAuthService())
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/movements", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusUnauthorized))
	})

	It("rejects protected routes with an invalid JWT", func() {
		router, err := server.SetupRouter("test", nil, newTestController(), nil, newTestAuthService())
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/movements", nil)
		req.Header.Set("Authorization", "Bearer not-a-valid-token")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusUnauthorized))
	})

	It("registers all expected routes", func() {
		authSvc := newTestAuthService()
		router, err := server.SetupRouter("test", nil, newTestController(), newTestAuthController(), authSvc)
		Expect(err).NotTo(HaveOccurred())

		actualRoutes := make(map[string]struct{}, len(router.Routes()))
		for _, route := range router.Routes() {
			actualRoutes[route.Method+" "+route.Path] = struct{}{}
		}

		expectedRoutes := []string{
			"GET /health",
			// Public credential routes
			"POST /api/v1/auth/web/signup",
			"POST /api/v1/auth/web/login",
			"POST /api/v1/auth/signup",
			"POST /api/v1/auth/login",
			// Protected auth routes
			"POST /api/v1/auth/web/logout",
			"GET /api/v1/auth/me",
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

	It("handles development CORS preflight without advertising X-API-Key", func() {
		router, err := server.SetupRouter("development", []string{"http://localhost:3000"}, newTestController(), nil, newTestAuthService())
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/history", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		req.Header.Set("Access-Control-Request-Headers", "Authorization")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNoContent))
		Expect(w.Header().Get("Access-Control-Allow-Origin")).To(Equal("http://localhost:3000"))
		Expect(w.Header().Get("Access-Control-Allow-Methods")).To(ContainSubstring(http.MethodGet))
		Expect(w.Header().Get("Access-Control-Allow-Headers")).To(ContainSubstring("Authorization"))
		Expect(w.Header().Get("Access-Control-Allow-Headers")).NotTo(ContainSubstring("X-API-Key"))
	})

	It("includes Access-Control-Allow-Credentials in CORS responses", func() {
		router, err := server.SetupRouter("development", []string{"http://localhost:5173"}, newTestController(), nil, newTestAuthService())
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
		router, err := server.SetupRouter("development", []string{"http://localhost:5173"}, newTestController(), nil, newTestAuthService())
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
		router, err := server.SetupRouter("development", []string{"http://localhost:5173"}, newTestController(), nil, newTestAuthService())
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/sessions/session-1/feedback/1", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", http.MethodPatch)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNoContent))
		Expect(w.Header().Get("Access-Control-Allow-Methods")).To(ContainSubstring("PATCH"))
	})

	It("allows mobile and web login without a JWT", func() {
		router, err := server.SetupRouter("test", nil, newTestController(), newTestAuthController(), newTestAuthService())
		Expect(err).NotTo(HaveOccurred())

		// Web login — expect 400 (no JSON body), not 401
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/web/login", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusBadRequest), "web login should not require JWT")

		// Web signup — disabled, returns 403
		req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/web/signup", nil)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusForbidden), "web signup should not require JWT")

		// Mobile login — expect 400 (no JSON body), not 401
		req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusBadRequest), "mobile login should not require JWT")

		// Mobile signup — disabled, returns 403
		req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusForbidden), "mobile signup should not require JWT")
	})

	It("allows localhost CORS on any port", func() {
		router, err := server.SetupRouter("development", nil, newTestController(), nil, newTestAuthService())
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/history", nil)
		req.Header.Set("Origin", "http://localhost:9999")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNoContent))
		Expect(w.Header().Get("Access-Control-Allow-Origin")).To(Equal("http://localhost:9999"))
	})
})
