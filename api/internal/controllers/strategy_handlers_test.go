package controllers_test

import (
	"bytes"
	"context"
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
	"github.com/wod-strategist/api/internal/fatigue"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
)

type mockTextParser struct {
	response string
	err      error
}

func (m *mockTextParser) ParseText(ctx context.Context, prompt string) (string, *gemini.TokenUsage, error) {
	if m.err != nil {
		return "", nil, m.err
	}
	return m.response, &gemini.TokenUsage{TotalTokens: 100}, nil
}

var _ = Describe("POST /api/v1/strategies/pre-wod-advice", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{
			FitnessLevel: "advanced",
		})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
	})

	It("returns 400 when profile_id is missing", func() {
		router = newTestRouterWithAuthService(controllers.Config{})
		reqBody := []byte(`{"wod_description":"Fran"}`)
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/strategies/pre-wod-advice", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusBadRequest))
	})

	It("returns 403 when user does not own profile", func() {
		router = newTestRouterWithAuthService(controllers.Config{})
		otherUser := testhelpers.CreateUser(dbConn, &db.User{Username: "other-user"})
		token := generateValidToken(otherUser)

		reqBody := []byte(fmt.Sprintf(`{"profile_id":%d,"wod_description":"Fran"}`, profile.ID))
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/strategies/pre-wod-advice", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusForbidden))
	})

	It("returns deterministic fallback advice when Gemini is not configured", func() {
		router = newTestRouterWithAuthService(controllers.Config{})
		now := time.Now()

		// Add recent past session with heavy shoulder load in SessionScore
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:    "WOD-20260901-01PASTSESSION0001",
			ProfileID:    profile.ID,
			Status:       "COMPLETED",
			AnalysisType: db.AnalysisTypeWOD,
			SessionScore: `{"intensity":85,"movements":{"Push Jerk":{"reps":30},"Thruster":{"reps":45}}}`,
			CreatedAt:    now.Add(-10 * time.Hour),
		})

		reqBody := []byte(fmt.Sprintf(`{"profile_id":%d,"wod_description":"21-15-9 Thrusters and Pull-ups","movements":["Thruster","Pull-up"]}`, profile.ID))
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/strategies/pre-wod-advice", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))

		var resp controllers.PreWODAdviceResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.ProfileID).To(Equal(profile.ID))
		Expect(resp.MuscleReadiness).To(HaveLen(len(fatigue.AllMuscleGroups)))
		Expect(resp.OverallFatigueScore).To(BeNumerically(">", 0))
		Expect(resp.TargetRPE.Score).To(BeNumerically(">=", 1))
		Expect(resp.OverallSummary).NotTo(BeEmpty())
	})

	It("uses Gemini generated output when textParser is configured", func() {
		mockGemini := &mockTextParser{
			response: `{
				"muscle_readiness": [
					{
						"group": "shoulders_push",
						"name_ko": "어깨 / 상체 밀기",
						"fatigue_score": 75,
						"state": "fatigued",
						"state_ko": "피로 주의",
						"note": "어깨 피로 누적"
					}
				],
				"target_rpe": {
					"score": 7,
					"label": "RPE 7 (조절된 페이스)",
					"pacing_strategy": "초반 페이스를 80%로 조절하세요."
				},
				"scaling_advice": [
					{
						"movement": "Thruster",
						"recommendation": "중량 조절 추천",
						"detail": "처방 무게의 80%로 조절 권장"
					}
				],
				"mobility_warmup": [
					{
						"title": "Thoracic Extension",
						"target_area": "Upper Back",
						"duration": "2분",
						"reason": "흉추 가동성 확보"
					}
				],
				"overall_summary": "어깨 피로에 주의하여 템포를 조절하세요."
			}`,
		}

		router = newTestRouterWithAuthService(controllers.Config{
			TextParser: mockGemini,
		})

		reqBody := []byte(fmt.Sprintf(`{"profile_id":%d,"wod_description":"Fran","movements":["Thruster","Pull-up"]}`, profile.ID))
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/strategies/pre-wod-advice", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))

		var resp controllers.PreWODAdviceResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.TargetRPE.Score).To(Equal(7))
		Expect(resp.ScalingAdvice).To(HaveLen(1))
		Expect(resp.ScalingAdvice[0].Movement).To(Equal("Thruster"))
		Expect(resp.OverallSummary).To(Equal("어깨 피로에 주의하여 템포를 조절하세요."))
	})
})
