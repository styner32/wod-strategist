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
	"github.com/wod-strategist/api/internal/cost"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("GET /api/v1/sessions/:session_id/cost", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
		session db.Session
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID: "WOD-20260407-01JCOST0000000000000001",
			ProfileID: profile.ID,
		})

		router = newTestRouterWithAuthService(controllers.Config{})
	})

	It("returns 200 with empty usage when no token usages exist", func() {
		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/cost", session.SessionID), "", &user)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		Expect(resp.Code).To(Equal(http.StatusOK))
		var body cost.SessionCostResponse
		Expect(json.Unmarshal(resp.Body.Bytes(), &body)).To(Succeed())
		Expect(body.SessionID).To(Equal(session.SessionID))
		Expect(body.TotalTokens).To(Equal(int64(0)))
		Expect(body.CostUSD).To(Equal(0.0))
		Expect(body.CostKRW).To(Equal(0.0))
		Expect(body.ByTaskType).To(BeEmpty())
		Expect(body.ByModel).To(BeEmpty())
	})

	It("returns 200 with calculated costs and breakdowns for owned session", func() {
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
			SessionID:       session.SessionID,
			ProfileID:       profile.ID,
			TaskType:        "video:index",
			Model:           "gemini-3.8-flash",
			PromptTokens:    100000,
			CandidateTokens: 10000,
			TotalTokens:     110000,
		})
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
			SessionID:       session.SessionID,
			ProfileID:       profile.ID,
			TaskType:        "video:segment",
			Model:           "gemini-3.8-flash",
			PromptTokens:    200000,
			CandidateTokens: 20000,
			TotalTokens:     220000,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/cost", session.SessionID), "", &user)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		Expect(resp.Code).To(Equal(http.StatusOK))
		var body cost.SessionCostResponse
		Expect(json.Unmarshal(resp.Body.Bytes(), &body)).To(Succeed())
		Expect(body.SessionID).To(Equal(session.SessionID))
		Expect(body.PromptTokens).To(Equal(int64(300000)))
		Expect(body.CandidateTokens).To(Equal(int64(30000)))
		Expect(body.TotalTokens).To(Equal(int64(330000)))
		Expect(body.CostUSD).To(BeNumerically(">", 0))
		Expect(body.CostKRW).To(Equal(cost.RoundKRW(body.CostUSD * 1380.0)))

		Expect(body.ByTaskType).To(HaveLen(2))
		Expect(body.ByModel).To(HaveLen(1))
		Expect(body.ByModel[0].Key).To(Equal("gemini-3.8-flash"))
	})

	It("rejects access for a session belonging to another user", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		otherUser := db.User{}
		Expect(dbConn.First(&otherUser, otherProfile.UserID).Error).NotTo(HaveOccurred())

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/cost", session.SessionID), "", &otherUser)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		Expect(resp.Code).To(Equal(http.StatusForbidden))
	})
})

var _ = Describe("GET /api/v1/analytics/cost", func() {
	var (
		router   *gin.Engine
		profile1 db.Profile
		profile2 db.Profile
		user     db.User
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)

		user = testhelpers.CreateUser(dbConn, &db.User{Username: "testuser"})
		profile1 = testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID, Name: "Profile 1"})
		profile2 = testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID, Name: "Profile 2"})

		router = newTestRouterWithAuthService(controllers.Config{})
	})

	It("returns cumulative totals across all user sessions and profiles", func() {
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
			SessionID:       "WOD-1",
			ProfileID:       profile1.ID,
			TaskType:        "video:index",
			Model:           "gemini-3.8-flash",
			PromptTokens:    500000,
			CandidateTokens: 50000,
			TotalTokens:     550000,
		})
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
			SessionID:       "WOD-2",
			ProfileID:       profile2.ID,
			TaskType:        "video:segment",
			Model:           "gemini-3.5-flash-lite",
			PromptTokens:    400000,
			CandidateTokens: 40000,
			TotalTokens:     440000,
		})

		// Another user's usage should not be included
		otherUser := testhelpers.CreateUser(dbConn, &db.User{Username: "otheruser"})
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: otherUser.ID})
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
			SessionID:       "WOD-3",
			ProfileID:       otherProfile.ID,
			TaskType:        "video:index",
			Model:           "gemini-3.8-flash",
			PromptTokens:    1000000,
			CandidateTokens: 1000000,
			TotalTokens:     2000000,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/analytics/cost", "", &user)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		Expect(resp.Code).To(Equal(http.StatusOK))
		var body cost.TotalCostResponse
		Expect(json.Unmarshal(resp.Body.Bytes(), &body)).To(Succeed())
		Expect(body.PromptTokens).To(Equal(int64(900000)))
		Expect(body.CandidateTokens).To(Equal(int64(90000)))
		Expect(body.TotalTokens).To(Equal(int64(990000)))
		Expect(body.CostUSD).To(BeNumerically(">", 0))
		Expect(body.CostKRW).To(Equal(cost.RoundKRW(body.CostUSD * 1380.0)))
	})

	It("supports filtering by profile_id", func() {
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
			SessionID:       "WOD-1",
			ProfileID:       profile1.ID,
			TaskType:        "video:index",
			Model:           "gemini-3.8-flash",
			PromptTokens:    500000,
			CandidateTokens: 50000,
			TotalTokens:     550000,
		})
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
			SessionID:       "WOD-2",
			ProfileID:       profile2.ID,
			TaskType:        "video:segment",
			Model:           "gemini-3.5-flash-lite",
			PromptTokens:    400000,
			CandidateTokens: 40000,
			TotalTokens:     440000,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/analytics/cost?profile_id=%d", profile1.ID), "", &user)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		Expect(resp.Code).To(Equal(http.StatusOK))
		var body cost.TotalCostResponse
		Expect(json.Unmarshal(resp.Body.Bytes(), &body)).To(Succeed())
		Expect(body.PromptTokens).To(Equal(int64(500000)))
		Expect(body.CandidateTokens).To(Equal(int64(50000)))
		Expect(body.TotalTokens).To(Equal(int64(550000)))
	})
})
