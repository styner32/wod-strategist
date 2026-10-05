package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

// Each endpoint exercises the actual router, authentication and owned session.
func enrichmentRouteSpecs(method, suffix string) {
	var router *gin.Engine
	var owner, other db.User
	var session db.Session
	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)
		p := testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&owner, p.UserID).Error).To(Succeed())
		q := testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&other, q.UserID).Error).To(Succeed())
		session = testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20260928-01JAPIENRICHMENT000000000", ProfileID: p.ID, WorkoutType: "wod"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: session.SessionID, ProfileID: p.ID, Status: "COMPLETED", Output: "stored analysis", HighlightSegments: `[{"start":"00:01","end":"00:06","type":"best_form"}]`})
		transport := testhelpers.NewMockTransport()
		storage, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		router = newTestRouterWithAuthService(controllers.Config{QueueClient: testhelpers.NewQueueClient(), StorageClient: storage, EnableAgenticHighlights: true})
	})
	path := func() string { return fmt.Sprintf("/api/v1/sessions/%s/%s", session.SessionID, suffix) }
	It("requires authentication and ownership", func() {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(method, path(), nil))
		Expect(r.Code).To(Equal(http.StatusUnauthorized))
		r = httptest.NewRecorder()
		router.ServeHTTP(r, newAuthorizedJSONRequest(method, path(), "{}", &other))
		Expect(r.Code).To(Equal(http.StatusForbidden))
	})
	It("is read-only on GET and deduplicates active POST requests", func() {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, newAuthorizedJSONRequest(method, path(), "{}", &owner))
		if method == http.MethodGet {
			Expect(r.Code).To(Equal(http.StatusOK))
			Expect(r.Body.String()).NotTo(ContainSubstring("file_uri"))
			var row db.AnalysisResult
			Expect(dbConn.Where("session_id = ?", session.SessionID).First(&row).Error).To(Succeed())
			Expect(string(row.AgenticHighlightAnalysis)).To(Equal("{}"))
			Expect(string(row.AnalysisSummary)).To(Equal("{}"))
		} else {
			Expect(r.Code).To(Equal(http.StatusAccepted))
			var first map[string]string
			Expect(json.Unmarshal(r.Body.Bytes(), &first)).To(Succeed())
			again := httptest.NewRecorder()
			router.ServeHTTP(again, newAuthorizedJSONRequest(method, path(), "{}", &owner))
			Expect(again.Code).To(Equal(http.StatusAccepted))
			var second map[string]string
			Expect(json.Unmarshal(again.Body.Bytes(), &second)).To(Succeed())
			Expect(second["run_id"]).To(Equal(first["run_id"]))
		}
	})
}

var _ = Describe("GET /api/v1/sessions/:session_id/agentic-highlights", func() { enrichmentRouteSpecs(http.MethodGet, "agentic-highlights") })
var _ = Describe("POST /api/v1/sessions/:session_id/agentic-highlights", func() {
	enrichmentRouteSpecs(http.MethodPost, "agentic-highlights")
	It("rejects a rerun while the previous upload cleanup is pending", func() {
		var owner db.User
		p := testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&owner, p.UserID).Error).To(Succeed())
		sessionID := "WOD-20260928-01JAPICLEANUP0000000000000"
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: sessionID, ProfileID: p.ID, WorkoutType: "wod"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: sessionID, ProfileID: p.ID, Status: "COMPLETED", Output: "stored analysis", AgenticHighlightAnalysis: db.JSONDocument(`{"run_id":"old","status":"failed","cleanup_pending":true,"owned_upload":true,"file_name":"files/owned"}`)})
		router := newTestRouterWithAuthService(controllers.Config{QueueClient: testhelpers.NewQueueClient(), EnableAgenticHighlights: true})
		r := httptest.NewRecorder()
		router.ServeHTTP(r, newAuthorizedJSONRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%s/agentic-highlights", sessionID), "{}", &owner))
		Expect(r.Code).To(Equal(http.StatusConflict), r.Body.String())
		Expect(r.Body.String()).To(ContainSubstring("정리"))
	})
})
var _ = Describe("POST /api/v1/sessions/:session_id/analysis-summary", func() { enrichmentRouteSpecs(http.MethodPost, "analysis-summary") })
