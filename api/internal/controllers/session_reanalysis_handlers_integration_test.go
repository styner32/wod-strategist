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
	"github.com/wod-strategist/api/internal/workoutprogress"
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
		ensureTestDB()
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

	It("persists custom wod_description on the run when provided", func() {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"wod-desc-1","wod_description":"5 rounds of 10 pullups and 20 thrusters"}`,
			&user,
		))
		Expect(response.Code).To(Equal(http.StatusAccepted))
		var resp controllers.CreateSessionReanalysisResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &resp)).To(Succeed())

		var saved db.SessionReanalysisRun
		Expect(dbConn.First(&saved, resp.RunID).Error).To(Succeed())
		Expect(saved.WODDescription).To(Equal("5 rounds of 10 pullups and 20 thrusters"))
	})

	It("falls back to the existing session wod_description when none is provided", func() {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"wod-desc-fallback-1"}`,
			&user,
		))
		Expect(response.Code).To(Equal(http.StatusAccepted))
		var resp controllers.CreateSessionReanalysisResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &resp)).To(Succeed())

		var saved db.SessionReanalysisRun
		Expect(dbConn.First(&saved, resp.RunID).Error).To(Succeed())
		Expect(saved.WODDescription).To(Equal("3 rounds of pull-ups and burpees"))
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

	It("enforces twenty whole-session runs per profile in a rolling 24-hour window", func() {
		for i := 0; i < 20; i++ {
			testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
				SessionID:       session.SessionID,
				ProfileID:       profile.ID,
				ClientRequestID: fmt.Sprintf("recent-%d", i),
				Status:          db.SessionReanalysisStatusCompleted,
				CreatedAt:       time.Now().UTC().Add(-time.Duration(i) * time.Minute),
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

	It("enforces the startup-configured quota instead of the default", func() {
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		limited := newTestRouterWithAuthService(controllers.Config{
			QueueClient:                 testhelpers.NewQueueClient(),
			StorageClient:               storageClient,
			BucketName:                  "test-bucket",
			EnableSessionReanalysis:     true,
			SessionReanalysisDailyQuota: 2,
		})
		for i := 0; i < 2; i++ {
			testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
				SessionID:       session.SessionID,
				ProfileID:       profile.ID,
				ClientRequestID: fmt.Sprintf("configured-%d", i),
				Status:          db.SessionReanalysisStatusCompleted,
			})
		}

		response := httptest.NewRecorder()
		limited.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, requestPath(), `{"client_request_id":"over-configured-quota"}`, &user))

		Expect(response.Code).To(Equal(http.StatusTooManyRequests))
	})

	It("does not count expired or other-profile runs toward the quota", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		otherSession := testhelpers.CreateSession(dbConn, &db.Session{
			SessionID: "WOD-20260716-01JOTHERPROFILESESSION000",
			ProfileID: otherProfile.ID,
		})
		for i := 0; i < 20; i++ {
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
				CreatedAt:       time.Now().UTC().Add(-time.Duration(i) * time.Minute),
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
		for i := 0; i < 19; i++ {
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
		Expect(count).To(Equal(int64(20)))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
	})

	It("allows wod_start_secs on legacy WOD sessions and rejects it on non-WOD sessions", func() {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"legacy-wod-start","wod_start_secs":12.5}`,
			&user,
		))
		Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())

		warmupSession := testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:   "WARMUP-20260716-01JWARMUPSTARTREJECT00",
			ProfileID:   profile.ID,
			WorkoutType: "warmup",
		})
		warmupResp := httptest.NewRecorder()
		router.ServeHTTP(warmupResp, newAuthorizedJSONRequest(
			http.MethodPost,
			fmt.Sprintf("/api/v1/sessions/%s/reanalyses", warmupSession.SessionID),
			`{"client_request_id":"warmup-wod-start","wod_start_secs":5}`,
			&user,
		))
		Expect(warmupResp.Code).To(Equal(http.StatusConflict))
	})

	It("allows explicit owned legacy video reanalysis without upgrading score eligibility", func() {
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		selectedRouter := newTestRouterWithAuthService(controllers.Config{
			QueueClient:   testhelpers.NewQueueClient(),
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})
		response := httptest.NewRecorder()
		selectedRouter.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(),
			`{"client_request_id":"disabled"}`,
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
		var count int64
		Expect(dbConn.Model(&db.SessionReanalysisRun{}).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
		Expect(dbConn.First(&session, session.ID).Error).To(Succeed())
		Expect(session.WODEvaluationVersion).To(BeZero())
		Expect(transport.Verify()).To(Succeed())
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
		ensureTestDB()
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
		ensureTestDB()
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

var _ = Describe("POST /api/v1/sessions/:session_id/reanalyses/:run_id/apply", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
		session db.Session
		run     db.SessionReanalysisRun
	)

	requestPath := func(sessionID string, runID uint) string {
		return fmt.Sprintf("/api/v1/sessions/%s/reanalyses/%d/apply", sessionID, runID)
	}

	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:      "WOD-20260716-01JAPPLYTEST0000000",
			ProfileID:      profile.ID,
			WorkoutType:    "wod",
			WODDescription: "Original WOD description",
		})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:         session.SessionID,
			ProfileID:         profile.ID,
			Status:            "COMPLETED",
			Output:            `{"coaching_feedback":"Original output"}`,
			SessionScore:      `{"overall":70}`,
			HighlightSegments: `[{"type":"key_moment","start":"0:01"}]`,
			WODDescription:    "Original WOD description",
		})

		run = testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
			SessionID:         session.SessionID,
			ProfileID:         profile.ID,
			Status:            db.SessionReanalysisStatusCompleted,
			Output:            `{"coaching_feedback":"Updated candidate output"}`,
			SessionScore:      `{"overall":95}`,
			HighlightSegments: `[{"type":"candidate_highlight","start":"0:05"}]`,
			WODDescription:    "Updated candidate WOD description",
			WorkoutType:       "amrap",
		})

		router = newTestRouterWithAuthService(controllers.Config{
			EnableSessionReanalysis: true,
		})
	})

	It("atomically applies the completed candidate to analysis_results and sessions", func() {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(session.SessionID, run.ID),
			"",
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusOK))
		var applyResp controllers.ApplySessionReanalysisResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &applyResp)).To(Succeed())
		Expect(applyResp.SessionID).To(Equal(session.SessionID))
		Expect(applyResp.RunID).To(Equal(run.ID))

		// Check analysis_results
		var updatedAnalysis db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", session.SessionID).First(&updatedAnalysis).Error).To(Succeed())
		Expect(updatedAnalysis.Output).To(Equal(`{"coaching_feedback":"Updated candidate output"}`))
		Expect(updatedAnalysis.SessionScore).To(Equal(`{"overall":95}`))
		Expect(updatedAnalysis.HighlightSegments).To(Equal(`[{"type":"candidate_highlight","start":"0:05"}]`))
		Expect(updatedAnalysis.WODDescription).To(Equal("Updated candidate WOD description"))

		// Check sessions
		var updatedSession db.Session
		Expect(dbConn.Where("session_id = ?", session.SessionID).First(&updatedSession).Error).To(Succeed())
		Expect(updatedSession.WODDescription).To(Equal("Updated candidate WOD description"))
		Expect(updatedSession.WorkoutType).To(Equal("amrap"))
	})

	It("rejects applying when run status is not COMPLETED", func() {
		runningRun := testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
			SessionID: session.SessionID,
			ProfileID: profile.ID,
			Status:    db.SessionReanalysisStatusRunning,
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(session.SessionID, runningRun.ID),
			"",
			&user,
		))

		Expect(response.Code).To(Equal(http.StatusConflict))
		Expect(response.Body.String()).To(ContainSubstring("only COMPLETED runs can be applied"))
	})

	It("rejects unauthorized user for apply", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		otherUser := db.User{}
		Expect(dbConn.First(&otherUser, otherProfile.UserID).Error).NotTo(HaveOccurred())

		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(
			http.MethodPost,
			requestPath(session.SessionID, run.ID),
			"",
			&otherUser,
		))

		Expect(response.Code).To(Equal(http.StatusForbidden))
	})

	Context("WOD start candidate", func() {
		It("rejects stale start candidates without replacing score or effort", func() {
			ensureTestDB()
			testhelpers.CleanupDB(dbConn)
			profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
			var user db.User
			Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
			effort := 7
			session := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261013-STALESTART", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: 1, Effort: &effort})
			current := `{"evaluation_version":1,"intensity":80}`
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", Output: "newer analysis", SessionScore: current})
			run := testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", OriginalAnalysisSnapshot: db.JSONDocument(`{"output":"old analysis","session_score":"{}"}`), Output: "stale candidate", SessionScore: `{"evaluation_version":1,"intensity":70}`})
			router := newTestRouterWithAuthService(controllers.Config{})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%s/reanalyses/%d/apply", session.SessionID, run.ID), `{}`, &user))
			Expect(response.Code).To(Equal(409))
			var stored db.AnalysisResult
			Expect(dbConn.Where("session_id = ?", session.SessionID).First(&stored).Error).To(Succeed())
			Expect(stored.SessionScore).To(Equal(current))
			Expect(dbConn.First(&session, session.ID).Error).To(Succeed())
			Expect(session.Effort).NotTo(BeNil())
			Expect(*session.Effort).To(Equal(7))
		})

		It("promotes a legacy WOD session to wod_evaluation_version=1 and backfills workout_at without moving the profile rollout time", func() {
			ensureTestDB()
			testhelpers.CleanupDB(dbConn)
			trackingStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
			profile := testhelpers.CreateProfile(dbConn, &db.Profile{
				WODTrackingStartedAt: trackingStart,
			})
			var user db.User
			Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())

			legacySessionID := fmt.Sprintf("P%d-WOD-2026-03-10-18-30", profile.ID)
			legacySession := testhelpers.CreateSession(dbConn, &db.Session{
				SessionID:            legacySessionID,
				ProfileID:            profile.ID,
				WorkoutType:          "wod",
				WODEvaluationVersion: 0,
			})
			analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:    legacySession.SessionID,
				ProfileID:    profile.ID,
				Status:       "COMPLETED",
				Output:       "legacy output",
				SessionScore: `{"overall":75}`,
				CreatedAt:    time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC),
			})
			snapshot, err := json.Marshal(map[string]any{
				"output":        analysis.Output,
				"session_score": analysis.SessionScore,
			})
			Expect(err).NotTo(HaveOccurred())

			candidateScore := `{"evaluation_version":1,"evidence_status":"complete","form":85,"intensity":82,"summary":"solid"}`
			run := testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
				SessionID:                legacySession.SessionID,
				ProfileID:                profile.ID,
				Status:                   db.SessionReanalysisStatusCompleted,
				OriginalAnalysisSnapshot: db.JSONDocument(snapshot),
				Output:                   "new version-1 output",
				SessionScore:             candidateScore,
				WorkoutType:              "wod",
			})

			router := newTestRouterWithAuthService(controllers.Config{
				EnableSessionReanalysis: true,
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(
				http.MethodPost,
				fmt.Sprintf("/api/v1/sessions/%s/reanalyses/%d/apply", legacySession.SessionID, run.ID),
				`{}`,
				&user,
			))
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())

			var updatedSession db.Session
			Expect(dbConn.First(&updatedSession, legacySession.ID).Error).To(Succeed())
			Expect(updatedSession.WODEvaluationVersion).To(Equal(1))

			var updatedAnalysis db.AnalysisResult
			Expect(dbConn.First(&updatedAnalysis, analysis.ID).Error).To(Succeed())
			Expect(updatedAnalysis.SessionScore).To(Equal(candidateScore))
			Expect(updatedAnalysis.WorkoutAt).NotTo(BeNil())
			Expect(updatedAnalysis.WorkoutAtSource).NotTo(BeNil())
			Expect(*updatedAnalysis.WorkoutAtSource).To(Equal("legacy_local_time"))

			var updatedProfile db.Profile
			Expect(dbConn.First(&updatedProfile, profile.ID).Error).To(Succeed())
			Expect(updatedProfile.WODTrackingStartedAt).To(BeTemporally("~", trackingStart, time.Second), "wod_tracking_started_at is the rollout time and drives automatic versioning")
		})
	})

	Context("workout progress candidate", func() {
		It("atomically replaces progress and invalidates confirmations on changed source for legacy warmup", func() {
			profile, user := progressOwner()
			old, oldRaw := progressFixture("old-video")
			next, nextRaw := progressFixture("new-video")
			s := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WARMUP-20261013-APPLY", ProfileID: profile.ID, WorkoutType: "warmup"})
			a := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: s.SessionID, ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: oldRaw})
			record := testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{ProfileID: profile.ID, AnalysisID: a.ID, SessionID: s.SessionID, IntervalID: old.Intervals[0].ID, SourceVersion: old.SourceVersion, Movement: "Power Clean", StartSecs: 0, EndSecs: 60, Reps: 3, RepsPerMinute: 3})
			snapshot, err := json.Marshal(map[string]any{"workout_progress": old})
			Expect(err).NotTo(HaveOccurred())
			run := testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{SessionID: s.SessionID, ProfileID: profile.ID, Status: "COMPLETED", OriginalAnalysisSnapshot: db.JSONDocument(snapshot), WorkoutProgress: nextRaw, SessionScore: `{}`, WorkoutType: "warmup"})
			router := newTestRouterWithAuthService(controllers.Config{})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%s/reanalyses/%d/apply", s.SessionID, run.ID), `{}`, &user))
			Expect(response.Code).To(Equal(200), response.Body.String())
			Expect(dbConn.First(&record, record.ID).Error).To(Succeed())
			Expect(record.Status).To(Equal("needs_confirmation"))
			Expect(dbConn.First(&a, a.ID).Error).To(Succeed())
			stored, err := workoutprogress.Decode(a.WorkoutProgress)
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.SourceVersion).To(Equal(next.SourceVersion))
			Expect(dbConn.First(&s, s.ID).Error).To(Succeed())
			Expect(s.WODEvaluationVersion).To(BeZero())
			// The same old snapshot cannot apply again even with legacy score version 0.
			response = httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%s/reanalyses/%d/apply", s.SessionID, run.ID), `{}`, &user))
			Expect(response.Code).To(Equal(409))
		})
	})
})
