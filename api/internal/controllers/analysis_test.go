package controllers_test

import (
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

var _ = Describe("ChunkComplete", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
	)

	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)

		router = newTestRouterWithAuthService(controllers.Config{})

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		err := dbConn.Where("id = ?", profile.UserID).First(&user).Error
		Expect(err).To(BeNil())
	})

	DescribeTable("invalid arguments", func(body string, expectedStatusCode int, expectedErrorMessage string) {
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/chunk-complete", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(expectedStatusCode))
		Expect(decodeMapBody(w)["error"]).To(Equal(expectedErrorMessage))
	},
		Entry("malformed json", `{"session_id":`, http.StatusBadRequest, "invalid request body"),
		Entry("missing sesssion id", fmt.Sprintf(`{"gcs_uri":"gs://test-bucket-name/videos/1/session-1/video.mp4","profile_id":%d}`, uint(1)), http.StatusBadRequest, "session_id is required"),
		Entry("gcs_uri is missing", fmt.Sprintf(`{"session_id":"session-1","profile_id":%d}`, uint(1)), http.StatusBadRequest, "gcs_uri is required"),
		Entry("profile_id is missing", `{"session_id":"session-1","gcs_uri":"gs://test-bucket-name/videos/1/session-1/video.mp4"}`, http.StatusBadRequest, "profile_id is required"),
		Entry("invalid GCS URI", fmt.Sprintf(`{"session_id":"session-1","gcs_uri":"https://test-bucket-name/videos/0/session-1/video.mp4/video.mp4","profile_id":%d}`, uint(1)), http.StatusBadRequest, "invalid GCS URI"),
	)

	It("returns forbidden when profile does not belong to current user", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		otherUser := db.User{}
		err := dbConn.Where("id = ?", otherProfile.UserID).First(&otherUser).Error
		Expect(err).To(BeNil())

		// otherUser tries to use profile (owned by user)
		body := fmt.Sprintf(`{"session_id":"session-1","gcs_uri":"gs://test-bucket-name/videos/1/session-1/video.mp4","profile_id":%d}`, profile.ID)
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/chunk-complete", body, &otherUser)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusForbidden))
	})
})
