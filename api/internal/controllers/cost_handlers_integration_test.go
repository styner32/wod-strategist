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
		ensureTestDB()
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

	It("retains both Argon pricing periods in one session model breakdown", func() {
		for _, date := range []time.Time{cost.ArgonStandardPricingStartsAt.Add(-time.Second), cost.ArgonStandardPricingStartsAt} {
			testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{SessionID: session.SessionID, ProfileID: profile.ID,
				TaskType: "video:segment", Model: "gemini-4-argon", CreatedAt: date, PromptTokens: 1000000, CandidateTokens: 500000,
				UsageMetadata: db.NullableJSONDocument(`{"promptTokenCount":1000000,"candidatesTokenCount":500000,"thoughtsTokenCount":500000,"totalTokenCount":2000000}`)})
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/cost", session.SessionID), "", &user))
		Expect(response.Code).To(Equal(http.StatusOK))
		var body cost.SessionCostResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.CostUSD).To(Equal(36.0))
		Expect(body.ByModel).To(HaveLen(1))
		Expect(body.ByModel[0].CostUSD).To(Equal(36.0))
	})

	It("includes total and per-stage durations from pipeline_stage_metrics", func() {
		testhelpers.CreatePipelineStageMetric(dbConn, &db.PipelineStageMetric{
			SessionID:  session.SessionID,
			ProfileID:  profile.ID,
			Stage:      "video_analysis",
			Variant:    "B",
			APICalls:   8,
			DurationMs: 185000,
		})
		testhelpers.CreatePipelineStageMetric(dbConn, &db.PipelineStageMetric{
			SessionID:  session.SessionID,
			ProfileID:  profile.ID,
			Stage:      "verify_highlights",
			Variant:    "B",
			APICalls:   1,
			DurationMs: 12000,
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/cost", session.SessionID), "", &user))
		Expect(response.Code).To(Equal(http.StatusOK))
		var body cost.SessionCostResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.TotalDurationMs).To(Equal(int64(197000)))
		Expect(body.ByStage).To(Equal([]cost.StageDurationItem{
			{Stage: "video_analysis", DurationMs: 185000, APICalls: 8},
			{Stage: "verify_highlights", DurationMs: 12000, APICalls: 1},
		}))
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
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)

		user = testhelpers.CreateUser(dbConn, &db.User{Username: "testuser"})
		profile1 = testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID, Name: "Profile 1"})
		profile2 = testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID, Name: "Profile 2"})

		router = newTestRouterWithAuthService(controllers.Config{})
	})

	It("includes account-only usage without leaking it into a selected profile or another user", func() {
		other := testhelpers.CreateUser(dbConn, &db.User{Username: "other-account"})
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{UserID: &user.ID, TaskType: "image:workout", Model: "gemini-3.8-flash", PromptTokens: 100, CandidateTokens: 20, TotalTokens: 460, UsageMetadata: db.NullableJSONDocument(`{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":40,"toolUsePromptTokenCount":300,"cachedContentTokenCount":50,"totalTokenCount":460}`)})
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{UserID: &other.ID, TaskType: "image:workout", Model: "gemini-3.8-flash", TotalTokens: 9999})
		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/analytics/cost", "", &user)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		Expect(resp.Code).To(Equal(http.StatusOK))
		var total cost.TotalCostResponse
		Expect(json.Unmarshal(resp.Body.Bytes(), &total)).To(Succeed())
		Expect(total.TotalTokens).To(Equal(int64(460)))
		Expect(total.ThinkingTokens).To(Equal(int64(40)))
		Expect(total.ToolUseTokens).To(Equal(int64(300)))
		expected, _ := cost.CalculateTokensCost("gemini-3.8-flash", 400, 60)
		Expect(total.CostUSD).To(Equal(expected))
		req = newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/analytics/cost?profile_id=%d", profile1.ID), "", &user)
		resp = httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		Expect(resp.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(resp.Body.Bytes(), &total)).To(Succeed())
		Expect(total.TotalTokens).To(BeZero())
	})

	It("separates Argon pricing dates while preserving account and profile ownership", func() {
		for _, date := range []time.Time{cost.ArgonStandardPricingStartsAt.Add(-time.Second), cost.ArgonStandardPricingStartsAt} {
			testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{ProfileID: profile1.ID, SessionID: "priced-session", TaskType: "video:segment",
				Model: "gemini-4-argon", CreatedAt: date, PromptTokens: 1000000, CandidateTokens: 500000,
				UsageMetadata: db.NullableJSONDocument(`{"promptTokenCount":1000000,"candidatesTokenCount":500000,"thoughtsTokenCount":500000,"totalTokenCount":2000000}`)})
		}
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{UserID: &user.ID, TaskType: "image:workout", Model: "gemini-4-argon",
			CreatedAt: cost.ArgonStandardPricingStartsAt.Add(-time.Second), PromptTokens: 1000000, CandidateTokens: 1000000})
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{ProfileID: other.ID, SessionID: "other", TaskType: "video:segment", Model: "gemini-4-argon",
			CreatedAt: cost.ArgonStandardPricingStartsAt, PromptTokens: 1000000, CandidateTokens: 1000000})
		for _, test := range []struct {
			url string
			usd float64
		}{
			{"/api/v1/analytics/cost", 48.0},
			{fmt.Sprintf("/api/v1/analytics/cost?profile_id=%d", profile1.ID), 36.0},
			{fmt.Sprintf("/api/v1/analytics/cost?profile_id=%d", profile2.ID), 0.0},
		} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, test.url, "", &user))
			Expect(response.Code).To(Equal(http.StatusOK))
			var body cost.TotalCostResponse
			Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
			Expect(body.CostUSD).To(Equal(test.usd))
		}
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
