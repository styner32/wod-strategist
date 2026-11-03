package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("GET /api/v1/history", func() {
	var router *gin.Engine
	var profileID uint
	var user db.User
	BeforeEach(func() {
		profile, owner, testRouter := historyArchiveOwner()
		profileID, user, router = profile.ID, owner, testRouter
	})

	It("does not list GCS objects while loading history", func() {
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		router := newTestRouter(controllers.Config{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:       "session-with-video",
			Status:          "COMPLETED",
			Output:          "ok",
			ProfileID:       profileID,
			AvailableVideos: db.CommaStringArray{"merged"},
		})

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profileID), nil)
		authorizeRequest(req, &user)
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

	It("fills a missing WOD description only from the same profile's session", func() {
		router := newTestRouter(controllers.Config{})
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-2025-01-01-10-00", ProfileID: other.ID, WODDescription: "Other profile's WOD"})
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-2025-01-02-10-00", ProfileID: profileID, WODDescription: "Fran"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-2025-01-01-10-00", Status: "COMPLETED", Output: "ok", ProfileID: profileID})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-2025-01-02-10-00", Status: "COMPLETED", Output: "ok", ProfileID: profileID})

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profileID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var results []map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
		descriptions := map[string]any{}
		for _, r := range results {
			descriptions[r["session_id"].(string)] = r["wod_description"]
		}
		Expect(descriptions).To(HaveLen(2))
		Expect(descriptions["WOD-2025-01-01-10-00"]).To(BeNil())
		Expect(descriptions["WOD-2025-01-02-10-00"]).To(Equal("Fran"))
	})

	It("omits empty analysis summary fields so clients keep the legacy overview", func() {
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "summary-empty", Status: "COMPLETED", Output: `{"overall_summary":"legacy"}`, ProfileID: profileID})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "summary-failed", Status: "COMPLETED", Output: "ok", ProfileID: profileID, AnalysisSummary: db.JSONDocument(`{"status":"failed","last_success":{"overview":"kept"}}`)})

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profileID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var results []map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
		summaries := map[string]any{}
		for _, r := range results {
			summaries[r["session_id"].(string)] = r["analysis_summary"]
		}
		Expect(summaries["summary-empty"]).To(Equal(map[string]any{}))
		Expect(summaries["summary-failed"]).To(Equal(map[string]any{"status": "failed", "result": map[string]any{"overview": "kept"}}))
	})

	It("returns recent history and enforces the limit", func() {
		// Seed one result so we get a non-empty response.
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "session-1",
			Status:    "COMPLETED",
			Output:    "ok",
			ProfileID: profileID,
		})

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profileID), nil)
		authorizeRequest(req, &user)
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
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID: fmt.Sprintf("paginated-session-%d", i),
				Status:    "COMPLETED",
				Output:    fmt.Sprintf("workout-%d", i),
				ProfileID: profileID,
			})
		}

		// First request: no before_id, limit=2
		req1 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&limit=2", profileID), nil)
		authorizeRequest(req1, &user)
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
		req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&before_id=%d&limit=2", profileID, cursorID), nil)
		authorizeRequest(req2, &user)
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
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(Equal("profile_id is required"))
	})

	It("returns only sessions within the from/to date range", func() {
		now := time.Now()
		yesterday := now.AddDate(0, 0, -1)
		lastWeek := now.AddDate(0, 0, -7)

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "session-today", Status: "COMPLETED", Output: "today workout", ProfileID: profileID, CreatedAt: now,
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "session-yesterday", Status: "COMPLETED", Output: "yesterday workout", ProfileID: profileID, CreatedAt: yesterday,
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "session-lastweek", Status: "COMPLETED", Output: "last week workout", ProfileID: profileID, CreatedAt: lastWeek,
		})

		from := now.AddDate(0, 0, -2).Format("2006-01-02")
		to := now.Format("2006-01-02")
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&from=%s&to=%s", profileID, from, to), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))

		var results []json.RawMessage
		Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
		Expect(results).To(HaveLen(2)) // today + yesterday, not last week
	})

	It("returns bad request for invalid from date", func() {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&from=bad-date&to=2026-06-01", profileID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(ContainSubstring("invalid from date"))
	})

	It("returns bad request for invalid to date", func() {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&from=2026-06-01&to=bad-date", profileID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(ContainSubstring("invalid to date"))
	})

	It("clamps and normalizes the limit query parameter", func() {
		// Seed 5 results
		for i := 1; i <= 5; i++ {
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID: fmt.Sprintf("limit-session-%d", i),
				Status:    "COMPLETED",
				ProfileID: profileID,
			})
		}

		// 1. Malformed limit: fallback to default (20) which returns all 5
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&limit=abc", profileID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
		var results []db.AnalysisResult
		Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
		Expect(results).To(HaveLen(5))

		// 2. Negative/Zero limit: fallback to default (20) which returns all 5
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&limit=0", profileID), nil)
		authorizeRequest(req, &user)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
		Expect(results).To(HaveLen(5))

		// 3. Respected limit
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&limit=2", profileID), nil)
		authorizeRequest(req, &user)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
		Expect(results).To(HaveLen(2))

		// 4. Oversized limit: clamp to 100
		// Let's seed 102 results in total (we already have 5, so seed 97 more)
		for i := 6; i <= 102; i++ {
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID: fmt.Sprintf("limit-session-%d", i),
				Status:    "COMPLETED",
				ProfileID: profileID,
			})
		}
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&limit=150", profileID), nil)
		authorizeRequest(req, &user)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(w.Body.Bytes(), &results)).To(Succeed())
		Expect(results).To(HaveLen(100))
	})
	It("keeps active and archive date ranges and ID cursors separate for the selected profile", func() {
		profile, user, router := historyArchiveOwner()
		day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
		archivedAt := day.Add(12 * time.Hour)
		older := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "P1-WOD-2026-10-28-08-00", ProfileID: profile.ID, Status: "COMPLETED", ArchivedAt: &archivedAt, CreatedAt: day.Add(-time.Hour)})
		active := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-LISTACTIVE", ProfileID: profile.ID, Status: "COMPLETED", CreatedAt: day.Add(time.Hour)})
		archived := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-LISTARCHIVED", ProfileID: profile.ID, Status: "CANCELLED", ArchivedAt: &archivedAt, CreatedAt: day.Add(2 * time.Hour)})
		newest := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-LISTNEWEST", ProfileID: profile.ID, Status: "PENDING", CreatedAt: day.Add(3 * time.Hour)})
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-LISTOTHER", ProfileID: other.ID, Status: "COMPLETED", CreatedAt: day.Add(4 * time.Hour)})
		for _, entry := range []struct {
			suffix string
			want   []uint
		}{
			{"&limit=1", []uint{newest.ID}},
			{fmt.Sprintf("&limit=1&before_id=%d", newest.ID), []uint{active.ID}},
			{"&archived=true&limit=1", []uint{archived.ID}},
			{fmt.Sprintf("&archived=true&limit=1&before_id=%d", archived.ID), []uint{older.ID}},
			{"&from=2026-10-29&to=2026-10-29", []uint{newest.ID, active.ID}},
			{"&archived=true&from=2026-10-29&to=2026-10-29", []uint{archived.ID}},
		} {
			response := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d%s", profile.ID, entry.suffix), "")
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			var rows []db.AnalysisResult
			Expect(json.Unmarshal(response.Body.Bytes(), &rows)).To(Succeed())
			ids := []uint{}
			for _, row := range rows {
				ids = append(ids, row.ID)
			}
			Expect(ids).To(Equal(entry.want), entry.suffix)
		}
	})

	It("validates archive filters and profile ownership", func() {
		profile, user, router := historyArchiveOwner()
		for _, suffix := range []string{"&archived=all", "&archived=1", "&from=invalid&to=2026-10-29"} {
			response := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d%s", profile.ID, suffix), "")
			Expect(response.Code).To(Equal(http.StatusBadRequest))
		}
		other := testhelpers.CreateUser(dbConn, &db.User{})
		response := historyArchiveRequest(router, other, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&archived=true", profile.ID), "")
		Expect(response.Code).To(Equal(http.StatusForbidden))
	})
})
