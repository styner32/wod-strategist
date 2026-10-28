package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/recognitionreview"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

type recognitionFixtureData struct {
	router    *gin.Engine
	profile   db.Profile
	user      db.User
	session   db.Session
	progress  workoutprogress.Progress
	record    db.PerformanceRecord
	transport *testhelpers.MockTransport
}

func newRecognitionFixture() recognitionFixtureData {
	profile, user := progressOwner()
	testhelpers.CleanupQueue(inspector)
	session := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261028-RECOGNITION", ProfileID: profile.ID, WorkoutType: "wod"})
	p, raw := progressFixture("review-source")
	analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: session.SessionID, ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: raw, Output: "original output"})
	record := testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{ProfileID: profile.ID, AnalysisID: analysis.ID, SessionID: session.SessionID, IntervalID: p.Intervals[0].ID, SourceVersion: p.SourceVersion, Movement: "Power Clean", StartSecs: 0, EndSecs: 60, Reps: 2, OriginalCandidate: db.JSONDocument(`{"movement":"User supplied candidate must not be read"}`)})
	transport := testhelpers.NewMockTransport()
	storage, err := testhelpers.NewStorageClient("test-bucket", transport)
	Expect(err).NotTo(HaveOccurred())
	router := newTestRouterWithAuthService(controllers.Config{StorageClient: storage, BucketName: "test-bucket", QueueClient: testhelpers.NewQueueClient()})
	return recognitionFixtureData{router: router, profile: profile, user: user, session: session, progress: p, record: record, transport: transport}
}

func (f recognitionFixtureData) path(suffix string) string {
	return "/api/v1/sessions/" + f.session.SessionID + suffix
}

func (f recognitionFixtureData) getEvaluation() recognitionreview.Evaluation {
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, newAuthorizedJSONRequest(http.MethodGet, f.path("/recognition-evaluation"), "", &f.user))
	Expect(w.Code).To(Equal(200), w.Body.String())
	var out recognitionreview.Evaluation
	Expect(json.Unmarshal(w.Body.Bytes(), &out)).To(Succeed())
	return out
}

func (f recognitionFixtureData) request(key string) controllers.CreateRecognitionReviewRequest {
	e := f.getEvaluation()
	return controllers.CreateRecognitionReviewRequest{RecordIDs: []uint{f.record.ID}, StartSecs: 0, EndSecs: 60, SourceVersion: e.SourceVersion, EvaluationFingerprint: e.Fingerprint, ClientRequestID: key}
}

func (f recognitionFixtureData) post(req controllers.CreateRecognitionReviewRequest) *httptest.ResponseRecorder {
	raw, err := json.Marshal(req)
	Expect(err).NotTo(HaveOccurred())
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, newAuthorizedJSONRequest(http.MethodPost, f.path("/recognition-reviews"), string(raw), &f.user))
	return w
}

func (f recognitionFixtureData) mockCurrentSource(generation string) {
	object := fmt.Sprintf("videos/%d/%s/merged.mp4", f.profile.ID, f.session.SessionID)
	testhelpers.MockGCSListObjects(f.transport, "test-bucket", object, []string{object})
	f.transport.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + url.PathEscape(object)).Reply(200).JSON(map[string]any{"name": object, "generation": generation, "size": "1000"})
}

var _ = Describe("GET /api/v1/sessions/:session_id/recognition-evaluation", func() {
	It("compares current raw observations without calling storage or AI and ignores corrected candidates", func() {
		f := newRecognitionFixture()
		e := f.getEvaluation()
		Expect(e.Summary.Total).To(Equal(1))
		Expect(e.Summary.Mismatch).To(Equal(1))
		Expect(*e.Records[0].ObservedCompleteReps).To(Equal(1))
		Expect(*e.Records[0].RepsDelta).To(Equal(-1))
		Expect(e.Records[0].PredictedMovements[0].Movement).To(Equal("Power Clean"))
		Expect(f.transport.Requests()).To(BeEmpty())
	})
	It("requires authentication and owned session scope", func() {
		f := newRecognitionFixture()
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, f.path("/recognition-evaluation"), nil))
		Expect(w.Code).To(Equal(401))
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, other.UserID).Error).To(Succeed())
		w = httptest.NewRecorder()
		f.router.ServeHTTP(w, newAuthorizedJSONRequest(http.MethodGet, f.path("/recognition-evaluation"), "", &user))
		Expect(w.Code).To(Equal(403))
	})
})

var _ = Describe("POST /api/v1/sessions/:session_id/recognition-reviews", func() {
	It("snapshots source generation and confirmations, queues once, and preserves production data", func() {
		f := newRecognitionFixture()
		req := f.request("create-review")
		f.mockCurrentSource("42")
		w := f.post(req)
		Expect(w.Code).To(Equal(202), w.Body.String())
		var out controllers.CreateRecognitionReviewResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &out)).To(Succeed())
		var run db.RecognitionReviewRun
		Expect(dbConn.First(&run, out.RunID).Error).To(Succeed())
		Expect(run.SourceGeneration).To(Equal(int64(42)))
		Expect(run.SourceVersion).To(Equal(f.progress.SourceVersion))
		Expect(run.Model).NotTo(BeEmpty())
		Expect(string(run.Snapshot)).To(ContainSubstring("original output"))
		Expect(string(run.Snapshot)).NotTo(ContainSubstring("User supplied candidate"))
		w = f.post(req)
		Expect(w.Code).To(Equal(202))
		var repeated controllers.CreateRecognitionReviewResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &repeated)).To(Succeed())
		Expect(repeated.RunID).To(Equal(out.RunID))
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal("recognition:review"))
		var analysis db.AnalysisResult
		Expect(dbConn.Where("session_id = ?", f.session.SessionID).First(&analysis).Error).To(Succeed())
		Expect(analysis.Output).To(Equal("original output"))
		Expect(string(analysis.WorkoutProgress)).To(ContainSubstring(f.progress.SourceVersion))
		var record db.PerformanceRecord
		Expect(dbConn.First(&record, f.record.ID).Error).To(Succeed())
		Expect(record.Reps).To(Equal(2))
		Expect(f.transport.Verify()).To(Succeed())
	})
	It("rejects stale confirmation fingerprints before reading video metadata", func() {
		f := newRecognitionFixture()
		req := f.request("stale-review")
		Expect(dbConn.Model(&db.PerformanceRecord{}).Where("id = ?", f.record.ID).Update("reps", 3).Error).To(Succeed())
		w := f.post(req)
		Expect(w.Code).To(Equal(409), w.Body.String())
		Expect(f.transport.Requests()).To(BeEmpty())
	})
	It("rejects a crossing record, unknown record, duplicate IDs, and arbitrary input fields", func() {
		f := newRecognitionFixture()
		req := f.request("invalid-range")
		req.StartSecs = 20
		Expect(f.post(req).Code).To(Equal(400))
		req = f.request("unknown-record")
		req.RecordIDs = []uint{9999}
		Expect(f.post(req).Code).To(Equal(400))
		req = f.request("duplicate-record")
		req.RecordIDs = []uint{f.record.ID, f.record.ID}
		Expect(f.post(req).Code).To(Equal(400))
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, newAuthorizedJSONRequest(http.MethodPost, f.path("/recognition-reviews"), `{"model":"attacker","source_gcs_uri":"gs://other/video.mp4"}`, &f.user))
		Expect(w.Code).To(Equal(400))
		Expect(f.transport.Requests()).To(BeEmpty())
	})
	It("rejects changed idempotent payload and another active run", func() {
		f := newRecognitionFixture()
		req := f.request("idempotent-review")
		f.mockCurrentSource("42")
		Expect(f.post(req).Code).To(Equal(202))
		req.EndSecs = 61
		Expect(f.post(req).Code).To(Equal(409))
		req = f.request("second-review")
		Expect(f.post(req).Code).To(Equal(409))
		Expect(f.transport.Verify()).To(Succeed())
	})
	It("forbids another profile from creating a diagnosis", func() {
		f := newRecognitionFixture()
		req := f.request("wrong-owner")
		raw, _ := json.Marshal(req)
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, other.UserID).Error).To(Succeed())
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, newAuthorizedJSONRequest(http.MethodPost, f.path("/recognition-reviews"), string(raw), &user))
		Expect(w.Code).To(Equal(403))
		Expect(f.transport.Requests()).To(BeEmpty())
	})
})

var _ = Describe("GET /api/v1/sessions/:session_id/recognition-reviews", func() {
	It("lists metadata and flags changed media without exposing snapshots or provider file names", func() {
		f := newRecognitionFixture()
		req := f.request("history-review")
		f.mockCurrentSource("42")
		Expect(f.post(req).Code).To(Equal(202))
		var run db.RecognitionReviewRun
		Expect(dbConn.First(&run).Error).To(Succeed())
		Expect(dbConn.Model(&run).Update("batches", db.JSONDocument(`[{"id":1,"record_id":1,"status":"RUNNING","gemini_file_name":"files/private","findings":[{"difference":"private detail"}]}]`)).Error).To(Succeed())
		f.mockCurrentSource("43")
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, newAuthorizedJSONRequest(http.MethodGet, f.path("/recognition-reviews"), "", &f.user))
		Expect(w.Code).To(Equal(200))
		var out controllers.ListRecognitionReviewsResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Runs).To(HaveLen(1))
		Expect(out.Runs[0].Outdated).To(BeTrue())
		Expect(out.ActiveRunID).NotTo(BeNil())
		Expect(out.Runs[0].Evaluation).To(BeNil())
		Expect(out.Runs[0].Batches[0].Findings).To(BeEmpty())
		Expect(w.Body.String()).NotTo(ContainSubstring("private"))
		Expect(w.Body.String()).NotTo(ContainSubstring("gemini_file_name"))
		Expect(f.transport.Verify()).To(Succeed())
	})
})

var _ = Describe("GET /api/v1/sessions/:session_id/recognition-reviews/:run_id", func() {
	It("returns the historical comparison and flags subsequent confirmation edits", func() {
		f := newRecognitionFixture()
		req := f.request("detail-review")
		f.mockCurrentSource("42")
		w := f.post(req)
		Expect(w.Code).To(Equal(202))
		var created controllers.CreateRecognitionReviewResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &created)).To(Succeed())
		Expect(dbConn.Model(&db.PerformanceRecord{}).Where("id = ?", f.record.ID).Update("reps", 4).Error).To(Succeed())
		f.mockCurrentSource("42")
		w = httptest.NewRecorder()
		f.router.ServeHTTP(w, newAuthorizedJSONRequest(http.MethodGet, f.path(fmt.Sprintf("/recognition-reviews/%d", created.RunID)), "", &f.user))
		Expect(w.Code).To(Equal(200), w.Body.String())
		var out controllers.RecognitionReviewRunResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Outdated).To(BeTrue())
		Expect(out.Evaluation).NotTo(BeNil())
		Expect(out.Evaluation.Records[0].Record.Reps).To(Equal(2))
		Expect(f.transport.Verify()).To(Succeed())
	})
	It("scopes run IDs to the owned session", func() {
		f := newRecognitionFixture()
		run := testhelpers.CreateRecognitionReviewRun(dbConn, &db.RecognitionReviewRun{SessionID: "WOD-OTHER", ProfileID: f.profile.ID})
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, newAuthorizedJSONRequest(http.MethodGet, f.path(fmt.Sprintf("/recognition-reviews/%d", run.ID)), "", &f.user))
		Expect(w.Code).To(Equal(404))
		Expect(strings.Contains(w.Body.String(), "snapshot")).To(BeFalse())
	})
})
