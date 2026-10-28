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

var _ = Describe("GET /api/v1/sessions/:session_id/analysis", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
		session db.Session
	)

	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)
		router = newTestRouterWithAuthService(controllers.Config{})

		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:      "WOD-20260715-01JTESTSESSIONANALYSIS00",
			ProfileID:      profile.ID,
			WODDescription: "5 rounds of Pull-ups",
			MovementHints:  db.JSONDocument(`["Pull-up"]`),
			WorkoutType:    "wod",
		})
	})

	It("returns the original analysis, every chronologically ordered chunk, active feedback, and unlisted observations", func() {
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:         session.SessionID,
			ProfileID:         profile.ID,
			Status:            "COMPLETED",
			Output:            "Original final analysis",
			HighlightSegments: `[{"start":"0:05","end":"0:07","type":"best_form","movement":"Pull-up","reason":"stable kip"}]`,
			WODDescription:    session.WODDescription,
		})

		zero := 0.0
		ten := 10.0
		fifteen := 15.0
		twenty := 20.0
		pullUp := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:      session.SessionID,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			ExerciseType:   "Pull-up",
			MediaStartSecs: &twenty,
		})
		failed := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID: session.SessionID,
			ProfileID: profile.ID,
			Status:    "FAILED",
		})
		ropeClimb := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:      session.SessionID,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			ExerciseType:   "Rope Climb",
			MediaStartSecs: &zero,
		})
		unknown := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:    session.SessionID,
			ProfileID:    profile.ID,
			Status:       "COMPLETED",
			ExerciseType: "Unknown",
			StartSecs:    &ten,
		})
		walking := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:    session.SessionID,
			ProfileID:    profile.ID,
			Status:       "COMPLETED",
			ExerciseType: "Walking",
			StartSecs:    &fifteen,
		})

		chunkID := ropeClimb.ID
		active := testhelpers.CreateAnalysisFeedback(dbConn, &db.AnalysisFeedback{
			ProfileID:             profile.ID,
			SessionID:             session.SessionID,
			TargetType:            db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &chunkID,
			Category:              db.FeedbackCategoryMovement,
			OriginalPrediction:    db.JSONDocument(`{"exercise_type":"Rope Climb"}`),
			Correction:            db.JSONDocument(`{"movement_name":"Pull-up"}`),
		})
		testhelpers.CreateAnalysisFeedback(dbConn, &db.AnalysisFeedback{
			ProfileID:          profile.ID,
			SessionID:          session.SessionID,
			TargetType:         db.FeedbackTargetSession,
			Category:           db.FeedbackCategoryOther,
			OriginalPrediction: db.JSONDocument(`{}`),
			Correction:         db.JSONDocument(`{}`),
			Note:               "retracted note",
			Retracted:          true,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/"+session.SessionID+"/analysis", "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var response controllers.SessionAnalysisResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.SessionID).To(Equal(session.SessionID))
		Expect(response.Analysis).NotTo(BeNil())
		Expect(response.Analysis.ID).To(Equal(analysis.ID))
		Expect(response.Analysis.Output).To(Equal("Original final analysis"))
		Expect(response.Analysis.WorkoutType).To(Equal("wod"))
		var highlights []worker.HighlightSegment
		Expect(json.Unmarshal([]byte(response.Analysis.HighlightSegments), &highlights)).To(Succeed())
		Expect(highlights).To(HaveLen(1))
		Expect(highlights[0].Version).To(Equal(2))
		Expect(highlights[0].Observations).To(HaveLen(1))
		Expect(response.MovementHints).To(Equal([]string{"Pull-up"}))
		Expect(response.AdditionalObservedMovements).To(Equal([]string{"Rope Climb"}))
		Expect(response.Feedback).To(HaveLen(1))
		Expect(response.Feedback[0].ID).To(Equal(active.ID))
		Expect(response.CorrectionsUpdatedAt).NotTo(BeNil())

		chunkIDs := make([]uint, 0, len(response.Chunks))
		for _, chunk := range response.Chunks {
			chunkIDs = append(chunkIDs, chunk.ID)
		}
		Expect(chunkIDs).To(Equal([]uint{ropeClimb.ID, unknown.ID, walking.ID, pullUp.ID, failed.ID}))
	})

	It("rejects a user who does not own the session", func() {
		otherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, otherProfile.UserID).Error).NotTo(HaveOccurred())

		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/"+session.SessionID+"/analysis", "", &otherUser)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusForbidden))
	})

	It("returns not found instead of treating an unknown session as owned", func() {
		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/wod-20201010-1234567890invalid/analysis", "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusNotFound))
		Expect(w.Body.String()).To(ContainSubstring("session not found"))
	})

	It("does not list GCS objects while loading the session aggregate", func() {
		transport := testhelpers.NewMockTransport()
		client, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		router = newTestRouterWithAuthService(controllers.Config{
			StorageClient: client,
			BucketName:    "test-bucket",
		})

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:       session.SessionID,
			ProfileID:       profile.ID,
			Status:          "COMPLETED",
			Output:          "Original final analysis",
			WODDescription:  session.WODDescription,
			AvailableVideos: db.CommaStringArray{"merged"},
		})

		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/"+session.SessionID+"/analysis", "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		Expect(transport.Requests()).To(BeEmpty())

		var response map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		analysis, ok := response["analysis"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(analysis).To(HaveKey("available_videos"))
		Expect(analysis["available_videos"]).To(ContainElement("merged"))
	})

	It("keeps legacy analysis-only sessions readable", func() {
		legacyID := "P1-WOD-2026-07-15-18-21"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:         legacyID,
			ProfileID:         profile.ID,
			Status:            "COMPLETED",
			Output:            "legacy",
			HighlightSegments: `[]`,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/analysis", legacyID), "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var response controllers.SessionAnalysisResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Analysis).NotTo(BeNil())
		Expect(response.MovementHints).To(BeEmpty())
		Expect(response.MovementHints).NotTo(BeNil())
	})

	It("falls back to sessions.wod_description when analysis_results.wod_description is empty", func() {
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:         session.SessionID,
			ProfileID:         profile.ID,
			Status:            "COMPLETED",
			Output:            "Original final analysis",
			HighlightSegments: `[]`,
			WODDescription:    "",
		})

		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/"+session.SessionID+"/analysis", "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var response controllers.SessionAnalysisResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.WODDescription).To(Equal("5 rounds of Pull-ups"))
		Expect(response.Analysis).NotTo(BeNil())
		Expect(response.Analysis.WODDescription).To(Equal("5 rounds of Pull-ups"))
	})

	It("excludes redundant split_chunk_* rows that overlap original mobile chunks", func() {
		cap0, cap10 := 118.5, 128.5
		med0, med10 := 0.0, 10.0
		mobile := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:      session.SessionID,
			ProfileID:      profile.ID,
			FilePath:       fmt.Sprintf("gs://test-bucket/videos/%d/%s/mobile_000.mp4", profile.ID, session.SessionID),
			Status:         "COMPLETED",
			ExerciseType:   "Burpee",
			HeartRateBPM:   156,
			StartSecs:      &cap0,
			EndSecs:        &cap10,
			MediaStartSecs: &med0,
			MediaEndSecs:   &med10,
		})
		testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:      session.SessionID,
			ProfileID:      profile.ID,
			FilePath:       fmt.Sprintf("gs://test-bucket/videos/%d/%s/split_chunk_000.mp4", profile.ID, session.SessionID),
			Status:         "COMPLETED",
			ExerciseType:   "Burpee",
			HeartRateBPM:   0,
			StartSecs:      &med0,
			EndSecs:        &med10,
			MediaStartSecs: &med0,
			MediaEndSecs:   &med10,
		})

		req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/"+session.SessionID+"/analysis", "", &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var response controllers.SessionAnalysisResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Chunks).To(HaveLen(1))
		Expect(response.Chunks[0].ID).To(Equal(mobile.ID))
		Expect(response.Chunks[0].HeartRateBPM).To(Equal(156))
	})
})

var _ = Describe("POST /api/v1/merge-chunks", func() {
	var (
		router  *gin.Engine
		profile db.Profile
		user    db.User
		session db.Session
	)

	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
		defaultTestUser = user
		session = testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:     "session-merge-hints",
			ProfileID:     profile.ID,
			MovementHints: db.JSONDocument(`["Old hint"]`),
		})
		router = newTestRouter(controllers.Config{
			QueueClient:        testhelpers.NewQueueClient(),
			BucketName:         "test-bucket",
			NewMergeChunksTask: worker.NewMergeChunksTask,
		})
	})

	It("updates movement hints and wod_description for the exact existing session", func() {
		body := fmt.Sprintf(`{"session_id":"%s","profile_id":%d,"movements":["Power Snatch","Custom Carry"],"wod_description":"For Time: 30 Power Snatches (135 lb)","workout_type":"wod"}`, session.SessionID, profile.ID)
		req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/merge-chunks", body, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusAccepted), w.Body.String())
		var updated db.Session
		Expect(dbConn.First(&updated, session.ID).Error).NotTo(HaveOccurred())
		Expect(string(updated.MovementHints)).To(MatchJSON(`["Power Snatch","Custom Carry"]`))
		Expect(updated.WODDescription).To(Equal("For Time: 30 Power Snatches (135 lb)"))
	})

	It("deduplicates repeated merge-chunks requests and returns 202 Accepted", func() {
		body := fmt.Sprintf(`{"session_id":"%s","profile_id":%d,"movements":["Pull-up"],"workout_type":"wod"}`, session.SessionID, profile.ID)

		req1 := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/merge-chunks", body, &user)
		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, req1)
		Expect(w1.Code).To(Equal(http.StatusAccepted), w1.Body.String())

		req2 := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/merge-chunks", body, &user)
		w2 := httptest.NewRecorder()
		router.ServeHTTP(w2, req2)
		Expect(w2.Code).To(Equal(http.StatusAccepted), w2.Body.String())
		Expect(w2.Body.String()).To(ContainSubstring("Merge already queued"))

		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		mergeCount := 0
		for _, t := range pending {
			if t.Type == worker.TypeMergeChunks {
				mergeCount++
			}
		}
		Expect(mergeCount).To(Equal(1))
	})
})
