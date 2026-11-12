package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/wodeval"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"go.uber.org/zap"
)

type historyArchiveRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip historyArchiveRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func historyArchiveOwner() (db.Profile, db.User, *gin.Engine) {
	ensureTestDB()
	testhelpers.CleanupDB(dbConn)
	profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
	var user db.User
	Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
	return profile, user, newTestRouter(controllers.Config{})
}

func historyArchiveRequest(router *gin.Engine, user db.User, method, path, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newAuthorizedJSONRequest(method, path, body, &user))
	return response
}

var _ = Describe("POST /api/v1/history/:id/archive", func() {
	It("discards in-flight AI advice when its source workout is archived before the response", func() {
		profile, user, _ := historyArchiveOwner()
		at, source := time.Now().UTC().Add(-time.Hour), "session_ulid"
		effort := 8
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261029-ARCHIVEINFLIGHT", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: 1, Effort: &effort})
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-ARCHIVEINFLIGHT", ProfileID: profile.ID, Status: "COMPLETED", WorkoutAt: &at, WorkoutAtSource: &source, SessionScore: `{"intensity":85,"movements":{"Push Jerk":{"reps":30}}}`})
		transport := testhelpers.NewMockTransport()
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").Reply(http.StatusOK).JSON(map[string]any{
			"candidates":    []any{map[string]any{"content": map[string]any{"parts": []any{map[string]string{"text": `{"target_rpe":{"score":9,"label":"RPE 9","pacing_strategy":"stale-ai-advice"},"overall_summary":"stale-ai-advice"}`}}}}},
			"usageMetadata": map[string]int{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
		})
		entered, release := make(chan struct{}, 1), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: historyArchiveRoundTripper(func(request *http.Request) (*http.Response, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return transport.RoundTrip(request)
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		})}})
		Expect(err).NotTo(HaveOccurred())
		router := newTestRouter(controllers.Config{TextParser: client})
		adviceRequest := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/strategies/pre-wod-advice", fmt.Sprintf(`{"profile_id":%d,"wod_description":"Push Jerk","movements":["Push Jerk"]}`, profile.ID), &user)
		adviceResponse := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			router.ServeHTTP(adviceResponse, adviceRequest)
		}()
		DeferCleanup(func() { unblock(); Eventually(done, 5*time.Second).Should(BeClosed()) })
		Eventually(entered, 5*time.Second).Should(Receive())
		archived := historyArchiveRequest(router, user, http.MethodPost, fmt.Sprintf("/api/v1/history/%d/archive", analysis.ID), "")
		Expect(archived.Code).To(Equal(http.StatusOK), archived.Body.String())
		unblock()
		Eventually(done, 5*time.Second).Should(BeClosed())
		Expect(adviceResponse.Code).To(Equal(http.StatusOK), adviceResponse.Body.String())
		Expect(adviceResponse.Body.String()).NotTo(ContainSubstring("stale-ai-advice"))
		var result controllers.PreWODAdviceResponse
		Expect(json.Unmarshal(adviceResponse.Body.Bytes(), &result)).To(Succeed())
		Expect(result.EvidenceStatus).To(Equal("no_history"))
		Expect(result.TargetRPE).To(BeNil())
		Expect(result.OverallFatigueScore).To(BeNil())
		Expect(result.HistoryRevision).To(Equal("1"))
		var usage []db.TokenUsage
		Expect(dbConn.Where("profile_id = ? AND task_type = ?", profile.ID, "strategy:pre-wod").Find(&usage).Error).To(Succeed())
		Expect(usage).To(HaveLen(1))
		Expect(usage[0].TotalTokens).To(Equal(int64(15)))
		Expect(transport.Verify()).To(Succeed())
	})

	It("rejects archiving while original or manual work is active, then archives idempotently once terminal", func() {
		profile, user, router := historyArchiveOwner()
		otherCompleted := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: "WOD-20261029-ARCHIVEOTHERDONE", ProfileID: profile.ID, Status: "COMPLETED", Output: "finished sibling workout",
		})
		session := testhelpers.CreateSession(dbConn, &db.Session{
			SessionID:       "WOD-20261029-ARCHIVECANCEL",
			ProfileID:       profile.ID,
			ActivitySummary: db.NullableJSONDocument(`{"review_state":"running"}`),
		})
		nextAttempt := time.Now().Add(time.Minute)
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: session.SessionID, ProfileID: profile.ID, Status: "PENDING", Output: "saved partial evidence",
			SensorVersion: 3, SensorState: db.SensorStatePending, SensorNextAttemptAt: &nextAttempt,
			SensorProcessing:         db.JSONDocument(`{"request_id":"old-sensor","lease_token":"old-lease","retry_not_before":"2026-10-29T00:00:00Z"}`),
			AnalysisSummary:          db.JSONDocument(`{"run_id":"old-summary","status":"running","last_success":{"overview":"saved summary"}}`),
			AgenticHighlightAnalysis: db.JSONDocument(`{"run_id":"old-agentic","status":"running","items":[{"status":"running"},{"status":"completed","result":{"movement":"Power Clean"}}]}`),
		})
		chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "PENDING", FilePath: "pending.mp4"})
		completedChunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", FilePath: "completed.mp4", Output: "confirmed observation"})
		highlight := db.HighlightResult{SessionID: session.SessionID, ProfileID: profile.ID, Title: "Full Reel", Status: "PROCESSING", Segments: "[]"}
		Expect(dbConn.Create(&highlight).Error).To(Succeed())
		chunkRun := testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{SessionID: session.SessionID, ProfileID: profile.ID, ChunkAnalysisResultID: chunk.ID, Status: "QUEUED"})
		sessionRun := testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{SessionID: session.SessionID, ProfileID: profile.ID, Status: "RUNNING"})
		reviewRun := testhelpers.CreateRecognitionReviewRun(dbConn, &db.RecognitionReviewRun{SessionID: session.SessionID, ProfileID: profile.ID, Status: "RUNNING"})

		path := fmt.Sprintf("/api/v1/history/%d/archive", analysis.ID)
		expectRejected := func(step string) {
			response := historyArchiveRequest(router, user, http.MethodPost, path, "")
			Expect(response.Code).To(Equal(http.StatusConflict), "%s: %s", step, response.Body.String())
			Expect(decodeMapBody(response)["error_code"]).To(Equal("SESSION_PROCESSING_ACTIVE"), step)
			var current db.AnalysisResult
			Expect(dbConn.First(&current, analysis.ID).Error).To(Succeed())
			Expect(current.ArchivedAt).To(BeNil(), step)
			Expect(current.AutomaticProcessingCancelledAt).To(BeNil(), step)
		}

		// 1. Main analysis PENDING blocks archive, while another completed session on the same profile can still be archived.
		expectRejected("pending main analysis")
		siblingResp := historyArchiveRequest(router, user, http.MethodPost, fmt.Sprintf("/api/v1/history/%d/archive", otherCompleted.ID), "")
		Expect(siblingResp.Code).To(Equal(http.StatusOK), siblingResp.Body.String())
		Expect(decodeMapBody(siblingResp)["history_revision"]).To(Equal("1"))

		// 2. Each remaining active sub-task continues to block archiving this session until terminal.
		Expect(dbConn.Model(&analysis).Update("status", "COMPLETED").Error).To(Succeed())
		expectRejected("pending sensor work")

		Expect(dbConn.Model(&analysis).Update("sensor_state", db.SensorStateUploading).Error).To(Succeed())
		expectRejected("running enrichment summary")

		Expect(dbConn.Model(&analysis).Update("analysis_summary", db.JSONDocument(`{"run_id":"old-summary","status":"stale","last_success":{"overview":"saved summary"}}`)).Error).To(Succeed())
		expectRejected("running agentic highlight item")

		Expect(dbConn.Model(&analysis).Update("agentic_highlight_analysis", db.JSONDocument(`{"run_id":"old-agentic","status":"completed","items":[{"status":"completed"},{"status":"completed","result":{"movement":"Power Clean"}}]}`)).Error).To(Succeed())
		expectRejected("pending chunk analysis")

		Expect(dbConn.Model(&chunk).Update("status", "COMPLETED").Error).To(Succeed())
		expectRejected("processing highlight reel")

		Expect(dbConn.Model(&highlight).Update("status", "COMPLETED").Error).To(Succeed())
		expectRejected("queued chunk reanalysis")

		Expect(dbConn.Model(&chunkRun).Update("status", "COMPLETED").Error).To(Succeed())
		expectRejected("running session reanalysis")

		Expect(dbConn.Model(&sessionRun).Update("status", "COMPLETED").Error).To(Succeed())
		expectRejected("running recognition review")

		Expect(dbConn.Model(&reviewRun).Update("status", "COMPLETED").Error).To(Succeed())
		expectRejected("running activity summary review")

		// 3. Once all active worker tasks finish (idle UPLOADING / stale / provisional allowed), archive succeeds and is idempotent.
		Expect(dbConn.Model(&session).Update("activity_summary", db.NullableJSONDocument(`{"review_state":"provisional"}`)).Error).To(Succeed())
		response := historyArchiveRequest(router, user, http.MethodPost, path, "")
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(decodeMapBody(response)["history_revision"]).To(Equal("2"))

		var saved db.AnalysisResult
		Expect(dbConn.First(&saved, analysis.ID).Error).To(Succeed())
		Expect(saved.ArchivedAt).NotTo(BeNil())
		Expect(saved.AutomaticProcessingCancelledAt).NotTo(BeNil())
		Expect(saved.Status).To(Equal("COMPLETED"))
		Expect(saved.Output).To(Equal(analysis.Output))
		Expect(saved.SensorState).To(Equal(db.SensorStateCancelled))
		Expect(saved.SensorVersion).To(Equal(int64(4)))
		Expect(saved.SensorNextAttemptAt).To(BeNil())
		Expect(string(saved.SensorProcessing)).NotTo(ContainSubstring("old-lease"))
		var summary map[string]any
		Expect(json.Unmarshal(saved.AnalysisSummary, &summary)).To(Succeed())
		Expect(summary["status"]).To(Equal("interrupted"))
		Expect(summary["last_success"]).To(HaveKeyWithValue("overview", "saved summary"))

		var retained db.ChunkAnalysisResult
		Expect(dbConn.First(&retained, completedChunk.ID).Error).To(Succeed())
		Expect(retained.Status).To(Equal("COMPLETED"))
		Expect(retained.Output).To(Equal(completedChunk.Output))

		response = historyArchiveRequest(router, user, http.MethodPost, path, "")
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(decodeMapBody(response)["history_revision"]).To(Equal("2"))
		var repeated db.AnalysisResult
		Expect(dbConn.First(&repeated, analysis.ID).Error).To(Succeed())
		Expect(repeated.ArchivedAt).NotTo(BeNil())
		Expect(*repeated.ArchivedAt).To(BeTemporally("==", *saved.ArchivedAt))
		Expect(repeated.SensorVersion).To(Equal(saved.SensorVersion))
	})

	It("preserves a completed sensor result, score and original while preventing automatic resume on restore", func() {
		profile, user, router := historyArchiveOwner()
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-ARCHIVEPRESERVE", ProfileID: profile.ID, Status: "COMPLETED", Output: "original analysis", SessionScore: `{"intensity":80}`, SensorState: db.SensorStateCompleted, SensorVersion: 7, SensorSummary: db.JSONDocument(`{"version":7,"original":"sensor evidence"}`), SensorTimeline: db.JSONDocument(`{"schema_version":1,"points":[]}`), StretchRecommendations: `[{"stretch":"Pigeon Pose","reason":"stored recommendation"}]`})
		for _, action := range []string{"archive", "unarchive"} {
			response := historyArchiveRequest(router, user, http.MethodPost, fmt.Sprintf("/api/v1/history/%d/%s", analysis.ID, action), "")
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		}
		var saved db.AnalysisResult
		Expect(dbConn.First(&saved, analysis.ID).Error).To(Succeed())
		Expect(saved.ArchivedAt).To(BeNil())
		Expect(saved.AutomaticProcessingCancelledAt).NotTo(BeNil())
		Expect(saved.Status).To(Equal("COMPLETED"))
		Expect(saved.Output).To(Equal(analysis.Output))
		Expect(saved.SessionScore).To(Equal(analysis.SessionScore))
		Expect(saved.SensorState).To(Equal(db.SensorStateCompleted))
		Expect(saved.SensorVersion).To(Equal(analysis.SensorVersion))
		Expect(string(saved.SensorSummary)).To(MatchJSON(string(analysis.SensorSummary)))
		Expect(string(saved.SensorTimeline)).To(MatchJSON(string(analysis.SensorTimeline)))
		Expect(saved.StretchRecommendations).To(Equal(analysis.StretchRecommendations))
	})

	It("retracts and restores current readiness, training load, benchmark and related WOD inputs", func() {
		profile, user, router := historyArchiveOwner()
		at := time.Now().UTC().Add(-time.Hour)
		effort, source := 8, "session_ulid"
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261029-ARCHIVECONSUMERS", ProfileID: profile.ID, WorkoutType: "wod", WODEvaluationVersion: 1, Effort: &effort})
		progress, progressRaw := progressFixture("archive-consumer-source")
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-ARCHIVECONSUMERS", ProfileID: profile.ID, Status: "COMPLETED", AnalysisType: db.AnalysisTypeWOD, WorkoutAt: &at, WorkoutAtSource: &source,
			SessionScore: `{"evaluation_version":1,"intensity":80,"movements":{"Power Clean":{"form":80}},"wod_start":{"seconds":0,"end_seconds":600,"status":"confirmed"}}`, WorkoutProgress: progressRaw, NormalizedWorkout: `[{"movement":"Power Clean","weight_kg":60,"is_main":true}]`})
		testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{ProfileID: profile.ID, AnalysisID: analysis.ID, SessionID: analysis.SessionID, IntervalID: progress.Intervals[0].ID, SourceVersion: progress.SourceVersion, Movement: "Power Clean", StartSecs: 0, EndSecs: 60, Reps: workoutprogress.IntPtr(3), RepsPerMinute: workoutprogress.Float64Ptr(3), Technique: "singles", WorkoutPrescription: "Grace", PerformedAt: &at})
		assertConsumers := func(included int) {
			response := historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/strategies/readiness?profile_id=%d", profile.ID), "")
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			var readiness controllers.ProfileReadinessResponse
			Expect(json.Unmarshal(response.Body.Bytes(), &readiness)).To(Succeed())
			Expect(readiness.RecentWorkouts).To(HaveLen(included))
			if included == 0 {
				Expect(readiness.EvidenceStatus).To(Equal("no_history"))
				Expect(readiness.OverallFatigueScore).To(BeNil())
			} else {
				Expect(readiness.EvidenceStatus).To(Equal("complete"))
				Expect(readiness.OverallFatigueScore).NotTo(BeNil())
			}
			response = historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/wod-training-load?profile_id=%d&timezone=Asia%%2FSeoul", profile.ID), "")
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			var load struct{ Comparison wodeval.Comparison }
			Expect(json.Unmarshal(response.Body.Bytes(), &load)).To(Succeed())
			Expect(load.Comparison.Seven.Included).To(Equal(included))
			response = historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/performance-benchmarks?profile_id=%d&movement=Power%%20Clean", profile.ID), "")
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			var benchmarks struct{ Records []workoutprogress.Record }
			Expect(json.Unmarshal(response.Body.Bytes(), &benchmarks)).To(Succeed())
			Expect(benchmarks.Records).To(HaveLen(included))
			response = historyArchiveRequest(router, user, http.MethodGet, fmt.Sprintf("/api/v1/related-wods?profile_id=%d&movement=Power%%20Clean", profile.ID), "")
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			var related controllers.RelatedWODsResponse
			Expect(json.Unmarshal(response.Body.Bytes(), &related)).To(Succeed())
			Expect(related.Related).To(HaveLen(included))
		}
		assertConsumers(1)
		response := historyArchiveRequest(router, user, http.MethodPost, fmt.Sprintf("/api/v1/history/%d/archive", analysis.ID), "")
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		assertConsumers(0)
		response = historyArchiveRequest(router, user, http.MethodPost, fmt.Sprintf("/api/v1/history/%d/unarchive", analysis.ID), "")
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		assertConsumers(1)
	})

	It("rejects unauthenticated and other users without changing the record", func() {
		profile, user, router := historyArchiveOwner()
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-ARCHIVEOWNER", ProfileID: profile.ID, Status: "PENDING"})
		other := testhelpers.CreateUser(dbConn, &db.User{})
		path := fmt.Sprintf("/api/v1/history/%d/archive", analysis.ID)
		unauthenticated := httptest.NewRecorder()
		router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, path, nil))
		Expect(unauthenticated.Code).To(Equal(http.StatusUnauthorized))
		response := historyArchiveRequest(router, other, http.MethodPost, path, "")
		Expect(response.Code).To(Equal(http.StatusForbidden))
		response = historyArchiveRequest(router, user, http.MethodPost, "/api/v1/history/0/archive", "")
		Expect(response.Code).To(Equal(http.StatusBadRequest))
		var unchanged db.AnalysisResult
		Expect(dbConn.First(&unchanged, analysis.ID).Error).To(Succeed())
		Expect(unchanged.ArchivedAt).To(BeNil())
		Expect(unchanged.AutomaticProcessingCancelledAt).To(BeNil())
	})
})

var _ = Describe("POST /api/v1/history/:id/unarchive", func() {
	It("restores membership only and returns precision-safe idempotent history revisions", func() {
		profile, user, router := historyArchiveOwner()
		at := time.Now().UTC().Add(-time.Hour)
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-ARCHIVERESTORE", ProfileID: profile.ID, Status: "CANCELLED", ArchivedAt: &at})
		Expect(dbConn.Model(&analysis).UpdateColumn("automatic_processing_cancelled_at", at).Error).To(Succeed())
		Expect(dbConn.Table("profiles").Where("id = ?", profile.ID).UpdateColumn("workout_history_revision", int64(9007199254740991)).Error).To(Succeed())
		path := fmt.Sprintf("/api/v1/history/%d/unarchive", analysis.ID)
		for i := 0; i < 2; i++ {
			response := historyArchiveRequest(router, user, http.MethodPost, path, "")
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			Expect(decodeMapBody(response)["history_revision"]).To(Equal("9007199254740992"))
		}
		var saved db.AnalysisResult
		Expect(dbConn.First(&saved, analysis.ID).Error).To(Succeed())
		Expect(saved.ArchivedAt).To(BeNil())
		Expect(saved.Status).To(Equal("CANCELLED"))
		Expect(saved.AutomaticProcessingCancelledAt).NotTo(BeNil())
		Expect(*saved.AutomaticProcessingCancelledAt).To(BeTemporally("~", at, time.Microsecond))
	})

	It("requires the record owner to restore", func() {
		profile, _, router := historyArchiveOwner()
		at := time.Now().UTC()
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-RESTOREOWNER", ProfileID: profile.ID, Status: "COMPLETED", ArchivedAt: &at})
		other := testhelpers.CreateUser(dbConn, &db.User{})
		response := historyArchiveRequest(router, other, http.MethodPost, fmt.Sprintf("/api/v1/history/%d/unarchive", analysis.ID), "")
		Expect(response.Code).To(Equal(http.StatusForbidden))
		var saved db.AnalysisResult
		Expect(dbConn.First(&saved, analysis.ID).Error).To(Succeed())
		Expect(saved.ArchivedAt).NotTo(BeNil())
	})
})

var _ = Describe("POST /api/v1/sessions/archive-states", func() {
	It("returns one ordered state per unique ID without revealing foreign metadata", func() {
		profile, user, router := historyArchiveOwner()
		at := time.Now().UTC()
		active := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261029-STATEACTIVE", ProfileID: profile.ID})
		archived := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261029-STATEARCHIVED", ProfileID: profile.ID})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: archived.SessionID, ProfileID: profile.ID, Status: "COMPLETED", ArchivedAt: &at})
		legacy := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "P1-WOD-2025-01-02-08-30", ProfileID: profile.ID})
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		foreign := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: "WOD-20261029-STATEFOREIGN", ProfileID: other.ID, Status: "COMPLETED", ArchivedAt: &at})
		testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{SessionID: "WOD-20261029-STATECHUNK", ProfileID: profile.ID, Status: "COMPLETED"})
		ids := []string{active.SessionID, archived.SessionID, "WOD-20261029-STATEMISSING", foreign.SessionID, legacy.SessionID, "WOD-20261029-STATECHUNK", active.SessionID}
		body, err := json.Marshal(map[string]any{"profile_id": profile.ID, "session_ids": ids})
		Expect(err).NotTo(HaveOccurred())
		response := historyArchiveRequest(router, user, http.MethodPost, "/api/v1/sessions/archive-states", string(body))
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		var payload struct {
			Revision  string    `json:"history_revision"`
			CheckedAt time.Time `json:"checked_at"`
			States    []struct {
				SessionID  string     `json:"session_id"`
				State      string     `json:"state"`
				ArchivedAt *time.Time `json:"archived_at"`
			} `json:"states"`
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.Revision).To(Equal("0"))
		Expect(payload.CheckedAt).NotTo(BeZero())
		Expect(payload.States).To(HaveLen(6))
		for index, state := range []string{"active", "archived", "not_registered", "unavailable", "active", "active"} {
			Expect(payload.States[index].SessionID).To(Equal(ids[index]))
			Expect(payload.States[index].State).To(Equal(state))
		}
		Expect(payload.States[1].ArchivedAt).NotTo(BeNil())
		Expect(payload.States[3].ArchivedAt).To(BeNil())
	})

	It("accepts 100 IDs but rejects larger, empty, unsafe or unauthorized batches", func() {
		profile, user, router := historyArchiveOwner()
		ids := []string{}
		for index := 0; index < 100; index++ {
			ids = append(ids, "session-missing-"+strconv.Itoa(index))
		}
		request := func(values []string, as db.User) *httptest.ResponseRecorder {
			body, err := json.Marshal(map[string]any{"profile_id": profile.ID, "session_ids": values})
			Expect(err).NotTo(HaveOccurred())
			return historyArchiveRequest(router, as, http.MethodPost, "/api/v1/sessions/archive-states", string(body))
		}
		response := request(ids, user)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(decodeMapBody(response)["states"]).To(HaveLen(100))
		for _, values := range [][]string{append(append([]string{}, ids...), "session-over-limit"), {}, {"../unsafe"}} {
			Expect(request(values, user).Code).To(Equal(http.StatusBadRequest))
		}
		other := testhelpers.CreateUser(dbConn, &db.User{})
		Expect(request(ids[:1], other).Code).To(Equal(http.StatusForbidden))
		unauthenticated := httptest.NewRecorder()
		router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/archive-states", nil))
		Expect(unauthenticated.Code).To(Equal(http.StatusUnauthorized))
	})
})
