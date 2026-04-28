package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("POST /api/v1/debug/telemetry", func() {
	It("stores telemetry JSON in GCS and returns gcs_uri", func() {
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())

		// Mock GCS upload for the telemetry JSON
		transport.New("https://storage.googleapis.com").
			Post(gcsUploadURL("test-bucket", "debug/telemetry/42/session-tel-1.json")).
			Reply(http.StatusOK).
			JSON(map[string]any{"name": "debug/telemetry/42/session-tel-1.json"})

		router := newTestRouter(Config{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})

		body := `{
			"sessionId": "session-tel-1",
			"profileId": 42,
			"startedAt": 1714303200000,
			"endedAt": 1714303800000,
			"samples": [
				{"ts": 0.0, "hr": 142, "batt": 0.78, "chunkIdx": 0},
				{"ts": 1.0, "hr": 145, "batt": 0.77, "chunkIdx": 0}
			],
			"appVersion": "1.4.2",
			"platform": "ios",
			"deviceModel": "iPhone15,3"
		}`

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		result := decodeMapBody(w)
		Expect(result["ok"]).To(BeTrue())
		Expect(result["gcs_uri"]).To(Equal("gs://test-bucket/debug/telemetry/42/session-tel-1.json"))
	})

	It("returns bad request for missing sessionId", func() {
		router := newTestRouter(Config{})

		body := `{"profileId": 42, "samples": []}`
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(Equal("sessionId is required"))
	})

	It("returns bad request for missing profileId", func() {
		router := newTestRouter(Config{})

		body := `{"sessionId": "s1", "samples": []}`
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(Equal("profileId is required"))
	})

	It("returns bad request for oversized samples", func() {
		router := newTestRouter(Config{})

		// Build a body with 7201 samples (exceeds max of 7200)
		var sampleEntries []string
		for i := 0; i <= maxTelemetrySamples; i++ {
			sampleEntries = append(sampleEntries, `{"ts": 0}`)
		}
		body := `{"sessionId": "s1", "profileId": 1, "samples": [` + strings.Join(sampleEntries, ",") + `]}`

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(ContainSubstring("too many samples"))
	})

	It("returns bad request for malformed JSON", func() {
		router := newTestRouter(Config{})

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", `{broken`)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeMapBody(w)["error"]).To(Equal("invalid request body"))
	})

	It("returns internal error when storage client is not configured", func() {
		router := newTestRouter(Config{}) // no storage client

		body := `{"sessionId": "s1", "profileId": 1, "samples": [{"ts": 0}]}`
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusInternalServerError))
		Expect(decodeMapBody(w)["error"]).To(Equal("storage not configured"))
	})

	It("returns internal error when GCS upload fails", func() {
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())

		// Mock GCS upload failure
		transport.New("https://storage.googleapis.com").
			Post(gcsUploadURL("test-bucket", "debug/telemetry/1/s1.json")).
			Reply(http.StatusInternalServerError).
			JSON(map[string]any{"error": map[string]any{"message": "boom"}})

		router := newTestRouter(Config{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})

		body := `{"sessionId": "s1", "profileId": 1, "samples": [{"ts": 0}]}`
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/debug/telemetry", body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusInternalServerError))
		Expect(decodeMapBody(w)["error"]).To(Equal("failed to upload telemetry"))
	})
})
