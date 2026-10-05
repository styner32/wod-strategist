package controllers_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("DELETE /api/v1/sessions/:session_id/environment", func() {
	It("requires ownership and deletes only observation assets, idempotently", func() {
		testhelpers.CleanupDB(dbConn)
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
		transport := testhelpers.NewMockTransport()
		client, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).To(Succeed())
		router := newTestRouterWithAuthService(controllers.Config{StorageClient: client, BucketName: "test-bucket"})
		sid := "WOD-20260921-01ARZ3NDEKTSV4RRFFQ69G5FAV"
		path := fmt.Sprintf("/api/v1/sessions/%s/environment?profile_id=%d", sid, profile.ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, path, nil))
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		other := testhelpers.CreateUser(dbConn, &db.User{})
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodDelete, path, "", &other))
		Expect(response.Code).To(Equal(http.StatusForbidden))
		prefix := fmt.Sprintf("videos/%d/%s/environment_", profile.ID, sid)
		object := prefix + "evidence.json"
		testhelpers.MockGCSListObjects(transport, "test-bucket", prefix, []string{object, fmt.Sprintf("videos/%d/%s/video.mp4", profile.ID, sid)})
		transport.New("https://storage.googleapis.com").Delete("/storage/v1/b/test-bucket/o/" + url.PathEscape(object) + "?alt=json&prettyPrint=false").Reply(http.StatusNoContent)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodDelete, path, "", &user))
		Expect(response.Code).To(Equal(http.StatusNoContent))
		testhelpers.MockGCSListObjects(transport, "test-bucket", prefix, nil)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodDelete, path, "", &user))
		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(transport.Verify()).To(Succeed())
	})
})
