package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

type feedbackTestFixture struct {
	user    db.User
	profile db.Profile
	session db.Session
	chunk   db.ChunkAnalysisResult
}

func createFeedbackTestFixture(sessionID string) feedbackTestFixture {
	user := testhelpers.CreateUser(dbConn, &db.User{Username: "feedback-" + sessionID})
	profile := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID, Name: "Feedback Tester"})
	session := testhelpers.CreateSession(dbConn, &db.Session{
		SessionID:      sessionID,
		ProfileID:      profile.ID,
		Status:         db.SessionStatus(db.SessionStatusCompleted),
		WODDescription: "10 rounds",
		WorkoutType:    "wod",
	})
	chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
		SessionID:       sessionID,
		ProfileID:       profile.ID,
		ExerciseType:    "Rope Climb",
		Status:          "COMPLETED",
		Output:          "Original coaching",
		ObservedSignals: `{"reps":3}`,
		HeartRateBPM:    140,
	})
	return feedbackTestFixture{user: user, profile: profile, session: session, chunk: chunk}
}

func feedbackRequest(method, path, body string, user *db.User) *http.Request {
	return newAuthorizedJSONRequest(method, path, body, user)
}

var _ = Describe("POST /api/v1/sessions/:session_id/feedback", func() {
	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
	})

	It("creates an immutable chunk correction with a server-captured prediction", func() {
		fixture := createFeedbackTestFixture("feedback-create")
		router := newTestRouterWithAuthService(controllers.Config{})
		body := fmt.Sprintf(`{
			"client_request_id":"feedback-create-1",
			"target_type":"chunk",
			"chunk_id":%d,
			"category":"movement",
			"correction":{"activity_state":"exercise","movement_name":"Pull-up","fatigue_state":"not_fatigued"},
			"note":"The rope was behind the athlete"
		}`, fixture.chunk.ID)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, feedbackRequest(http.MethodPost, "/api/v1/sessions/feedback-create/feedback", body, &fixture.user))
		Expect(w.Code).To(Equal(http.StatusCreated), w.Body.String())

		var response controllers.FeedbackResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Feedback.Revision).To(Equal(1))
		Expect(response.Feedback.ChunkAnalysisResultID).To(Equal(&fixture.chunk.ID))
		Expect(string(response.Feedback.OriginalPrediction)).To(ContainSubstring(`"exercise_type":"Rope Climb"`))

		var original db.ChunkAnalysisResult
		Expect(dbConn.First(&original, fixture.chunk.ID).Error).NotTo(HaveOccurred())
		Expect(original.ExerciseType).To(Equal("Rope Climb"))
		Expect(original.Output).To(Equal("Original coaching"))
	})

	It("returns the existing event for an idempotent retry", func() {
		fixture := createFeedbackTestFixture("feedback-idempotent")
		router := newTestRouterWithAuthService(controllers.Config{})
		body := fmt.Sprintf(`{"client_request_id":"same-request","target_type":"chunk","chunk_id":%d,"category":"movement","correction":{"movement_name":"Pull-up"}}`, fixture.chunk.ID)
		path := "/api/v1/sessions/feedback-idempotent/feedback"

		first := httptest.NewRecorder()
		router.ServeHTTP(first, feedbackRequest(http.MethodPost, path, body, &fixture.user))
		Expect(first.Code).To(Equal(http.StatusCreated), first.Body.String())
		second := httptest.NewRecorder()
		router.ServeHTTP(second, feedbackRequest(http.MethodPost, path, body, &fixture.user))
		Expect(second.Code).To(Equal(http.StatusOK), second.Body.String())
		changed := httptest.NewRecorder()
		changedBody := fmt.Sprintf(`{"client_request_id":"same-request","target_type":"chunk","chunk_id":%d,"category":"movement","correction":{"movement_name":"Strict Pull-up"}}`, fixture.chunk.ID)
		router.ServeHTTP(changed, feedbackRequest(http.MethodPost, path, changedBody, &fixture.user))
		Expect(changed.Code).To(Equal(http.StatusConflict), changed.Body.String())

		var count int64
		Expect(dbConn.Model(&db.AnalysisFeedback{}).Count(&count).Error).NotTo(HaveOccurred())
		Expect(count).To(Equal(int64(1)))
	})

	It("rejects a chunk from another owned session", func() {
		fixture := createFeedbackTestFixture("feedback-owner-a")
		other := testhelpers.CreateSession(dbConn, &db.Session{
			SessionID: "feedback-owner-b", ProfileID: fixture.profile.ID, Status: db.SessionStatus(db.SessionStatusCompleted),
		})
		Expect(other.ID).NotTo(BeZero())
		router := newTestRouterWithAuthService(controllers.Config{})
		body := fmt.Sprintf(`{"client_request_id":"wrong-session","target_type":"chunk","chunk_id":%d,"category":"movement","correction":{"movement_name":"Pull-up"}}`, fixture.chunk.ID)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, feedbackRequest(http.MethodPost, "/api/v1/sessions/feedback-owner-b/feedback", body, &fixture.user))
		Expect(w.Code).To(Equal(http.StatusForbidden), w.Body.String())
	})

	It("applies a completed debug candidate only through explicit feedback", func() {
		fixture := createFeedbackTestFixture("feedback-candidate")
		router := newTestRouterWithAuthService(controllers.Config{})
		queued := testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
			SessionID:             fixture.session.SessionID,
			ProfileID:             fixture.profile.ID,
			ChunkAnalysisResultID: fixture.chunk.ID,
			Status:                db.ChunkReanalysisStatusQueued,
		})
		path := "/api/v1/sessions/feedback-candidate/feedback"
		queuedBody := fmt.Sprintf(`{"client_request_id":"candidate-queued","target_type":"chunk","chunk_id":%d,"category":"movement","correction":{"movement_name":"Pull-up"},"reanalysis_run_id":%d}`, fixture.chunk.ID, queued.ID)
		queuedResponse := httptest.NewRecorder()
		router.ServeHTTP(queuedResponse, feedbackRequest(http.MethodPost, path, queuedBody, &fixture.user))
		Expect(queuedResponse.Code).To(Equal(http.StatusBadRequest), queuedResponse.Body.String())

		Expect(dbConn.Model(&db.ChunkReanalysisRun{}).Where("id = ?", queued.ID).
			Updates(map[string]any{"status": db.ChunkReanalysisStatusCompleted}).Error).To(Succeed())
		completedBody := fmt.Sprintf(`{"client_request_id":"candidate-confirmed","target_type":"chunk","chunk_id":%d,"category":"movement","correction":{"movement_name":"Pull-up"},"reanalysis_run_id":%d}`, fixture.chunk.ID, queued.ID)
		completedResponse := httptest.NewRecorder()
		router.ServeHTTP(completedResponse, feedbackRequest(http.MethodPost, path, completedBody, &fixture.user))
		Expect(completedResponse.Code).To(Equal(http.StatusCreated), completedResponse.Body.String())

		var response controllers.FeedbackResponse
		Expect(json.Unmarshal(completedResponse.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Feedback.ReanalysisRunID).To(Equal(&queued.ID))
		var original db.ChunkAnalysisResult
		Expect(dbConn.First(&original, fixture.chunk.ID).Error).To(Succeed())
		Expect(original.ExerciseType).To(Equal("Rope Climb"))
		Expect(original.Output).To(Equal("Original coaching"))
	})
})

var _ = Describe("GET /api/v1/sessions/:session_id/feedback", func() {
	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
	})

	It("returns current corrections separately from append-only history", func() {
		fixture := createFeedbackTestFixture("feedback-list")
		chunkID := fixture.chunk.ID
		first := testhelpers.CreateAnalysisFeedback(dbConn, &db.AnalysisFeedback{
			FeedbackKey:           "list-chain",
			ProfileID:             fixture.profile.ID,
			SessionID:             fixture.session.SessionID,
			TargetType:            db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &chunkID,
			Category:              db.FeedbackCategoryMovement,
			OriginalPrediction:    db.JSONDocument(`{"exercise_type":"Rope Climb"}`),
			Correction:            db.JSONDocument(`{"movement_name":"Pull-up"}`),
		})
		testhelpers.CreateAnalysisFeedback(dbConn, &db.AnalysisFeedback{
			FeedbackKey:           first.FeedbackKey,
			ProfileID:             fixture.profile.ID,
			SessionID:             fixture.session.SessionID,
			TargetType:            db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &chunkID,
			Category:              db.FeedbackCategoryMovement,
			OriginalPrediction:    first.OriginalPrediction,
			Correction:            db.JSONDocument(`{"movement_name":"Strict Pull-up"}`),
			Revision:              2,
			SupersedesFeedbackID:  &first.ID,
		})
		router := newTestRouterWithAuthService(controllers.Config{})

		w := httptest.NewRecorder()
		router.ServeHTTP(w, feedbackRequest(http.MethodGet, "/api/v1/sessions/feedback-list/feedback", "", &fixture.user))
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())

		var response controllers.FeedbackListResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.History).To(HaveLen(2))
		Expect(response.Current).To(HaveLen(1))
		Expect(response.Current[0].Revision).To(Equal(2))
		Expect(response.HasActiveCorrections).To(BeTrue())
	})
})

var _ = Describe("PATCH /api/v1/sessions/:session_id/feedback/:feedback_id", func() {
	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
	})

	It("appends a revision and rejects a stale concurrent edit", func() {
		fixture := createFeedbackTestFixture("feedback-update")
		chunkID := fixture.chunk.ID
		first := testhelpers.CreateAnalysisFeedback(dbConn, &db.AnalysisFeedback{
			ProfileID:             fixture.profile.ID,
			SessionID:             fixture.session.SessionID,
			TargetType:            db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &chunkID,
			Category:              db.FeedbackCategoryMovement,
			OriginalPrediction:    db.JSONDocument(`{"exercise_type":"Rope Climb"}`),
			Correction:            db.JSONDocument(`{"movement_name":"Pull-up"}`),
		})
		router := newTestRouterWithAuthService(controllers.Config{})
		path := fmt.Sprintf("/api/v1/sessions/feedback-update/feedback/%d", first.ID)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, feedbackRequest(http.MethodPatch, path, `{"client_request_id":"edit-1","expected_revision":1,"category":"activity","correction":{"activity_state":"walking"}}`, &fixture.user))
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var response controllers.FeedbackResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Feedback.Category).To(Equal(db.FeedbackCategoryActivity))

		stale := httptest.NewRecorder()
		router.ServeHTTP(stale, feedbackRequest(http.MethodPatch, path, `{"client_request_id":"edit-2","expected_revision":1,"category":"movement","correction":{"movement_name":"Kipping Pull-up"}}`, &fixture.user))
		Expect(stale.Code).To(Equal(http.StatusConflict), stale.Body.String())

		var count int64
		Expect(dbConn.Model(&db.AnalysisFeedback{}).Where("feedback_key = ?", first.FeedbackKey).Count(&count).Error).NotTo(HaveOccurred())
		Expect(count).To(Equal(int64(2)))
	})
})

var _ = Describe("DELETE /api/v1/sessions/:session_id/feedback/:feedback_id", func() {
	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
	})

	It("appends a retraction instead of deleting history", func() {
		fixture := createFeedbackTestFixture("feedback-delete")
		chunkID := fixture.chunk.ID
		first := testhelpers.CreateAnalysisFeedback(dbConn, &db.AnalysisFeedback{
			ProfileID:             fixture.profile.ID,
			SessionID:             fixture.session.SessionID,
			TargetType:            db.FeedbackTargetChunk,
			ChunkAnalysisResultID: &chunkID,
			Category:              db.FeedbackCategoryMovement,
			OriginalPrediction:    db.JSONDocument(`{"exercise_type":"Rope Climb"}`),
			Correction:            db.JSONDocument(`{"movement_name":"Pull-up"}`),
		})
		router := newTestRouterWithAuthService(controllers.Config{})
		path := fmt.Sprintf("/api/v1/sessions/feedback-delete/feedback/%d", first.ID)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, feedbackRequest(http.MethodDelete, path, `{"client_request_id":"undo-1","expected_revision":1}`, &fixture.user))
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())

		var events []db.AnalysisFeedback
		Expect(dbConn.Where("feedback_key = ?", first.FeedbackKey).Order("revision").Find(&events).Error).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
		Expect(events[0].Retracted).To(BeFalse())
		Expect(events[1].Retracted).To(BeTrue())
	})
})
