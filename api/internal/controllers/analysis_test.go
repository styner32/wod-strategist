package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
)

var _ = Describe("POST /api/v1/chunk-complete", func() {
	var (
		router      *gin.Engine
		queueClient *asynq.Client
		profile     db.Profile
		user        db.User
		sessionID   string
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)

		queueClient = testhelpers.NewQueueClient()
		router = newTestRouterWithAuthService(controllers.Config{
			QueueClient:          queueClient,
			NewChunkAnalysisTask: worker.NewChunkAnalysisTask,
		})

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		err := dbConn.Where("id = ?", profile.UserID).First(&user).Error
		Expect(err).To(BeNil())

		sessionID = "session-1"
	})

	It("enqueues a chunk analysis task and returns 202", func() {
		body := fmt.Sprintf(`{
			"session_id": "%s",
			"gcs_uri": "gs://bucket/videos/%d/%s/chunk_0001.mp4",
			"movements": ["Snatch"],
			"injuries": ["Left Knee"],
			"workout_type": "wod",
			"profile_id": %d,
			"start_secs": 0.0,
			"end_secs": 10.0,
			"heart_rate_bpm": 150,
			"workout_confidence": 0.95
		}`, sessionID, profile.ID, sessionID, profile.ID)

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/chunk-complete", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusAccepted))

		respBody := decodeMapBody(w)
		Expect(respBody["message"]).To(Equal("Chunk analysis started"))
		Expect(respBody["task_id"]).NotTo(BeEmpty())
		Expect(respBody["session_id"]).To(Equal(sessionID))

		// Verify enqueued task payload via Redis inspector.
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())

		var matched []asynq.TaskInfo
		for _, t := range pending {
			if t.Type != worker.TypeChunkAnalysis {
				continue
			}
			var p worker.VideoAnalysisPayload
			if json.Unmarshal(t.Payload, &p) == nil && p.SessionID == sessionID {
				matched = append(matched, *t)
			}
		}
		Expect(matched).To(HaveLen(1))

		var payload worker.VideoAnalysisPayload
		Expect(json.Unmarshal(matched[0].Payload, &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal(sessionID))
		Expect(payload.Movements).To(ConsistOf("Snatch"))
		Expect(payload.Injuries).To(ConsistOf("Left Knee"))
		Expect(payload.WorkoutType).To(Equal(worker.WorkoutTypeWOD))
		Expect(payload.ProfileID).To(Equal(profile.ID))
		Expect(payload.StartSecs).To(Equal(0.0))
		Expect(payload.EndSecs).To(Equal(10.0))
		Expect(payload.HeartRateBPM).To(Equal(150))
		Expect(payload.WorkoutConfidence).To(Equal(0.95))
	})

	It("accepts the deployed mobile payload without newer analysis fields", func() {
		body := fmt.Sprintf(`{
			"session_id": "%s",
			"gcs_uri": "gs://bucket/videos/%d/%s/chunk_0001.mp4",
			"profile_id": %d,
			"start_secs": 0.0,
			"end_secs": 10.0
		}`, sessionID, profile.ID, sessionID, profile.ID)

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/chunk-complete", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusAccepted), w.Body.String())
		respBody := decodeMapBody(w)
		Expect(respBody["message"]).To(Equal("Chunk analysis started"))
		Expect(respBody["task_id"]).To(BeAssignableToTypeOf(""))
		Expect(respBody["task_id"]).NotTo(BeEmpty())
		Expect(respBody["session_id"]).To(Equal(sessionID))

		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal(worker.TypeChunkAnalysis))

		var payload worker.VideoAnalysisPayload
		Expect(json.Unmarshal(pending[0].Payload, &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal(sessionID))
		Expect(payload.ProfileID).To(Equal(profile.ID))
		Expect(payload.StartSecs).To(Equal(0.0))
		Expect(payload.EndSecs).To(Equal(10.0))
		Expect(payload.WorkoutType).To(Equal(worker.WorkoutTypeWOD))
		Expect(payload.Movements).To(BeEmpty())
		Expect(payload.Injuries).To(BeEmpty())
		Expect(payload.HeartRateBPM).To(BeZero())
		Expect(payload.WorkoutConfidence).To(BeZero())
	})

	It("configures wod type from body", func() {
		body := fmt.Sprintf(`{
			"session_id": "session-1",
			"gcs_uri": "gs://bucket/videos/%d/session-1/chunk_0001.mp4",
			"movements": ["Back Squat"],
			"injuries": [],
			"workout_type": "accessory",
			"profile_id": %d,
			"start_secs": 0.0,
			"end_secs": 10.0,
			"heart_rate_bpm": 150,
			"workout_confidence": 0.95
		}`, profile.ID, profile.ID)

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/chunk-complete", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusAccepted))

		respBody := decodeMapBody(w)
		Expect(respBody["message"]).To(Equal("Chunk analysis started"))
		Expect(respBody["task_id"]).NotTo(BeEmpty())
		Expect(respBody["session_id"]).To(Equal("session-1"))

		// Verify enqueued task payload via Redis inspector.
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())

		var matched []asynq.TaskInfo
		for _, t := range pending {
			if t.Type != worker.TypeChunkAnalysis {
				continue
			}
			var p worker.VideoAnalysisPayload
			if json.Unmarshal(t.Payload, &p) == nil && p.SessionID == "session-1" {
				matched = append(matched, *t)
			}
		}
		Expect(matched).To(HaveLen(1))

		var payload worker.VideoAnalysisPayload
		Expect(json.Unmarshal(matched[0].Payload, &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.Movements).To(ConsistOf("Back Squat"))
		Expect(payload.Injuries).To(HaveLen(0))
		Expect(payload.WorkoutType).To(Equal(worker.WorkoutTypeAccessory))
		Expect(payload.ProfileID).To(Equal(profile.ID))
		Expect(payload.StartSecs).To(Equal(0.0))
		Expect(payload.EndSecs).To(Equal(10.0))
		Expect(payload.HeartRateBPM).To(Equal(150))
		Expect(payload.WorkoutConfidence).To(Equal(0.95))
	})

	It("enqueues a task with session if exists", func() {
		testhelpers.CreateSession(dbConn, &db.Session{
			ProfileID:      profile.ID,
			SessionID:      sessionID,
			WODDescription: "5 Rounds of 5 Back Squat",
		})

		injury := "Left Knee"
		profile.Injuries = &injury
		Expect(dbConn.Save(profile).Error).To(BeNil())

		gcsUri := fmt.Sprintf("gs://bucket/videos/%d/%s/chunk_0001.mp4", profile.ID, sessionID)

		body := fmt.Sprintf(`{
			"session_id": "%s",
			"gcs_uri": "%s",
			"movements": ["Back Squat"],
			"workout_type": "wod",
			"profile_id": %d,
			"start_secs": 0.0,
			"end_secs": 10.0,
			"heart_rate_bpm": 150,
			"workout_confidence": 0.95
		}`, sessionID, gcsUri, profile.ID)

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/chunk-complete", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusAccepted))

		respBody := decodeMapBody(w)
		Expect(respBody["message"]).To(Equal("Chunk analysis started"))
		Expect(respBody["task_id"]).NotTo(BeEmpty())
		Expect(respBody["session_id"]).To(Equal(sessionID))

		// Verify enqueued task payload via Redis inspector.
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())

		var matched []asynq.TaskInfo
		for _, t := range pending {
			if t.Type != worker.TypeChunkAnalysisWithSession {
				continue
			}
			var p worker.VideoAnalysisWithSessionPayload
			if json.Unmarshal(t.Payload, &p) == nil && p.SessionID == sessionID {
				matched = append(matched, *t)
			}
		}
		Expect(matched).To(HaveLen(1))

		var payload worker.VideoAnalysisWithSessionPayload
		Expect(json.Unmarshal(matched[0].Payload, &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal(sessionID))
		Expect(payload.FilePath).To(Equal(gcsUri))
		Expect(payload.ProfileID).To(Equal(profile.ID))
		Expect(payload.StartSecs).To(Equal(0.0))
		Expect(payload.EndSecs).To(Equal(10.0))
		Expect(payload.HeartRateBPM).To(Equal(150))
		Expect(payload.WorkoutConfidence).To(Equal(0.95))

		var updatedSession db.Session
		Expect(dbConn.Where("session_id = ?", sessionID).First(&updatedSession).Error).NotTo(HaveOccurred())
		Expect(string(updatedSession.MovementHints)).To(MatchJSON(`["Back Squat"]`))
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

	It("returns internal error when enqueue fails", func() {
		failRouter := newTestRouterWithAuthService(controllers.Config{
			QueueClient:          newBrokenQueueClient(),
			NewChunkAnalysisTask: worker.NewChunkAnalysisTask,
		})

		body := fmt.Sprintf(`{
			"session_id": "session-1",
			"gcs_uri": "gs://test-bucket-name/videos/1/session-1/video.mp4",
			"profile_id": %d,
			"start_secs": 0,
			"end_secs": 10
		}`, profile.ID)

		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/chunk-complete", body, &user)
		w := httptest.NewRecorder()
		failRouter.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusInternalServerError))
		Expect(decodeMapBody(w)["error"]).To(Equal("failed to enqueue task"))
	})
})
