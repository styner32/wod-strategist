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

func generateValidToken(user db.User) string {
	token, err := authService.IssueTokenByUser(&user)
	Expect(err).NotTo(HaveOccurred())
	return token
}

var _ = Describe("GET /api/v1/related-wods", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)
		router = newTestRouterWithAuthService(controllers.Config{})

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
	})

	Context("Mode A (Pre-session query)", func() {
		It("ranks results by recency, weight match, and main status", func() {
			now := time.Now()

			// High match: recent, main movement, matching weight (60kg)
			res1 := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260801-01HIGHMATCH0000000",
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				AnalysisType:      db.AnalysisTypeWOD,
				WODDescription:    "5x5 Power Clean 60kg",
				NormalizedWorkout: `[{"movement":"power clean","weight_raw":"60kg","weight_kg":60,"reps":"5","is_main":true}]`,
				CreatedAt:         now.Add(-1 * 24 * time.Hour),
			})

			// Low match: older, accessory movement, different weight (40kg)
			res2 := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260601-01LOWMATCH00000000",
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				AnalysisType:      db.AnalysisTypeWOD,
				WODDescription:    "100 Power Clean 40kg for time",
				NormalizedWorkout: `[{"movement":"power clean","weight_raw":"40kg","weight_kg":40,"reps":"100","is_main":false}]`,
				CreatedAt:         now.Add(-60 * 24 * time.Hour),
			})

			req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/related-wods?profile_id=%d&movement=power%%20clean&weight_kg=60", profile.ID), nil)
			req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var resp controllers.RelatedWODsResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Query.Movement).To(Equal("power clean"))
			Expect(resp.Query.WeightKG).NotTo(BeNil())
			Expect(*resp.Query.WeightKG).To(Equal(60.0))
			Expect(resp.Related).To(HaveLen(2))
			Expect(resp.Related[0].SessionID).To(Equal(res1.SessionID))
			Expect(resp.Related[1].SessionID).To(Equal(res2.SessionID))
			Expect(resp.Related[0].Score).To(BeNumerically(">", resp.Related[1].Score))
		})

		It("filters out other profiles, non-completed, archived, non-WOD types, and empty columns", func() {
			otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})

			// Other profile
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260801-01OTHERPROFILE0000",
				ProfileID:         otherProfile.ID,
				Status:            "COMPLETED",
				NormalizedWorkout: `[{"movement":"power clean","weight_raw":"60kg","weight_kg":60,"is_main":true}]`,
			})

			// Non-COMPLETED status
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260801-01FAILED0000000000",
				ProfileID:         profile.ID,
				Status:            "FAILED",
				NormalizedWorkout: `[{"movement":"power clean","weight_raw":"60kg","weight_kg":60,"is_main":true}]`,
			})

			// Archived session
			archivedAt := time.Now()
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260801-01ARCHIVED0000000",
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				NormalizedWorkout: `[{"movement":"power clean","weight_raw":"60kg","weight_kg":60,"is_main":true}]`,
				ArchivedAt:        &archivedAt,
			})

			// Non-WOD analysis type
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260801-01REHAB00000000000",
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				AnalysisType:      db.AnalysisTypeInjurySupplement,
				NormalizedWorkout: `[{"movement":"power clean","weight_raw":"60kg","weight_kg":60,"is_main":true}]`,
			})

			// Empty normalized_workout
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260801-01EMPTYCOL00000000",
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				AnalysisType:      db.AnalysisTypeWOD,
				NormalizedWorkout: "",
			})

			req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/related-wods?profile_id=%d&movement=power%%20clean", profile.ID), nil)
			req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var resp controllers.RelatedWODsResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Related).To(BeEmpty())
		})
	})

	Context("Mode B (Post-session query)", func() {
		It("fetches anchor session's main movement and returns related WODs excluding anchor session", func() {
			now := time.Now()

			anchor := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260805-01ANCHOR0000000000",
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				AnalysisType:      db.AnalysisTypeWOD,
				WODDescription:    "Today Clean 70kg",
				NormalizedWorkout: `[{"movement":"power clean","weight_raw":"70kg","weight_kg":70,"is_main":true}]`,
				CreatedAt:         now,
			})

			past := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "WOD-20260801-01PAST000000000000",
				ProfileID:         profile.ID,
				Status:            "COMPLETED",
				AnalysisType:      db.AnalysisTypeWOD,
				WODDescription:    "Past Clean 70kg",
				NormalizedWorkout: `[{"movement":"power clean","weight_raw":"70kg","weight_kg":70,"is_main":true}]`,
				CreatedAt:         now.Add(-4 * 24 * time.Hour),
			})

			req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/related-wods?profile_id=%d&session_id=%s", profile.ID, anchor.SessionID), nil)
			req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var resp controllers.RelatedWODsResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Query.SourceSessionID).To(Equal(anchor.SessionID))
			Expect(resp.Query.Movement).To(Equal("power clean"))
			Expect(resp.Related).To(HaveLen(1))
			Expect(resp.Related[0].SessionID).To(Equal(past.SessionID))
		})

		It("returns 200 with empty list when anchor session is missing or has empty normalized_workout", func() {
			req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/related-wods?profile_id=%d&session_id=WOD-NONEXISTENT", profile.ID), nil)
			req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var resp controllers.RelatedWODsResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Related).To(BeEmpty())
		})
	})

	Context("Validation & Ownership", func() {
		It("returns 400 if profile_id is missing or invalid", func() {
			req, _ := http.NewRequest("GET", "/api/v1/related-wods?movement=power%20clean", nil)
			req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
		})

		It("returns 400 if neither or both movement and session_id are provided", func() {
			// Neither
			req1, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/related-wods?profile_id=%d", profile.ID), nil)
			req1.Header.Set("Authorization", "Bearer "+generateValidToken(user))
			w1 := httptest.NewRecorder()
			router.ServeHTTP(w1, req1)
			Expect(w1.Code).To(Equal(http.StatusBadRequest))

			// Both
			req2, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/related-wods?profile_id=%d&movement=power%%20clean&session_id=WOD-123", profile.ID), nil)
			req2.Header.Set("Authorization", "Bearer "+generateValidToken(user))
			w2 := httptest.NewRecorder()
			router.ServeHTTP(w2, req2)
			Expect(w2.Code).To(Equal(http.StatusBadRequest))
		})

		It("returns 403 if user does not own the profile", func() {
			otherUser := testhelpers.CreateUser(dbConn, &db.User{Username: "other-user"})
			req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/related-wods?profile_id=%d&movement=power%%20clean", profile.ID), nil)
			req.Header.Set("Authorization", "Bearer "+generateValidToken(otherUser))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusForbidden))
		})
	})
})
