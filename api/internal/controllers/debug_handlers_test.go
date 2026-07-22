package controllers_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("POST /api/v1/debug/telemetry", func() {
	var (
		profile db.Profile
		user    db.User
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		defaultTestUser = user
	})

	It("stores telemetry JSON in GCS and returns gcs_uri", func() {
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())

		objectName := fmt.Sprintf("debug/telemetry/%d/session-tel-1.json", profile.ID)
		transport.New("https://storage.googleapis.com").
			Post(gcsUploadURL("test-bucket", objectName)).
			Reply(http.StatusOK).
			JSON(map[string]any{"name": objectName})

		router := newTestRouter(controllers.Config{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})

		body := fmt.Sprintf(`{
			"sessionId": "session-tel-1",
			"profileId": %d,
			"startedAt": 1714303200000,
			"endedAt": 1714303800000,
			"samples": [
				{"ts": 0.0, "hr": 142, "batt": 0.78, "chunkIdx": 0},
				{"ts": 1.0, "hr": 145, "batt": 0.77, "chunkIdx": 0}
			],
			"appVersion": "1.4.2",
			"platform": "ios",
			"deviceModel": "iPhone15,3"
		}`, profile.ID)

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		result := decodeMapBody(w)
		Expect(result["ok"]).To(BeTrue())
		Expect(result["gcs_uri"]).To(Equal("gs://test-bucket/" + objectName))
	})

	It("returns bad request for missing sessionId", func() {
		router := newTestRouter(controllers.Config{})

		body := fmt.Sprintf(`{"profileId": %d, "samples": []}`, profile.ID)
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(Equal("sessionId is required"))
	})

	It("returns bad request for missing profileId", func() {
		router := newTestRouter(controllers.Config{})

		body := `{"sessionId": "s1", "samples": []}`
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(Equal("profileId is required"))
	})

	It("returns bad request for oversized samples", func() {
		router := newTestRouter(controllers.Config{})

		// Build a body with 7201 samples (exceeds max of 7200)
		var sampleEntries []string
		for i := 0; i <= 7200; i++ {
			sampleEntries = append(sampleEntries, `{"ts": 0}`)
		}
		body := fmt.Sprintf(`{"sessionId": "s1", "profileId": %d, "samples": [%s]}`, profile.ID, strings.Join(sampleEntries, ","))

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(ContainSubstring("too many samples"))
	})

	It("returns bad request for malformed JSON", func() {
		router := newTestRouter(controllers.Config{})

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", `{broken`, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(Equal("invalid request body"))
	})

	It("returns internal error when GCS upload fails", func() {
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())

		objectName := fmt.Sprintf("debug/telemetry/%d/s1.json", profile.ID)
		transport.New("https://storage.googleapis.com").
			Post(gcsUploadURL("test-bucket", objectName)).
			Reply(http.StatusInternalServerError).
			JSON(map[string]any{"error": map[string]any{"message": "boom"}})

		router := newTestRouter(controllers.Config{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})

		body := fmt.Sprintf(`{"sessionId": "s1", "profileId": %d, "samples": [{"ts": 0}]}`, profile.ID)
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusInternalServerError))
		Expect(decodeMapBody(w)["error"]).To(Equal("failed to upload telemetry"))
	})
})
