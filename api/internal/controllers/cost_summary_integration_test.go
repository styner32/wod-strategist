package controllers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/cost"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("GET /api/v1/analytics/cost", func() {
	It("aggregates scalar thinking, tool-use, cached, unmeasured, and :batch discount columns identically to full row computation", func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)

		profile := testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Cost Scalar Athlete"})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())

		sid := "WOD-20261106-01JCOSTSCALAR00000001"
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: sid, ProfileID: profile.ID})

		seedRows := []db.TokenUsage{
			testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
				UserID:          &user.ID,
				ProfileID:       profile.ID,
				SessionID:       sid,
				TaskType:        "video:segment",
				Model:           "gemini-4-argon",
				PromptTokens:    400000,
				CandidateTokens: 80000,
				TotalTokens:     530000,
				CreatedAt:       cost.ArgonStandardPricingStartsAt.Add(time.Hour),
				UsageMetadata:   db.NullableJSONDocument(`{"promptTokenCount":400000,"candidatesTokenCount":80000,"thoughtsTokenCount":50000,"cachedContentTokenCount":100000,"totalTokenCount":530000}`),
			}),
			testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
				UserID:          &user.ID,
				ProfileID:       profile.ID,
				SessionID:       sid,
				TaskType:        "video:segment:batch",
				Model:           "gemini-3.8-flash",
				PromptTokens:    200000,
				CandidateTokens: 40000,
				TotalTokens:     250000,
				CreatedAt:       cost.ArgonStandardPricingStartsAt.Add(2 * time.Hour),
				UsageMetadata:   db.NullableJSONDocument(`{"promptTokenCount":200000,"candidatesTokenCount":40000,"toolUsePromptTokenCount":10000,"totalTokenCount":250000}`),
			}),
			testhelpers.CreateTokenUsage(dbConn, &db.TokenUsage{
				UserID:          &user.ID,
				ProfileID:       profile.ID,
				SessionID:       sid,
				TaskType:        "activity:review",
				Model:           "gemini-3.8-flash",
				PromptTokens:    0,
				CandidateTokens: 0,
				TotalTokens:     0,
				CreatedAt:       cost.ArgonStandardPricingStartsAt.Add(3 * time.Hour),
				UsageMetadata:   db.NullableJSONDocument(`{}`),
			}),
		}

		router := newTestRouterWithAuthService(controllers.Config{})
		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/analytics/cost", "", &user)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		Expect(resp.Code).To(Equal(http.StatusOK), resp.Body.String())

		var got cost.TotalCostResponse
		Expect(json.Unmarshal(resp.Body.Bytes(), &got)).To(Succeed())

		expected := cost.CalculateTotalCost(seedRows)
		Expect(got.CostUSD).To(Equal(expected.CostUSD))
		Expect(got.CostKRW).To(Equal(expected.CostKRW))
		Expect(got.TotalTokens).To(Equal(expected.TotalTokens))
		Expect(got.ThinkingTokens).To(Equal(int64(50000)))
		Expect(got.ToolUseTokens).To(Equal(int64(10000)))
		Expect(got.CachedTokens).To(Equal(int64(100000)))
		Expect(got.UnmeasuredCalls).To(Equal(int64(1)))
	})
})
