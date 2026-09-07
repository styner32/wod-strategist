package controllers_test

import (
	"bytes"
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

var _ = Describe("Sensor Handlers", func() {
	var (
		router   *gin.Engine
		user     db.User
		profile  db.Profile
		user2    db.User
		profile2 db.Profile
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)
		router = newTestRouterWithAuthService(controllers.Config{})

		birthYear1 := 1990
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{
			BirthYear: &birthYear1,
		})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())

		birthYear2 := 1995
		profile2 = testhelpers.CreateProfile(dbConn, &db.Profile{
			BirthYear: &birthYear2,
		})
		Expect(dbConn.First(&user2, profile2.UserID).Error).NotTo(HaveOccurred())
	})

	Context("CreateUploadURL protection", func() {
		It("rejects filenames starting with sensor_telemetry_v", func() {
			body, _ := json.Marshal(map[string]any{
				"session_id": "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF",
				"filename":   "sensor_telemetry_v1_someuuid.ndjson",
				"profile_id": profile.ID,
			})
			req := httptest.NewRequest("POST", "/api/v1/upload-url", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			authorizeRequest(req, &user)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(w.Body.String()).To(ContainSubstring("reserved filename prefix"))
		})
	})

	Context("POST /api/v1/sessions/:session_id/sensor-upload", func() {
		const validSessionID = "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		const reqUUID = "a0000000-0000-0000-0000-000000000001"
		const sha256Hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

		It("successfully prepares sensor upload for a new session", func() {
			body, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       reqUUID,
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          sha256Hash,
			})
			req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			authorizeRequest(req, &user)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))

			var resp controllers.PrepareSensorUploadResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.RequestID).To(Equal(reqUUID))
			Expect(resp.Version).To(Equal("1"))
			Expect(resp.State).To(Equal(db.SensorStateUploading))
			Expect(resp.UploadURL).NotTo(BeEmpty())
			Expect(resp.ObjectName).To(ContainSubstring(fmt.Sprintf("videos/%d/%s/sensor_telemetry_v1_%s.ndjson", profile.ID, validSessionID, reqUUID)))
			Expect(resp.RequiredHeaders["Content-Type"]).To(Equal("application/x-ndjson"))
			Expect(resp.RequiredHeaders["x-goog-if-generation-match"]).To(Equal("0"))
			Expect(resp.RequiredHeaders["x-goog-meta-sha256"]).To(Equal(sha256Hash))

			// Verify analysis_results row was created with minimal PENDING video status
			var row db.AnalysisResult
			Expect(dbConn.Where("session_id = ?", validSessionID).First(&row).Error).NotTo(HaveOccurred())
			Expect(row.ProfileID).To(Equal(profile.ID))
			Expect(row.Status).To(Equal("PENDING"))
			Expect(row.SensorState).To(Equal(db.SensorStateUploading))
			Expect(row.SensorVersion).To(Equal(int64(1)))
			Expect(row.WorkoutAt).NotTo(BeNil())
			Expect(row.WorkoutAtSource).NotTo(BeNil())
			Expect(*row.WorkoutAtSource).To(Equal("session_ulid"))
		})

		It("returns identical response on idempotent retry of identical request", func() {
			body, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       reqUUID,
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          sha256Hash,
			})
			req1 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body))
			req1.Header.Set("Content-Type", "application/json")
			authorizeRequest(req1, &user)

			w1 := httptest.NewRecorder()
			router.ServeHTTP(w1, req1)
			Expect(w1.Code).To(Equal(http.StatusOK))

			req2 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body))
			req2.Header.Set("Content-Type", "application/json")
			authorizeRequest(req2, &user)

			w2 := httptest.NewRecorder()
			router.ServeHTTP(w2, req2)
			Expect(w2.Code).To(Equal(http.StatusOK))

			var resp1, resp2 controllers.PrepareSensorUploadResponse
			Expect(json.Unmarshal(w1.Body.Bytes(), &resp1)).To(Succeed())
			Expect(json.Unmarshal(w2.Body.Bytes(), &resp2)).To(Succeed())
			Expect(resp2.Version).To(Equal(resp1.Version))
			Expect(resp2.RequestID).To(Equal(resp1.RequestID))
		})

		It("rejects conflicting payload for the same request ID", func() {
			body1, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       reqUUID,
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          sha256Hash,
			})
			req1 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body1))
			req1.Header.Set("Content-Type", "application/json")
			authorizeRequest(req1, &user)
			w1 := httptest.NewRecorder()
			router.ServeHTTP(w1, req1)
			Expect(w1.Code).To(Equal(http.StatusOK))

			// Different size
			body2, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       reqUUID,
				ExpectedVersion: "0",
				SizeBytes:       2048,
				SHA256:          sha256Hash,
			})
			req2 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body2))
			req2.Header.Set("Content-Type", "application/json")
			authorizeRequest(req2, &user)
			w2 := httptest.NewRecorder()
			router.ServeHTTP(w2, req2)
			Expect(w2.Code).To(Equal(http.StatusConflict))
			Expect(w2.Body.String()).To(ContainSubstring("REQUEST_CONTENT_CONFLICT"))
		})

		It("rejects version conflict when expected_version does not match current sensor_version", func() {
			body1, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       reqUUID,
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          sha256Hash,
			})
			req1 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body1))
			req1.Header.Set("Content-Type", "application/json")
			authorizeRequest(req1, &user)
			w1 := httptest.NewRecorder()
			router.ServeHTTP(w1, req1)
			Expect(w1.Code).To(Equal(http.StatusOK))

			// Attempt another prepare with wrong expected_version "0" (current is 1)
			body2, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       "b0000000-0000-0000-0000-000000000002",
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          sha256Hash,
			})
			req2 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body2))
			req2.Header.Set("Content-Type", "application/json")
			authorizeRequest(req2, &user)
			w2 := httptest.NewRecorder()
			router.ServeHTTP(w2, req2)
			Expect(w2.Code).To(Equal(http.StatusConflict))
			Expect(w2.Body.String()).To(ContainSubstring("SENSOR_VERSION_CONFLICT"))
		})

		It("rejects access from user not owning the profile (403)", func() {
			body, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       reqUUID,
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          sha256Hash,
			})
			req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			// user2 tries to act as profile (owned by user1)
			authorizeRequest(req, &user2)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusForbidden))
		})

		It("rejects session owned by another profile (409)", func() {
			// Pre-create session owned by profile2
			testhelpers.CreateSession(dbConn, &db.Session{
				SessionID: validSessionID,
				ProfileID: profile2.ID,
			})

			// user1 tries to prepare for validSessionID with profile
			body, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       reqUUID,
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          sha256Hash,
			})
			req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			authorizeRequest(req, &user)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusConflict))
			Expect(w.Body.String()).To(ContainSubstring("OWNERSHIP_CONFLICT"))
		})
	})

	Context("GET /api/v1/sessions/:session_id/sensor-status", func() {
		const validSessionID = "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"

		It("returns NONE for a session with no sensor record", func() {
			req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/sensor-status?profile_id=%d", validSessionID, profile.ID), nil)
			authorizeRequest(req, &user)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))

			var resp controllers.SensorStatusResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.State).To(Equal(db.SensorStateNone))
			Expect(resp.Version).To(Equal("0"))
		})
	})

	Context("POST /api/v1/sessions/:session_id/sensor-complete", func() {
		const validSessionID = "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		const reqUUID = "a0000000-0000-0000-0000-000000000001"
		const sha256Hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

		It("returns 404 UPLOAD_NOT_FOUND when object does not exist in storage", func() {
			// First prepare
			body, _ := json.Marshal(controllers.PrepareSensorUploadRequest{
				ProfileID:       profile.ID,
				RequestID:       reqUUID,
				ExpectedVersion: "0",
				SizeBytes:       1024,
				SHA256:          sha256Hash,
			})
			req1 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-upload", validSessionID), bytes.NewReader(body))
			req1.Header.Set("Content-Type", "application/json")
			authorizeRequest(req1, &user)
			w1 := httptest.NewRecorder()
			router.ServeHTTP(w1, req1)
			Expect(w1.Code).To(Equal(http.StatusOK))

			// Now complete without uploading
			completeBody, _ := json.Marshal(controllers.CompleteSensorUploadRequest{
				ProfileID: profile.ID,
				RequestID: reqUUID,
				Version:   "1",
			})
			req2 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/sessions/%s/sensor-complete", validSessionID), bytes.NewReader(completeBody))
			req2.Header.Set("Content-Type", "application/json")
			authorizeRequest(req2, &user)
			w2 := httptest.NewRecorder()
			router.ServeHTTP(w2, req2)
			Expect(w2.Code).To(Equal(http.StatusNotFound))
			Expect(w2.Body.String()).To(ContainSubstring("UPLOAD_NOT_FOUND"))
		})
	})
})
