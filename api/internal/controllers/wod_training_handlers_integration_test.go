package controllers_test

import (
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/wodeval"
	"net/http"
	"net/http/httptest"
	"time"
)

var _ = Describe("PUT /api/v1/sessions/:session_id/effort", func() {
	It("saves and edits integer effort only on the owner's new WOD, preserving score", func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
		session := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261013-EFFORT", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: 1})
		score := `{"evaluation_version":1,"intensity":80,"wod_start":{"seconds":120,"end_seconds":1320,"status":"confirmed"}}`
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", SessionScore: score})
		router := newTestRouterWithAuthService(controllers.Config{})
		path := fmt.Sprintf("/api/v1/sessions/%s/effort", session.SessionID)
		for _, effort := range []int{8, 4} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, fmt.Sprintf(`{"effort":%d}`, effort), &user))
			Expect(response.Code).To(Equal(200))
			var payload struct {
				Effort      int
				SessionLoad float64 `json:"session_load"`
			}
			Expect(json.Unmarshal(response.Body.Bytes(), &payload)).To(Succeed())
			Expect(payload.Effort).To(Equal(effort))
			Expect(payload.SessionLoad).To(Equal(float64(effort * 20)))
		}
		for _, body := range []string{`{"effort":0}`, `{"effort":11}`, `{"effort":1.5}`, `{}`, `{"effort":null}`} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, body, &user))
			Expect(response.Code).To(Equal(400))
		}
		var a db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", session.SessionID).First(&a).Error).To(Succeed())
		Expect(a.SessionScore).To(Equal(score))
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, other.UserID).Error).To(Succeed())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, `{"effort":9}`, &otherUser))
		Expect(response.Code).To(Equal(403))
		Expect(dbConn.Model(&session).Update("wod_evaluation_version", 0).Error).To(Succeed())
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, `{"effort":9}`, &user))
		Expect(response.Code).To(Equal(409))
	})
})

var _ = Describe("GET /api/v1/wod-training-load", func() {
	It("compares rated new WODs only, excludes archived/other workouts and withholds missing effort", func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
		now := time.Now()
		Expect(dbConn.Exec("UPDATE profiles SET wod_tracking_started_at = ? WHERE id = ?", now.AddDate(0, 0, -40), profile.ID).Error).To(Succeed())
		start, end := 120.0, 1320.0
		effort := 8
		score, _ := json.Marshal(wodeval.Score{EvaluationVersion: 1, WODStart: wodeval.Start{Seconds: &start, EndSeconds: &end, Status: "confirmed"}})
		for i, kind := range []string{"wod", "wod", "warmup", "wod"} {
			version := 1
			if i == 1 {
				version = 0
			}
			sid := fmt.Sprintf("WOD-20261013-LOAD%d", i)
			testhelpers.CreateSession(dbConn, &db.Session{SessionID: sid, ProfileID: profile.ID, WorkoutType: kind, WODEvaluationVersion: version, Effort: &effort})
			a := &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "COMPLETED", SessionScore: string(score), WorkoutAt: &now}
			if i == 3 {
				a.ArchivedAt = &now
			}
			testhelpers.CreateAnalysisResult(dbConn, a)
		}
		router := newTestRouterWithAuthService(controllers.Config{})
		path := fmt.Sprintf("/api/v1/wod-training-load?profile_id=%d&timezone=Asia%%2FSeoul", profile.ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &user))
		Expect(response.Code).To(Equal(200))
		var payload struct{ Comparison wodeval.Comparison }
		Expect(json.Unmarshal(response.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.Comparison.Seven.Total).To(Equal(160.0))
		Expect(payload.Comparison.TwentyEight.Included).To(Equal(1))
		Expect(payload.Comparison.Status).To(Equal("available"))
		Expect(dbConn.Model(&db.Session{}).Where("session_id = ?", "WOD-20261013-LOAD0").Update("effort", nil).Error).To(Succeed())
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &user))
		Expect(response.Code).To(Equal(200))
		Expect(json.Unmarshal(response.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.Comparison.Status).To(Equal("incomplete_records"))
		Expect(payload.Comparison.ChangePercent).To(BeNil())
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, other.UserID).Error).To(Succeed())
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &otherUser))
		Expect(response.Code).To(Equal(403))
	})
})

var _ = Describe("POST /api/v1/sessions/:session_id/reanalyses/:run_id/apply WOD start", func() {
	It("rejects stale start candidates without replacing score or effort", func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
		effort := 7
		session := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261013-STALESTART", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: 1, Effort: &effort})
		current := `{"evaluation_version":1,"intensity":80}`
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", Output: "newer analysis", SessionScore: current})
		run := testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", OriginalAnalysisSnapshot: db.JSONDocument(`{"output":"old analysis","session_score":"{}"}`), Output: "stale candidate", SessionScore: `{"evaluation_version":1,"intensity":70}`})
		router := newTestRouterWithAuthService(controllers.Config{})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%s/reanalyses/%d/apply", session.SessionID, run.ID), `{}`, &user))
		Expect(response.Code).To(Equal(409))
		var stored db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", session.SessionID).First(&stored).Error).To(Succeed())
		Expect(stored.SessionScore).To(Equal(current))
		Expect(dbConn.First(&session, session.ID).Error).To(Succeed())
		Expect(session.Effort).NotTo(BeNil())
		Expect(*session.Effort).To(Equal(7))
	})
})
