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
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("POST /api/v1/profiles", func() {
	var (
		router *gin.Engine
		user   db.User
	)

	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)
		router = newTestRouterWithAuthService(controllers.Config{})
		user = testhelpers.CreateUser(dbConn, &db.User{Username: "profile-create-user"})
	})

	It("creates a profile with optional resting_hr_bpm and rejects out-of-range values", func() {
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/profiles", `{"name":"Athlete","birth_year":1992,"resting_hr_bpm":58,"fitness_level":"intermediate"}`, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusCreated))

		var created controllers.ProfileResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &created)).To(Succeed())
		Expect(created.RestingHRBPM).To(HaveValue(Equal(58)))
		Expect(created.SensorCalibrationVersion).To(BeNil())

		badReq := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/profiles", `{"name":"InvalidHR","resting_hr_bpm":20}`, &user)
		badW := httptest.NewRecorder()
		router.ServeHTTP(badW, badReq)
		Expect(badW.Code).To(Equal(http.StatusBadRequest))
	})
})

var _ = Describe("PUT /api/v1/profiles/:id", func() {
	var (
		router  *gin.Engine
		user    db.User
		profile db.Profile
	)

	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)
		router = newTestRouterWithAuthService(controllers.Config{})

		resting := 62
		restG := 0.012
		refG := 0.32
		calibVer := "personal_chest_v1"
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{
			Name:                     "Runner",
			RestingHRBPM:             &resting,
			SensorENMORestG:          &restG,
			SensorENMORefG:           &refG,
			SensorCalibrationVersion: &calibVer,
		})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
	})

	It("updates and clears resting_hr_bpm while preserving worker-promoted sensor calibration columns", func() {
		updateReq := newAuthorizedJSONRequest(http.MethodPut, fmt.Sprintf("/api/v1/profiles/%d", profile.ID), `{"resting_hr_bpm":54}`, &user)
		updateW := httptest.NewRecorder()
		router.ServeHTTP(updateW, updateReq)
		Expect(updateW.Code).To(Equal(http.StatusOK))

		var updated controllers.ProfileResponse
		Expect(json.Unmarshal(updateW.Body.Bytes(), &updated)).To(Succeed())
		Expect(updated.RestingHRBPM).To(HaveValue(Equal(54)))
		Expect(updated.SensorCalibrationVersion).To(HaveValue(Equal("personal_chest_v1")))

		clearReq := newAuthorizedJSONRequest(http.MethodPut, fmt.Sprintf("/api/v1/profiles/%d", profile.ID), `{"clear_resting_hr":true}`, &user)
		clearW := httptest.NewRecorder()
		router.ServeHTTP(clearW, clearReq)
		Expect(clearW.Code).To(Equal(http.StatusOK))

		var cleared controllers.ProfileResponse
		Expect(json.Unmarshal(clearW.Body.Bytes(), &cleared)).To(Succeed())
		Expect(cleared.RestingHRBPM).To(BeNil())
		Expect(cleared.SensorCalibrationVersion).To(HaveValue(Equal("personal_chest_v1")))

		var dbRow db.Profile
		Expect(dbConn.First(&dbRow, profile.ID).Error).NotTo(HaveOccurred())
		Expect(dbRow.RestingHRBPM).To(BeNil())
		Expect(dbRow.SensorENMORestG).To(HaveValue(Equal(0.012)))
		Expect(dbRow.SensorENMORefG).To(HaveValue(Equal(0.32)))
		Expect(dbRow.SensorCalibrationVersion).To(HaveValue(Equal("personal_chest_v1")))
	})
})
