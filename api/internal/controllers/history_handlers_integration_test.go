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
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/wodeval"
	"github.com/wod-strategist/api/internal/workoutprogress"
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
		for _, suffix := range []string{"&archived=all", "&archived=1", "&from=invalid&to=2026-10-29", "&missing=unknown", "&archived=true&missing=any"} {
			response := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d%s", profile.ID, suffix), "")
			Expect(response.Code).To(Equal(http.StatusBadRequest), suffix)
		}
		other := testhelpers.CreateUser(dbConn, &db.User{})
		response := historyArchiveRequest(router, other, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&archived=true", profile.ID), "")
		Expect(response.Code).To(Equal(http.StatusForbidden))
	})

	It("computes input_status and filters missing effort and performance confirmations across cursor pages", func() {
		profile, user, router := historyArchiveOwner()
		progressFor := func(sourceID string) (workoutprogress.Progress, db.NullableJSONDocument) {
			p := workoutprogress.Build([]workoutprogress.Interval{
				{
					ID:              "interval-1",
					Start:           0,
					End:             10,
					Activity:        "exercise",
					Movement:        "Power Clean",
					ObservationRefs: []string{"seg-1"},
					RepEvents: []activity.Observation{
						{Movement: "Power Clean", Unit: "reps", Start: 1, End: 3, Complete: true, Evidence: "clean rep"},
					},
				},
			}, nil, sourceID)
			raw, err := json.Marshal(p)
			Expect(err).NotTo(HaveOccurred())
			return p, db.NullableJSONDocument(raw)
		}

		effortEight := 8
		repsOne := 1

		// 1. Legacy WOD (evaluation_version = 0): not effort_eligible, no workout_progress -> nothing missing
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-LEGACY", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: 0})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-LEGACY", ProfileID: profile.ID, Status: "COMPLETED", Output: "ok"})

		// 2. Completed v1 WOD with effort saved AND performance confirmed -> nothing missing
		pDone, rawDone := progressFor("done-src")
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-DONE", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: wodeval.Version, Effort: &effortEight})
		aDone := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-DONE", ProfileID: profile.ID, Status: "COMPLETED", Output: "ok", WorkoutProgress: rawDone})
		testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{
			ProfileID:     profile.ID,
			AnalysisID:    aDone.ID,
			SessionID:     "WOD-DONE",
			IntervalID:    "interval-1",
			SourceVersion: pDone.SourceVersion,
			Status:        "confirmed",
			Outcome:       "completed",
			Movement:      "Power Clean",
			StartSecs:     0,
			EndSecs:       10,
			Reps:          &repsOne,
		})

		// 3. Completed v1 WOD missing effort ONLY (performance confirmed)
		pMissingEffort, rawMissingEffort := progressFor("effort-src")
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-MISSING-EFFORT", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: wodeval.Version})
		aMissingEffort := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-MISSING-EFFORT", ProfileID: profile.ID, Status: "COMPLETED", Output: "ok", WorkoutProgress: rawMissingEffort})
		testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{
			ProfileID:     profile.ID,
			AnalysisID:    aMissingEffort.ID,
			SessionID:     "WOD-MISSING-EFFORT",
			IntervalID:    "interval-1",
			SourceVersion: pMissingEffort.SourceVersion,
			Status:        "confirmed",
			Outcome:       "completed",
			Movement:      "Power Clean",
			StartSecs:     0,
			EndSecs:       10,
			Reps:          &repsOne,
		})

		// 4. Non-WOD (accessory) missing performance confirmation (stale source_version)
		_, rawMissingPerf := progressFor("perf-src-v2")
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "ACCESSORY-MISSING-PERF", ProfileID: profile.ID, WorkoutType: "accessory", WODEvaluationVersion: 0})
		aMissingPerf := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "ACCESSORY-MISSING-PERF", ProfileID: profile.ID, Status: "COMPLETED", Output: "ok", WorkoutProgress: rawMissingPerf})
		testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{
			ProfileID:     profile.ID,
			AnalysisID:    aMissingPerf.ID,
			SessionID:     "ACCESSORY-MISSING-PERF",
			IntervalID:    "interval-1",
			SourceVersion: "stale-perf-src-v1",
			Status:        "confirmed",
			Outcome:       "completed",
			Movement:      "Power Clean",
			StartSecs:     0,
			EndSecs:       10,
			Reps:          &repsOne,
		})

		// 5. Pending v1 WOD (not yet COMPLETED): should NOT be flagged as missing
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-PENDING", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: wodeval.Version})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-PENDING", ProfileID: profile.ID, Status: "PENDING", Output: ""})

		// 6. Completed v1 WOD missing BOTH effort and performance confirmation
		_, rawMissingBoth := progressFor("both-src")
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-MISSING-BOTH", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: wodeval.Version})
		aMissingBoth := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-MISSING-BOTH", ProfileID: profile.ID, Status: "COMPLETED", Output: "ok", WorkoutProgress: rawMissingBoth})

		// Unfiltered list returns input_status for every active item
		allResp := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profile.ID), "")
		Expect(allResp.Code).To(Equal(http.StatusOK), allResp.Body.String())
		var allRows []db.AnalysisResult
		Expect(json.Unmarshal(allResp.Body.Bytes(), &allRows)).To(Succeed())
		statusBySession := map[string]*db.HistoryInputStatus{}
		for _, r := range allRows {
			statusBySession[r.SessionID] = r.InputStatus
		}
		Expect(statusBySession["WOD-DONE"]).NotTo(BeNil())
		Expect(statusBySession["WOD-DONE"].EffortEligible).To(BeTrue())
		Expect(statusBySession["WOD-DONE"].Effort).NotTo(BeNil())
		Expect(*statusBySession["WOD-DONE"].Effort).To(Equal(8))
		Expect(statusBySession["WOD-DONE"].EffortMissing).To(BeFalse())
		Expect(statusBySession["WOD-DONE"].PerformanceMissing).To(BeFalse())

		Expect(statusBySession["ACCESSORY-MISSING-PERF"].EffortEligible).To(BeFalse())
		Expect(statusBySession["ACCESSORY-MISSING-PERF"].NeedsConfirmationCount).To(Equal(1))
		Expect(statusBySession["ACCESSORY-MISSING-PERF"].PerformanceMissing).To(BeTrue())

		Expect(statusBySession["WOD-PENDING"].EffortEligible).To(BeTrue())
		Expect(statusBySession["WOD-PENDING"].EffortMissing).To(BeFalse())

		// Filter: missing=effort -> [WOD-MISSING-BOTH, WOD-MISSING-EFFORT]
		effortResp := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&missing=effort", profile.ID), "")
		Expect(effortResp.Code).To(Equal(http.StatusOK))
		var effortRows []db.AnalysisResult
		Expect(json.Unmarshal(effortResp.Body.Bytes(), &effortRows)).To(Succeed())
		Expect([]uint{effortRows[0].ID, effortRows[1].ID}).To(Equal([]uint{aMissingBoth.ID, aMissingEffort.ID}))

		// Filter: missing=performance -> [WOD-MISSING-BOTH, ACCESSORY-MISSING-PERF]
		perfResp := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&missing=performance", profile.ID), "")
		Expect(perfResp.Code).To(Equal(http.StatusOK))
		var perfRows []db.AnalysisResult
		Expect(json.Unmarshal(perfResp.Body.Bytes(), &perfRows)).To(Succeed())
		Expect([]uint{perfRows[0].ID, perfRows[1].ID}).To(Equal([]uint{aMissingBoth.ID, aMissingPerf.ID}))

		// Filter: missing=any with limit=2 + before_id cursor pagination
		anyPage1 := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&missing=any&limit=2", profile.ID), "")
		Expect(anyPage1.Code).To(Equal(http.StatusOK))
		var page1Rows []db.AnalysisResult
		Expect(json.Unmarshal(anyPage1.Body.Bytes(), &page1Rows)).To(Succeed())
		Expect([]uint{page1Rows[0].ID, page1Rows[1].ID}).To(Equal([]uint{aMissingBoth.ID, aMissingPerf.ID}))

		anyPage2 := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d&missing=any&limit=2&before_id=%d", profile.ID, page1Rows[1].ID), "")
		Expect(anyPage2.Code).To(Equal(http.StatusOK))
		var page2Rows []db.AnalysisResult
		Expect(json.Unmarshal(anyPage2.Body.Bytes(), &page2Rows)).To(Succeed())
		Expect(page2Rows).To(HaveLen(1))
		Expect(page2Rows[0].ID).To(Equal(aMissingEffort.ID))
	})

	It("embeds batched completed highlights and compact activity summaries while clearing raw workout_progress", func() {
		profile, user, router := historyArchiveOwner()
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})

		progress := workoutprogress.Build([]workoutprogress.Interval{
			{
				ID:       "interval-1",
				Start:    0,
				End:      10,
				Activity: "exercise",
				Movement: "Burpee",
				RepEvents: []activity.Observation{
					{Movement: "Burpee", Unit: "reps", Start: 1, End: 4, Complete: true, Evidence: "rep 1"},
				},
			},
		}, nil, "history-src")
		rawProgress, err := json.Marshal(progress)
		Expect(err).NotTo(HaveOccurred())

		obsDoc := activity.Observations{
			Version:       activity.Version,
			DurationSecs:  10,
			TargetState:   "identified",
			ActivityState: "exercise",
			Events: []activity.Observation{
				{Movement: "Burpee", Unit: "reps", Start: 1, End: 4, Complete: true, Evidence: "rep 1"},
				{Movement: "Burpee", Unit: "reps", Start: 5, End: 8, Complete: true, Evidence: "rep 2"},
			},
			Unassessed: []activity.Interval{},
		}
		rawObs, err := json.Marshal(obsDoc)
		Expect(err).NotTo(HaveOccurred())
		startZero, endTen := 0.0, 10.0
		mediaZero, mediaTen := 0.0, 10.0

		sidWithHighlights := "WOD-20261105-01HISTHL001"
		chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			ProfileID:            profile.ID,
			SessionID:            sidWithHighlights,
			FilePath:             fmt.Sprintf("gs://test-bucket/videos/%d/%s/c1.mp4", profile.ID, sidWithHighlights),
			ExerciseType:         "Burpee",
			Status:               "COMPLETED",
			StartSecs:            &startZero,
			EndSecs:              &endTen,
			MediaStartSecs:       &mediaZero,
			MediaEndSecs:         &mediaTen,
			MovementObservations: db.NullableJSONDocument(rawObs),
		})
		expectedSummary := activity.Build([]db.ChunkAnalysisResult{chunk}, nil)
		storedSummary := activity.Summary{
			Version:         activity.Version,
			Available:       true,
			SourceVersion:   expectedSummary.SourceVersion,
			ReviewVersion:   activity.Version,
			ReviewState:     "completed",
			CoverageScope:   "recorded_chunks",
			MediaGeneration: "42",
			Reviews: []activity.ReviewedChunk{
				{ChunkID: chunk.ID, State: "completed", Observations: obsDoc},
			},
		}
		rawStoredSummary, err := json.Marshal(storedSummary)
		Expect(err).NotTo(HaveOccurred())

		testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:            sidWithHighlights,
			ProfileID:            profile.ID,
			WorkoutType:          "wod",
			WODEvaluationVersion: wodeval.Version,
			ActivitySummary:      db.NullableJSONDocument(rawStoredSummary),
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:         sidWithHighlights,
			ProfileID:         profile.ID,
			AnalysisType:      db.AnalysisTypeWOD,
			Status:            "COMPLETED",
			Output:            "ok",
			HighlightSegments: `[{"start":0,"end":10,"type":"best_form"}]`,
			WorkoutProgress:   db.NullableJSONDocument(rawProgress),
			SensorTimeline:    db.JSONDocument(`[{"t":1,"hr":160}]`),
		})

		baseTime := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
		olderHL := testhelpers.CreateHighlightResult(dbConn, &db.HighlightResult{
			SessionID:   sidWithHighlights,
			ProfileID:   profile.ID,
			Title:       "Best Forms",
			Status:      "COMPLETED",
			GCSURI:      "gs://test-bucket/hl-old.mp4",
			DurationSec: 8,
			CreatedAt:   baseTime,
		})
		newerHL := testhelpers.CreateHighlightResult(dbConn, &db.HighlightResult{
			SessionID:   sidWithHighlights,
			ProfileID:   profile.ID,
			Title:       "Full Reel",
			Status:      "COMPLETED",
			GCSURI:      "gs://test-bucket/hl-new.mp4",
			DurationSec: 10,
			CreatedAt:   baseTime.Add(time.Minute),
		})
		// Non-completed and other-profile highlights must be excluded.
		testhelpers.CreateHighlightResult(dbConn, &db.HighlightResult{
			SessionID: sidWithHighlights,
			ProfileID: profile.ID,
			Title:     "Pending Reel",
			Status:    "PROCESSING",
		})
		testhelpers.CreateHighlightResult(dbConn, &db.HighlightResult{
			SessionID: sidWithHighlights,
			ProfileID: other.ID,
			Title:     "Other Profile Reel",
			Status:    "COMPLETED",
		})

		sidEmpty := "WOD-20261105-01HISTEMPTY"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:    sidEmpty,
			ProfileID:    profile.ID,
			AnalysisType: db.AnalysisTypeWOD,
			Status:       "COMPLETED",
			Output:       "ok",
		})

		resp := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/history?profile_id=%d", profile.ID), "")
		Expect(resp.Code).To(Equal(http.StatusOK), resp.Body.String())

		var rawItems []map[string]any
		Expect(json.Unmarshal(resp.Body.Bytes(), &rawItems)).To(Succeed())
		bySession := map[string]map[string]any{}
		for _, item := range rawItems {
			bySession[item["session_id"].(string)] = item
		}

		rich := bySession[sidWithHighlights]
		Expect(rich).NotTo(BeNil())
		// Raw workout_progress is omitted from payload after computing input_status.
		Expect(rich["workout_progress"]).To(BeNil())
		inputStatus, ok := rich["input_status"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(inputStatus["performance_missing"]).To(BeTrue())

		highlightsRaw, ok := rich["highlights"].([]any)
		Expect(ok).To(BeTrue())
		Expect(highlightsRaw).To(HaveLen(2))
		Expect(highlightsRaw[0].(map[string]any)["id"]).To(BeNumerically("==", newerHL.ID))
		Expect(highlightsRaw[1].(map[string]any)["id"]).To(BeNumerically("==", olderHL.ID))

		summaryRaw, ok := rich["activity_summary"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(summaryRaw["available"]).To(BeTrue())
		Expect(summaryRaw["review_state"]).To(Equal("completed"))
		Expect(summaryRaw["reviews"]).To(BeEmpty())
		movements, ok := summaryRaw["movements"].([]any)
		Expect(ok).To(BeTrue())
		Expect(movements).To(HaveLen(1))
		Expect(movements[0].(map[string]any)["movement"]).To(Equal("Burpee"))
		Expect(movements[0].(map[string]any)["count"]).To(BeNumerically("==", 2))

		empty := bySession[sidEmpty]
		Expect(empty).NotTo(BeNil())
		Expect(empty["highlights"]).To(Equal([]any{}))
		emptySummary, ok := empty["activity_summary"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(emptySummary["available"]).To(BeFalse())
		Expect(emptySummary["review_state"]).To(Equal("unavailable"))
	})
})
