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

var _ = Describe("GET /api/v1/video-download/:session_id", func() {
	const sessionID = "WOD-20260716-01JTARGETVIDEO00000000"

	var (
		router    *gin.Engine
		profile   db.Profile
		user      db.User
		transport *testhelpers.MockTransport
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())

		transport = testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClientWithSigning("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		router = newTestRouterWithAuthService(controllers.Config{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})

		dbConn.Create(&db.Session{
			SessionID:   sessionID,
			ProfileID:   profile.ID,
			WorkoutType: "wod",
		})
	})

	It("resolves the exact canonical target with one GCS lookup", func() {
		canonicalObject := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, sessionID)
		testhelpers.MockGCSListObjects(transport, "test-bucket", canonicalObject, []string{
			canonicalObject,
			canonicalObject + ".tmp",
		})

		req := newAuthorizedJSONRequest(
			http.MethodGet,
			fmt.Sprintf("/api/v1/video-download/%s?profile_id=%d&kind=merged", sessionID, profile.ID),
			"",
			&user,
		)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var response controllers.VideoDownloadURLResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.SessionID).To(Equal(sessionID))
		Expect(response.Kind).To(Equal("merged"))
		Expect(response.DownloadURL).NotTo(BeEmpty())
		Expect(transport.Requests()).To(HaveLen(1))
		Expect(transport.Verify()).To(Succeed())
	})

	It("checks legacy layouts only after the canonical target is absent", func() {
		canonicalObject := fmt.Sprintf("videos/%d/%s/hardsubbed.mp4", profile.ID, sessionID)
		canonicalPrefix := fmt.Sprintf("videos/%d/%s/", profile.ID, sessionID)
		legacyUploadPrefix := fmt.Sprintf("videos/0/%s/", sessionID)
		legacyObject := legacyUploadPrefix + "workout_hardsubbed_20260716.mp4"

		testhelpers.MockGCSListObjects(transport, "test-bucket", canonicalObject, nil)
		testhelpers.MockGCSListObjects(transport, "test-bucket", canonicalPrefix, nil)
		testhelpers.MockGCSListObjects(transport, "test-bucket", legacyUploadPrefix, []string{
			legacyObject,
		})

		req := newAuthorizedJSONRequest(
			http.MethodGet,
			fmt.Sprintf("/api/v1/video-download/%s?profile_id=%d&kind=hardsubbed", sessionID, profile.ID),
			"",
			&user,
		)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var response controllers.VideoDownloadURLResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Kind).To(Equal("hardsubbed"))
		Expect(response.DownloadURL).NotTo(BeEmpty())
		Expect(transport.Requests()).To(HaveLen(3))
		Expect(transport.Verify()).To(Succeed())
	})

	It("falls back to the legacy flat layout for encoded video", func() {
		canonicalObject := fmt.Sprintf("videos/%d/%s/encoded.mp4", profile.ID, sessionID)
		canonicalPrefix := fmt.Sprintf("videos/%d/%s/", profile.ID, sessionID)
		legacyUploadPrefix := fmt.Sprintf("videos/0/%s/", sessionID)
		legacyFlatPrefix := fmt.Sprintf("videos/%s_", sessionID)
		legacyObject := fmt.Sprintf("videos/%s_workout_encoded.mp4", sessionID)

		testhelpers.MockGCSListObjects(transport, "test-bucket", canonicalObject, nil)
		testhelpers.MockGCSListObjects(transport, "test-bucket", canonicalPrefix, []string{
			canonicalPrefix + "merged.mp4",
		})
		testhelpers.MockGCSListObjects(transport, "test-bucket", legacyUploadPrefix, nil)
		testhelpers.MockGCSListObjects(transport, "test-bucket", legacyFlatPrefix, []string{
			legacyObject,
		})

		req := newAuthorizedJSONRequest(
			http.MethodGet,
			fmt.Sprintf("/api/v1/video-download/%s?profile_id=%d&kind=encoded", sessionID, profile.ID),
			"",
			&user,
		)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var response controllers.VideoDownloadURLResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Kind).To(Equal("encoded"))
		Expect(response.DownloadURL).NotTo(BeEmpty())
		Expect(transport.Requests()).To(HaveLen(4))
		Expect(transport.Verify()).To(Succeed())
	})

	It("rejects similarly prefixed objects and other video kinds", func() {
		canonicalObject := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, sessionID)
		canonicalPrefix := fmt.Sprintf("videos/%d/%s/", profile.ID, sessionID)
		legacyUploadPrefix := fmt.Sprintf("videos/0/%s/", sessionID)
		legacyFlatPrefix := fmt.Sprintf("videos/%s_", sessionID)

		testhelpers.MockGCSListObjects(transport, "test-bucket", canonicalObject, []string{
			canonicalObject + ".tmp",
		})
		testhelpers.MockGCSListObjects(transport, "test-bucket", canonicalPrefix, []string{
			canonicalPrefix + "merged.mp4.tmp",
			canonicalPrefix + "hardsubbed.mp4",
		})
		testhelpers.MockGCSListObjects(transport, "test-bucket", legacyUploadPrefix, nil)
		testhelpers.MockGCSListObjects(transport, "test-bucket", legacyFlatPrefix, nil)

		req := newAuthorizedJSONRequest(
			http.MethodGet,
			fmt.Sprintf("/api/v1/video-download/%s?profile_id=%d&kind=merged", sessionID, profile.ID),
			"",
			&user,
		)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNotFound), w.Body.String())
		Expect(decodeMapBody(w)["error"]).To(Equal("no merged video found for session"))
		Expect(transport.Requests()).To(HaveLen(4))
		Expect(transport.Verify()).To(Succeed())
	})
})
