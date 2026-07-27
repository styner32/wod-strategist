package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
)

var _ = Describe("POST /api/v1/sessions/:session_id/reanalyses", func() {
	var (
		router    *gin.Engine
		profile   db.Profile
		user      db.User
		session   db.Session
		transport *testhelpers.MockTransport
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:      "WOD-20260716-01JSESSIONREANALYSIS00000",
			ProfileID:      profile.ID,
			WorkoutType:    "wod",
			WODDescription: "3 rounds of pull-ups and burpees",
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:    session.SessionID,
			ProfileID:    profile.ID,
			Status:       "COMPLETED",
			Output:       `{"coaching_feedback":"Original analysis"}`,
			SessionScore: `{"overall":82}`,
		})

		transport = testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		testhelpers.MockGCSListObjects(
			transport,
			"test-bucket",
			fmt.Sprintf("videos/%d/%s/", profile.ID, session.SessionID),
			[]string{fmt.Sprintf("videos/%d/%s/analysis.mp4", profile.ID, session.SessionID)},
		)

		router = newTestRouterWithAuthService(controllers.Config{
			QueueClient:             testhelpers.NewQueueClient(),
			StorageClient:           storageClient,
			BucketName:              "test-bucket",
			EnableSessionReanalysis: true,
		})
	})

	requestPath := func() string {
		return fmt.Sprintf("/api/v1/sessions/%s/reanalyses", session.SessionID)
	}

	It("requires authentication and exact session ownership", func() {
		unauthenticatedReq := httptest.NewRequest(http.MethodPost, requestPath(), strings.NewReader(`{"client_request_id":"unauthenticated"}`))
		unauthenticatedReq.Header.Set("Content-Type", "application/json")
		unauthenticated := httptest.NewRecorder()
		router.ServeHTTP(unauthenticated, unauthenticatedReq)
		Expect(unauthenticated.Code).To(Equal(http.StatusUnauthorized))

		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, otherProfile.UserID).Error).NotTo(HaveOccurred())
		wrongOwner := httptest.NewRecorder()
		router.ServeHTTP(wrongOwner, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"wrong-owner"}`,
			&otherUser,
		))
		Expect(wrongOwner.Code).To(Equal(http.StatusForbidden))

		var count int64
		Expect(dbConn.Model(&db.SessionReanalysisRun{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("accepts only valid fields in the request body", func() {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"strict-1","model":"attacker-model","gcs_uri":"gs://attacker/video.mp4"}`,
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusBadRequest))
		Expect(response.Body.String()).To(ContainSubstring("invalid request body"))
		var count int64
		Expect(dbConn.Model(&db.SessionReanalysisRun{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("returns the same run and queues only one task for an idempotent retry", func() {
		body := `{"client_request_id":"idempotent-1"}`
		first := httptest.NewRecorder()
		router.ServeHTTP(first, newAuthorizedJSONRequest(http.MethodPost, requestPath(), body, &user))
		Expect(first.Code).To(Equal(http.StatusAccepted), first.Body.String())
		var firstResponse controllers.CreateSessionReanalysisResponse
		Expect(json.Unmarshal(first.Body.Bytes(), &firstResponse)).To(Succeed())
		Expect(firstResponse.RunID).NotTo(BeZero())
		Expect(firstResponse.TaskID).NotTo(BeEmpty())
		Expect(firstResponse.Status).To(Equal(db.SessionReanalysisStatusQueued))

		second := httptest.NewRecorder()
		router.ServeHTTP(second, newAuthorizedJSONRequest(http.MethodPost, requestPath(), body, &user))
		Expect(second.Code).To(Equal(http.StatusAccepted), second.Body.String())
		var secondResponse controllers.CreateSessionReanalysisResponse
		Expect(json.Unmarshal(second.Body.Bytes(), &secondResponse)).To(Succeed())
		Expect(secondResponse).To(Equal(firstResponse))

		var count int64
		Expect(dbConn.Model(&db.SessionReanalysisRun{}).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))

		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal(worker.TypeSessionDebugReanalysis))
		Expect(pending[0].ID).To(Equal(firstResponse.TaskID))
		var payload worker.SessionDebugReanalysisPayload
		Expect(json.Unmarshal(pending[0].Payload, &payload)).To(Succeed())
		Expect(payload.RunID).To(Equal(firstResponse.RunID))
	})

	It("recovers a queued idempotent row whose task was not enqueued", func() {
		run := testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
			SessionID: session.SessionID, ProfileID: profile.ID,
			ClientRequestID: "recover-session-task", TaskID: "recoverable-session-task",
			Status: db.SessionReanalysisStatusQueued,
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost, requestPath(), `{"client_request_id":"recover-session-task"}`, &user,
		))

		Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
		var body controllers.CreateSessionReanalysisResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.RunID).To(Equal(run.ID))
		Expect(body.TaskID).To(Equal("recoverable-session-task"))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].ID).To(Equal(body.TaskID))
	})

	It("rejects another request while the session has an active run", func() {
		testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
			SessionID:       session.SessionID,
			ProfileID:       profile.ID,
			ClientRequestID: "already-active",
			Status:          db.SessionReanalysisStatusRunning,
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"second-request"}`,
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusConflict))
		Expect(response.Body.String()).To(ContainSubstring("already active"))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})

	It("rejects a whole-session request while a chunk run is active", func() {
		start, end := 10.0, 20.0
		chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:      session.SessionID,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			ExerciseType:   "Pull-up",
			MediaStartSecs: &start,
			MediaEndSecs:   &end,
		})
		testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
			SessionID:             session.SessionID,
			ProfileID:             profile.ID,
			ChunkAnalysisResultID: chunk.ID,
			ClientRequestID:       "active-chunk",
			Status:                db.ChunkReanalysisStatusQueued,
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"blocked-by-chunk"}`,
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusConflict))
		Expect(response.Body.String()).To(ContainSubstring("chunk re-analyses must finish"))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})

	It("enforces five whole-session runs per profile in a rolling 24-hour window", func() {
		for i := 0; i < 5; i++ {
			testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
				SessionID:       session.SessionID,
				ProfileID:       profile.ID,
				ClientRequestID: fmt.Sprintf("recent-%d", i),
				Status:          db.SessionReanalysisStatusCompleted,
				CreatedAt:       time.Now().UTC().Add(-time.Duration(i) * time.Hour),
			})
		}

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"over-quota"}`,
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusTooManyRequests))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})

	It("does not count expired or other-profile runs toward the quota", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		otherSession := testhelpers.CreateSession(dbConn, &db.Session{
			SessionID: "WOD-20260716-01JOTHERPROFILESESSION000",
			ProfileID: otherProfile.ID,
		})
		for i := 0; i < 5; i++ {
			testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
				SessionID:       session.SessionID,
				ProfileID:       profile.ID,
				ClientRequestID: fmt.Sprintf("expired-%d", i),
				Status:          db.SessionReanalysisStatusCompleted,
				CreatedAt:       time.Now().UTC().Add(-25*time.Hour - time.Duration(i)*time.Hour),
			})
			testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
				SessionID:       otherSession.SessionID,
				ProfileID:       otherProfile.ID,
				ClientRequestID: fmt.Sprintf("other-profile-%d", i),
				Status:          db.SessionReanalysisStatusCompleted,
				CreatedAt:       time.Now().UTC().Add(-time.Duration(i) * time.Hour),
			})
		}

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"within-quota"}`,
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
	})

	It("serializes concurrent profile requests so the rolling quota cannot be exceeded", func() {
		for i := 0; i < 4; i++ {
			testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
				SessionID: session.SessionID, ProfileID: profile.ID,
				ClientRequestID: fmt.Sprintf("existing-quota-%d", i),
				Status:          db.SessionReanalysisStatusCompleted, CreatedAt: time.Now().UTC(),
			})
		}
		secondSession := testhelpers.CreateSession(dbConn, &db.Session{
			SessionID: "WOD-20260716-01JCONCURRENTSESSION000", ProfileID: profile.ID, WorkoutType: "wod",
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: secondSession.SessionID, ProfileID: profile.ID, Status: "COMPLETED", Output: "original",
		})
		testhelpers.MockGCSListObjects(transport, "test-bucket", fmt.Sprintf("videos/%d/%s/", profile.ID, secondSession.SessionID),
			[]string{fmt.Sprintf("videos/%d/%s/analysis.mp4", profile.ID, secondSession.SessionID)})

		responses := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
		requests := []*http.Request{
			newAuthorizedJSONRequest(http.MethodPost, requestPath(), `{"client_request_id":"concurrent-a"}`, &user),
			newAuthorizedJSONRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%s/reanalyses", secondSession.SessionID), `{"client_request_id":"concurrent-b"}`, &user),
		}
		var wait sync.WaitGroup
		for index := range requests {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				router.ServeHTTP(responses[i], requests[i])
			}(index)
		}
		wait.Wait()
		Expect([]int{responses[0].Code, responses[1].Code}).To(ConsistOf(http.StatusAccepted, http.StatusTooManyRequests))
		var count int64
		Expect(dbConn.Model(&db.SessionReanalysisRun{}).Where("profile_id = ?", profile.ID).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(5)))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
	})

	It("is disabled unless the server feature flag is enabled", func() {
		disabledRouter := newTestRouterWithAuthService(controllers.Config{
			QueueClient: testhelpers.NewQueueClient(),
		})
		response := httptest.NewRecorder()
		disabledRouter.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"disabled"}`,
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(response.Body.String()).To(ContainSubstring("session re-analysis is disabled"))
		var count int64
		Expect(dbConn.Model(&db.SessionReanalysisRun{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})
})

var _ = Describe("GET /api/v1/sessions/:session_id/reanalyses", func() {
	var (
		router           *gin.Engine
		transport        *testhelpers.MockTransport
		profile          db.Profile
		user             db.User
		session          db.Session
		activeSessionRun db.SessionReanalysisRun
		activeChunkRun   db.ChunkReanalysisRun
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:   "WOD-20260716-01JSESSIONREANALYSISLIST0",
			ProfileID:   profile.ID,
			WorkoutType: "wod",
		})
		testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
			SessionID:       session.SessionID,
			ProfileID:       profile.ID,
			ClientRequestID: "completed-list-run",
			Status:          db.SessionReanalysisStatusCompleted,
			Output:          "Older completed candidate",
			CreatedAt:       time.Now().UTC().Add(-time.Hour),
		})
		activeSessionRun = testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
			SessionID:       session.SessionID,
			ProfileID:       profile.ID,
			ClientRequestID: "active-list-run",
			Status:          db.SessionReanalysisStatusRunning,
			CreatedAt:       time.Now().UTC(),
		})
		start, end := 4.5, 9.5
		chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:      session.SessionID,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			ExerciseType:   "Burpee",
			MediaStartSecs: &start,
			MediaEndSecs:   &end,
		})
		activeChunkRun = testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
			SessionID:             session.SessionID,
			ProfileID:             profile.ID,
			ChunkAnalysisResultID: chunk.ID,
			ClientRequestID:       "active-list-chunk",
			Status:                db.ChunkReanalysisStatusQueued,
		})

		transport = testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		testhelpers.MockGCSListObjects(
			transport,
			"test-bucket",
			fmt.Sprintf("videos/%d/%s/", profile.ID, session.SessionID),
			[]string{fmt.Sprintf("videos/%d/%s/analysis.mp4", profile.ID, session.SessionID)},
		)
		router = newTestRouterWithAuthService(controllers.Config{
			StorageClient:           storageClient,
			BucketName:              "test-bucket",
			EnableSessionReanalysis: true,
		})
	})

	It("lists owned runs newest first with creation readiness", func() {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodGet,
			fmt.Sprintf("/api/v1/sessions/%s/reanalyses", session.SessionID),
			"",
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		var body controllers.ListSessionReanalysesResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Runs).To(HaveLen(2))
		Expect(body.Runs[0].ID).To(Equal(activeSessionRun.ID))
		Expect(body.Readiness.CanCreate).To(BeFalse())
		Expect(body.Readiness.ActiveChunkRuns).To(Equal(int64(1)))
		Expect(body.Readiness.VideoAvailable).To(BeTrue())
		Expect(body.Readiness.ActiveSessionRunID).NotTo(BeNil())
		Expect(*body.Readiness.ActiveSessionRunID).To(Equal(activeSessionRun.ID))
		Expect(body.Readiness.BlockedReason).NotTo(BeEmpty())
		Expect(activeChunkRun.ID).NotTo(BeZero())
		Expect(transport.Verify()).To(Succeed())
	})
})

var _ = Describe("GET /api/v1/sessions/:session_id/reanalyses/:run_id", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
		session db.Session
		run     db.SessionReanalysisRun
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:   "WOD-20260716-01JSESSIONREANALYSISGET00",
			ProfileID:   profile.ID,
			WorkoutType: "wod",
		})
		run = testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
			SessionID:         session.SessionID,
			ProfileID:         profile.ID,
			ClientRequestID:   "get-run",
			Status:            db.SessionReanalysisStatusCompleted,
			Output:            "Candidate workout coaching",
			WorkoutType:       "wod",
			SessionScore:      `{"overall":91}`,
			HighlightSegments: `[{"start":3,"end":6}]`,
		})
		router = newTestRouterWithAuthService(controllers.Config{
			EnableSessionReanalysis: true,
		})
	})

	requestPath := func(sessionID string, runID uint) string {
		return fmt.Sprintf("/api/v1/sessions/%s/reanalyses/%d", sessionID, runID)
	}

	It("returns an owned run without exposing private source fields", func() {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodGet,
			requestPath(session.SessionID, run.ID),
			"",
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		var body controllers.SessionReanalysisRunResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.ID).To(Equal(run.ID))
		Expect(body.Status).To(Equal(db.SessionReanalysisStatusCompleted))
		Expect(body.Candidate).NotTo(BeNil())
		Expect(body.Candidate.Output).To(Equal("Candidate workout coaching"))
		Expect(response.Body.String()).NotTo(ContainSubstring("source_gcs_uri"))
		Expect(response.Body.String()).NotTo(ContainSubstring("gemini_file_uri"))
	})

	It("returns not found for another owner, session, or missing run", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, otherProfile.UserID).Error).NotTo(HaveOccurred())

		wrongOwner := httptest.NewRecorder()
		router.ServeHTTP(wrongOwner, newAuthorizedJSONRequest(
			http.MethodGet,
			requestPath(session.SessionID, run.ID),
			"",
			&otherUser,
		))
		Expect(wrongOwner.Code).To(Equal(http.StatusForbidden))

		wrongSession := httptest.NewRecorder()
		router.ServeHTTP(wrongSession, newAuthorizedJSONRequest(
			http.MethodGet,
			requestPath("WOD-20260716-01JDIFFERENTSESSION000000", run.ID),
			"",
			&user,
		))
		Expect(wrongSession.Code).To(Equal(http.StatusNotFound))

		missingRun := httptest.NewRecorder()
		router.ServeHTTP(missingRun, newAuthorizedJSONRequest(
			http.MethodGet,
			requestPath(session.SessionID, run.ID+999),
			"",
			&user,
		))
		Expect(missingRun.Code).To(Equal(http.StatusNotFound))
	})
})
