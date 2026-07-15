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
	"github.com/wod-strategist/api/internal/worker"
)

var _ = Describe("POST /api/v1/sessions/:session_id/chunks/:chunk_id/reanalyses", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
		session db.Session
		chunk   db.ChunkAnalysisResult
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:   "WOD-20260715-01JREANALYSISAPI0000000",
			ProfileID:   profile.ID,
			WorkoutType: "wod",
		})
		start, end := 12.25, 19.75
		chunk = testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:       session.SessionID,
			ProfileID:       profile.ID,
			Status:          "COMPLETED",
			ExerciseType:    "Rope Climb",
			Output:          "original coaching",
			ObservedSignals: `{"movement":"Rope Climb"}`,
			MediaStartSecs:  &start,
			MediaEndSecs:    &end,
		})
		router = newTestRouterWithAuthService(controllers.Config{
			QueueClient:           testhelpers.NewQueueClient(),
			EnableChunkReanalysis: true,
		})
	})

	requestPath := func() string {
		return fmt.Sprintf("/api/v1/sessions/%s/chunks/%d/reanalyses", session.SessionID, chunk.ID)
	}

	It("requires authentication and exact chunk ownership", func() {
		unauthenticated := newAuthorizedJSONRequest(http.MethodPost, requestPath(), `{"client_request_id":"unauthenticated"}`)
		unauthenticatedResponse := httptest.NewRecorder()
		router.ServeHTTP(unauthenticatedResponse, unauthenticated)
		Expect(unauthenticatedResponse.Code).To(Equal(http.StatusUnauthorized))

		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, otherProfile.UserID).Error).NotTo(HaveOccurred())
		wrongOwner := newAuthorizedJSONRequest(http.MethodPost, requestPath(), `{"client_request_id":"wrong-owner"}`, &otherUser)
		wrongOwnerResponse := httptest.NewRecorder()
		router.ServeHTTP(wrongOwnerResponse, wrongOwner)
		Expect(wrongOwnerResponse.Code).To(Equal(http.StatusNotFound))

		var count int64
		Expect(dbConn.Model(&db.ChunkReanalysisRun{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("rejects every caller-controlled field except client_request_id", func() {
		req := newAuthorizedJSONRequest(http.MethodPost, requestPath(), `{"client_request_id":"strict-1","model":"attacker-model","gcs_uri":"gs://attacker/video.mp4"}`, &user)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)

		Expect(response.Code).To(Equal(http.StatusBadRequest))
		Expect(response.Body.String()).To(ContainSubstring("only client_request_id is allowed"))
		var count int64
		Expect(dbConn.Model(&db.ChunkReanalysisRun{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("returns the same run and queues only one task for an idempotent retry", func() {
		body := `{"client_request_id":"idempotent-1"}`
		first := httptest.NewRecorder()
		router.ServeHTTP(first, newAuthorizedJSONRequest(http.MethodPost, requestPath(), body, &user))
		Expect(first.Code).To(Equal(http.StatusAccepted), first.Body.String())
		var firstResponse controllers.CreateChunkReanalysisResponse
		Expect(json.Unmarshal(first.Body.Bytes(), &firstResponse)).To(Succeed())
		Expect(firstResponse.Status).To(Equal(db.ChunkReanalysisStatusQueued))
		Expect(firstResponse.RunID).NotTo(BeZero())
		Expect(firstResponse.TaskID).NotTo(BeEmpty())

		second := httptest.NewRecorder()
		router.ServeHTTP(second, newAuthorizedJSONRequest(http.MethodPost, requestPath(), body, &user))
		Expect(second.Code).To(Equal(http.StatusAccepted), second.Body.String())
		var secondResponse controllers.CreateChunkReanalysisResponse
		Expect(json.Unmarshal(second.Body.Bytes(), &secondResponse)).To(Succeed())
		Expect(secondResponse).To(Equal(firstResponse))

		var count int64
		Expect(dbConn.Model(&db.ChunkReanalysisRun{}).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal(worker.TypeChunkDebugReanalysis))
	})

	It("rejects a second client request while the chunk has an active run", func() {
		testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
			SessionID:             session.SessionID,
			ProfileID:             profile.ID,
			ChunkAnalysisResultID: chunk.ID,
			ClientRequestID:       "already-active",
			Status:                db.ChunkReanalysisStatusRunning,
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost, requestPath(), `{"client_request_id":"second-request"}`, &user,
		))

		Expect(response.Code).To(Equal(http.StatusConflict))
		Expect(response.Body.String()).To(ContainSubstring("already active"))
	})

	It("enforces the rolling per-profile quota before enqueueing", func() {
		for i := 0; i < 20; i++ {
			testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
				SessionID:             session.SessionID,
				ProfileID:             profile.ID,
				ChunkAnalysisResultID: chunk.ID,
				ClientRequestID:       fmt.Sprintf("completed-%02d", i),
				Status:                db.ChunkReanalysisStatusCompleted,
			})
		}

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost, requestPath(), `{"client_request_id":"over-quota"}`, &user,
		))

		Expect(response.Code).To(Equal(http.StatusTooManyRequests))
		Expect(response.Body.String()).To(ContainSubstring("daily re-analysis limit reached"))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})
})
