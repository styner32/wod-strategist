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

var _ = Describe("GET /api/v1/stretches/recommended", func() {
	var (
		router  *gin.Engine
		user    db.User
		profile db.Profile
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClientWithSigning("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())

		router = newTestRouterWithAuthService(controllers.Config{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
	})

	It("returns 401 when unauthorized", func() {
		req, err := http.NewRequest(http.MethodGet, "/api/v1/stretches/recommended?profile_id=1", nil)
		Expect(err).NotTo(HaveOccurred())

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusUnauthorized))
	})

	It("returns 400 when profile_id is missing or invalid", func() {
		reqMissing := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/stretches/recommended", "", &user)
		wMissing := httptest.NewRecorder()
		router.ServeHTTP(wMissing, reqMissing)
		Expect(wMissing.Code).To(Equal(http.StatusBadRequest))

		reqInvalid := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/stretches/recommended?profile_id=abc", "", &user)
		wInvalid := httptest.NewRecorder()
		router.ServeHTTP(wInvalid, reqInvalid)
		Expect(wInvalid.Code).To(Equal(http.StatusBadRequest))
	})

	It("returns 403 when accessing another user's profile", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d", otherProfile.ID), "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusForbidden))
	})

	It("returns empty array when no recommended stretches exist", func() {
		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d", profile.ID), "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(BeEmpty())
	})

	It("collapses the same stretch across multiple sessions into one entry with sessions newest first", func() {
		s := testhelpers.CreateStretch(dbConn, &db.Stretch{
			Name:        "Pigeon Pose",
			TargetArea:  "Hips & Glutes",
			Description: "Opens hips and relieves glute tightness",
		})

		t1 := time.Now().Add(-2 * time.Hour)
		t2 := time.Now().Add(-1 * time.Hour)
		t3 := time.Now()

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-20260401-001",
			StretchRecommendations: `[{"stretch":"Pigeon Pose","target_area":"Hips & Glutes","reason":"Tight hips from squats","duration_hint":"60s per side"}]`,
			CreatedAt:              t1,
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-20260402-002",
			StretchRecommendations: `[{"stretch":"Pigeon Pose","target_area":"Hips & Glutes","reason":"Glute fatigue from deadlifts"}]`,
			CreatedAt:              t2,
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-20260403-003",
			StretchRecommendations: `[{"stretch":"Pigeon Pose","target_area":"Hips & Glutes","reason":"Recovery after lunges"}]`,
			CreatedAt:              t3,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d", profile.ID), "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(HaveLen(1))

		item := resp[0]
		Expect(item.ID).To(Equal(s.ID))
		Expect(item.Name).To(Equal("Pigeon Pose"))
		Expect(item.InCatalog).To(BeTrue())
		Expect(item.SessionCount).To(Equal(3))
		Expect(item.Sessions).To(HaveLen(3))
		Expect(item.Sessions[0].SessionID).To(Equal("WOD-20260403-003"))
		Expect(item.Sessions[1].SessionID).To(Equal("WOD-20260402-002"))
		Expect(item.Sessions[2].SessionID).To(Equal("WOD-20260401-001"))
		Expect(item.LastRecommendedAt.Unix()).To(Equal(t3.Unix()))
	})

	It("collapses alias and canonical stretch into a single entry", func() {
		s := testhelpers.CreateStretch(dbConn, &db.Stretch{
			Name:        "Couch Stretch",
			TargetArea:  "Quadriceps & Hip Flexors",
			Description: "Deep hip flexor and quad stretch against a wall",
		})
		testhelpers.CreateStretchAlias(dbConn, &db.StretchAlias{
			StretchID: s.ID,
			Alias:     "Wall Quad Stretch",
		})

		t1 := time.Now().Add(-1 * time.Hour)
		t2 := time.Now()

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-20260401-001",
			StretchRecommendations: `[{"stretch":"Wall Quad Stretch","target_area":"Quads","reason":"Tight quads"}]`,
			CreatedAt:              t1,
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-20260402-002",
			StretchRecommendations: `[{"stretch":"Couch Stretch","target_area":"Hip Flexors","reason":"Hip extension restriction"}]`,
			CreatedAt:              t2,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d", profile.ID), "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(HaveLen(1))

		item := resp[0]
		Expect(item.ID).To(Equal(s.ID))
		Expect(item.Name).To(Equal("Couch Stretch"))
		Expect(item.NormalizedKey).To(Equal(db.NormalizeStretchKey("Couch Stretch")))
		Expect(item.InCatalog).To(BeTrue())
		Expect(item.SessionCount).To(Equal(2))
		Expect(item.Aliases).To(ConsistOf("Wall Quad Stretch"))
	})

	It("returns description and signed media URLs for matched catalog entries", func() {
		s := testhelpers.CreateStretch(dbConn, &db.Stretch{
			Name:        "Doorway Pec Stretch",
			TargetArea:  "Chest & Shoulders",
			Description: "Chest opening stretch using a doorway frame",
			ImageObject: "stretches/1/img.jpg",
			VideoObject: "stretches/1/video.mp4",
		})

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-20260401-001",
			StretchRecommendations: `[{"stretch":"Doorway Pec Stretch","target_area":"Chest","reason":"Chest tightness"}]`,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d", profile.ID), "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(HaveLen(1))

		item := resp[0]
		Expect(item.ID).To(Equal(s.ID))
		Expect(item.InCatalog).To(BeTrue())
		Expect(item.Description).To(Equal("Chest opening stretch using a doorway frame"))
		Expect(item.ImageURL).NotTo(BeEmpty())
		Expect(item.VideoURL).NotTo(BeEmpty())
	})

	It("returns in_catalog: false and id: 0 for recommendations not in the catalog", func() {
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-20260401-001",
			StretchRecommendations: `[{"stretch":"Custom Mystery Stretch","target_area":"Shoulders","reason":"Scapular mobility"}]`,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d", profile.ID), "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(HaveLen(1))

		item := resp[0]
		Expect(item.ID).To(Equal(uint64(0)))
		Expect(item.InCatalog).To(BeFalse())
		Expect(item.Name).To(Equal("Custom Mystery Stretch"))
		Expect(item.NormalizedKey).To(Equal("custom mystery stretch"))
		Expect(item.TargetArea).To(Equal("Shoulders"))
		Expect(item.ImageURL).To(BeEmpty())
		Expect(item.VideoURL).To(BeEmpty())
		Expect(item.SessionCount).To(Equal(1))
		Expect(item.Sessions[0].Reason).To(Equal("Scapular mobility"))
	})

	It("excludes archived analyses and orders by last_recommended_at descending", func() {
		tArchived := time.Now()
		tOld := time.Now().Add(-2 * time.Hour)
		tNew := time.Now().Add(-1 * time.Hour)

		archivedTime := time.Now()
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-ARCHIVED",
			StretchRecommendations: `[{"stretch":"Archived Stretch","target_area":"Back","reason":"Archived"}]`,
			ArchivedAt:              &archivedTime,
			CreatedAt:              tArchived,
		})

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-OLD",
			StretchRecommendations: `[{"stretch":"Older Stretch","target_area":"Hamstrings","reason":"Old"}]`,
			CreatedAt:              tOld,
		})

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-NEW",
			StretchRecommendations: `[{"stretch":"Newer Stretch","target_area":"Calves","reason":"New"}]`,
			CreatedAt:              tNew,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d", profile.ID), "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(HaveLen(2))

		Expect(resp[0].Name).To(Equal("Newer Stretch"))
		Expect(resp[1].Name).To(Equal("Older Stretch"))
	})

	It("filters by ?key= query param with canonical key and alias key", func() {
		s := testhelpers.CreateStretch(dbConn, &db.Stretch{
			Name:       "World's Greatest Stretch",
			TargetArea: "Full Body",
		})
		testhelpers.CreateStretchAlias(dbConn, &db.StretchAlias{
			StretchID: s.ID,
			Alias:     "WGS",
		})

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-1",
			StretchRecommendations: `[{"stretch":"World's Greatest Stretch","target_area":"Full Body","reason":"Thoracic mobility"}]`,
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			ProfileID:              profile.ID,
			SessionID:              "WOD-2",
			StretchRecommendations: `[{"stretch":"Child's Pose","target_area":"Lats","reason":"Lat recovery"}]`,
		})

		// Query with canonical key
		req1 := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d&key=world's+greatest+stretch", profile.ID), "", &user)
		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, req1)
		Expect(w1.Code).To(Equal(http.StatusOK))
		var resp1 []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w1.Body.Bytes(), &resp1)).To(Succeed())
		Expect(resp1).To(HaveLen(1))
		Expect(resp1[0].Name).To(Equal("World's Greatest Stretch"))

		// Query with alias key "wgs"
		req2 := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d&key=wgs", profile.ID), "", &user)
		w2 := httptest.NewRecorder()
		router.ServeHTTP(w2, req2)
		Expect(w2.Code).To(Equal(http.StatusOK))
		var resp2 []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w2.Body.Bytes(), &resp2)).To(Succeed())
		Expect(resp2).To(HaveLen(1))
		Expect(resp2[0].Name).To(Equal("World's Greatest Stretch"))

		// Query with non-matching key
		req3 := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/stretches/recommended?profile_id=%d&key=nonexistent", profile.ID), "", &user)
		w3 := httptest.NewRecorder()
		router.ServeHTTP(w3, req3)
		Expect(w3.Code).To(Equal(http.StatusOK))
		var resp3 []controllers.RecommendedStretchResponse
		Expect(json.Unmarshal(w3.Body.Bytes(), &resp3)).To(Succeed())
		Expect(resp3).To(BeEmpty())
	})
})
