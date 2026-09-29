package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("GET /api/v1/sessions/:session_id/activity-summary", func() {
	var router *gin.Engine
	var profile db.Profile
	var user db.User
	var session db.Session
	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		router = newTestRouterWithAuthService(controllers.Config{})
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
		session = testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20260916-01JACTIVITYAPITEST0000000", ProfileID: profile.ID})
	})
	It("returns additive observed counts and separates incomplete coverage from zero", func() {
		start, end := 0.0, 10.0
		observations := db.NullableJSONDocument(`{"duration_secs":10,"version":1,"target_state":"identified","activity_state":"exercise","events":[{"movement":"Air Squat","unit":"reps","start_secs":1,"end_secs":3,"complete":true,"evidence":"complete cycle"}],"unassessed":[{"start_secs":8,"end_secs":10,"reason":"occlusion"}]}`)
		row := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, FilePath: "gs://bucket/chunk.mp4", Status: "COMPLETED", StartSecs: &start, EndSecs: &end, MovementObservations: observations, ObservedSignals: `{"rep_count":100}`})
		duplicate := row
		duplicate.ID = 0
		testhelpers.CreateChunkAnalysisResult(dbConn, &duplicate)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/activity-summary?profile_id=%d", session.SessionID, profile.ID), "", &user))
		Expect(response.Code).To(Equal(http.StatusOK))
		var summary activity.Summary
		Expect(json.Unmarshal(response.Body.Bytes(), &summary)).To(Succeed())
		Expect(summary.Available).To(BeTrue())
		Expect(summary.Movements[0].Count).To(Equal(1))
		Expect(summary.Unassessed).To(HaveLen(1))
	})
	It("does not expose a session belonging to another profile before any analysis exists", func() {
		other := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/activity-summary?profile_id=%d", session.SessionID, other.ID), "", &user))
		Expect(response.Code).To(Equal(http.StatusForbidden))
	})
	It("rejects another user and requires an explicit owned profile", func() {
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, other.UserID).Error).To(Succeed())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/activity-summary?profile_id=%d", session.SessionID, profile.ID), "", &otherUser))
		Expect(response.Code).To(Equal(http.StatusForbidden))
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/"+session.SessionID+"/activity-summary", "", &user))
		Expect(response.Code).To(Equal(http.StatusBadRequest))
	})
	It("leaves old recordings unevaluated even when legacy estimates exist", func() {
		start, end := 0.0, 10.0
		testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", StartSecs: &start, EndSecs: &end, ObservedSignals: `{"movement":"Air Squat","rep_count":20}`})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/activity-summary?profile_id=%d", session.SessionID, profile.ID), "", &user))
		var summary activity.Summary
		Expect(json.Unmarshal(response.Body.Bytes(), &summary)).To(Succeed())
		Expect(summary.Available).To(BeFalse())
		Expect(summary.ReviewState).To(Equal("unavailable"))
		Expect(summary.Movements).To(BeEmpty())
	})
})
