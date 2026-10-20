package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

func progressFixture(source string) (workoutprogress.Progress, db.NullableJSONDocument) {
	p := workoutprogress.Build([]workoutprogress.Interval{{Start: 0, End: 60, Activity: "exercise", Movement: "Power Clean", ObservationRefs: []string{"segment"}, RepEvents: []activity.Observation{{Start: 1, End: 3, Movement: "Power Clean", Unit: "reps", Complete: true, Evidence: "floor to rack"}}}}, nil, source)
	raw, err := json.Marshal(p)
	Expect(err).NotTo(HaveOccurred())
	return p, db.NullableJSONDocument(raw)
}

func progressOwner() (db.Profile, db.User) {
	ensureTestDB()
	testhelpers.CleanupDB(dbConn)
	profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
	var user db.User
	Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
	return profile, user
}

var _ = Describe("GET /api/v1/sessions/:session_id/workout-progress", func() {
	It("returns SQL-null progress for an unevaluated session and forbids another owner", func() {
		profile, user := progressOwner()
		s := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WARMUP-20261013-PROGRESS", ProfileID: profile.ID, WorkoutType: "warmup"})
		router := newTestRouterWithAuthService(controllers.Config{})
		path := "/api/v1/sessions/" + s.SessionID + "/workout-progress"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &user))
		Expect(response.Code).To(Equal(200))
		Expect(decodeMapBody(response)["progress"]).To(BeNil())
		Expect(decodeMapBody(response)["records"]).To(BeEmpty())
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, other.UserID).Error).To(Succeed())
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &otherUser))
		Expect(response.Code).To(Equal(403))
	})
})

var _ = Describe("PUT /api/v1/sessions/:session_id/performance-records", func() {
	It("confirms five repetitions over 60 seconds including directly observed rest", func() {
		profile, user := progressOwner()
		cycle := func(a, b float64) activity.Observation {
			return activity.Observation{Start: a, End: b, Movement: "Power Clean", Unit: "reps", Complete: true, Evidence: "floor to front rack"}
		}
		p := workoutprogress.Build([]workoutprogress.Interval{{Start: 0, End: 20, Activity: "exercise", Movement: "Power Clean", ObservationRefs: []string{"first"}, RepEvents: []activity.Observation{cycle(1, 3), cycle(6, 8), cycle(12, 14)}}, {Start: 20, End: 40, Activity: "rest", ObservationRefs: []string{"rest"}}, {Start: 40, End: 60, Activity: "exercise", Movement: "Power Clean", ObservationRefs: []string{"last"}, RepEvents: []activity.Observation{cycle(44, 47), cycle(49, 52)}}}, nil, "repeat-video")
		raw, err := json.Marshal(p)
		Expect(err).NotTo(HaveOccurred())
		sid := "WOD-20261013-RESTWINDOW"
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: sid, ProfileID: profile.ID, WorkoutType: "wod"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: db.NullableJSONDocument(raw)})
		body := fmt.Sprintf(`{"source_version":%q,"records":[{"interval_id":%q,"start_secs":0,"end_secs":60,"reps":5,"load":{"value":135,"unit":"lb","basis":"total"},"measurement":"window","technique":"singles"}]}`, p.SourceVersion, p.Intervals[0].ID)
		router := newTestRouterWithAuthService(controllers.Config{})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, "/api/v1/sessions/"+sid+"/performance-records", body, &user))
		Expect(response.Code).To(Equal(200), response.Body.String())
		var row db.PerformanceRecord
		Expect(dbConn.Where("profile_id = ? AND session_id = ?", profile.ID, sid).First(&row).Error).To(Succeed())
		Expect(row.RepsPerMinute).To(Equal(5.0))
		Expect(row.ElapsedSeconds).To(Equal(60.0))
		Expect(row.RateBasis).To(Equal("actual_60_second_window"))
		Expect(row.MedianCycleSeconds).NotTo(BeNil())
		Expect(*row.MedianCycleSeconds).To(Equal(2.0))
		var confirmed controllers.WorkoutProgressResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &confirmed)).To(Succeed())
		Expect(confirmed.Records).To(HaveLen(1))
		Expect(confirmed.Records[0].RestComparisons).To(HaveLen(1))
		Expect(*confirmed.Records[0].RestComparisons[0].CycleChangePercent).To(Equal(50.0))
		Expect(*confirmed.Records[0].RestComparisons[0].RestElapsedSeconds).To(Equal(20.0))
		getResponse := httptest.NewRecorder()
		router.ServeHTTP(getResponse, newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/workout-progress", "", &user))
		Expect(getResponse.Code).To(Equal(200))
		var reread controllers.WorkoutProgressResponse
		Expect(json.Unmarshal(getResponse.Body.Bytes(), &reread)).To(Succeed())
		Expect(reread.Records[0].RestComparisons).To(Equal(confirmed.Records[0].RestComparisons))
		var candidate struct {
			Intervals []workoutprogress.Interval `json:"intervals"`
		}
		Expect(json.Unmarshal(row.OriginalCandidate, &candidate)).To(Succeed())
		Expect(candidate.Intervals).To(HaveLen(3))
		Expect(candidate.Intervals[1].Activity).To(Equal("rest"))
		body = fmt.Sprintf(`{"source_version":%q,"records":[{"interval_id":%q,"start_secs":0,"end_secs":60,"reps":6,"load":{"value":135,"unit":"lb","basis":"total"},"measurement":"window","technique":"singles"}]}`, p.SourceVersion, p.Intervals[0].ID)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, "/api/v1/sessions/"+sid+"/performance-records", body, &user))
		Expect(response.Code).To(Equal(200))
		Expect(json.Unmarshal(response.Body.Bytes(), &confirmed)).To(Succeed())
		Expect(confirmed.Records[0].RestComparisons).To(HaveLen(1))
		Expect(confirmed.Records[0].RestComparisons[0].BeforeMedianCycleSeconds).To(BeNil())
		Expect(confirmed.Records[0].RestComparisons[0].AfterMedianCycleSeconds).To(BeNil())
		Expect(confirmed.Records[0].RestComparisons[0].CycleChangePercent).To(BeNil())
		Expect(*confirmed.Records[0].RestComparisons[0].RestElapsedSeconds).To(Equal(20.0))
		Expect(confirmed.Progress.Intervals[0].RepEvents).To(HaveLen(3))
		Expect(confirmed.Progress.Intervals[2].RepEvents).To(HaveLen(2))
	})
	It("resolves a known legacy workout date instead of using confirmation time", func() {
		profile, user := progressOwner()
		p, raw := progressFixture("legacy-video")
		location, err := time.LoadLocation("Asia/Seoul")
		Expect(err).NotTo(HaveOccurred())
		legacyAt := time.Now().Add(-24 * time.Hour).In(location).Truncate(time.Minute)
		sid := "P321-WOD-" + legacyAt.Format("2006-01-02-15-04")
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: sid, ProfileID: profile.ID, WorkoutType: "wod"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: raw})
		body := fmt.Sprintf(`{"source_version":%q,"records":[{"interval_id":%q,"start_secs":0,"end_secs":60,"reps":3,"load":null,"measurement":"window","technique":"singles"}]}`, p.SourceVersion, p.Intervals[0].ID)
		router := newTestRouterWithAuthService(controllers.Config{})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, "/api/v1/sessions/"+sid+"/performance-records", body, &user))
		Expect(response.Code).To(Equal(200), response.Body.String())
		var row db.PerformanceRecord
		Expect(dbConn.Where("profile_id = ? AND session_id = ?", profile.ID, sid).First(&row).Error).To(Succeed())
		Expect(row.PerformedAt).NotTo(BeNil())
		Expect(row.PerformedAt.Equal(legacyAt)).To(BeTrue())
		Expect(row.PerformedAtSource).NotTo(BeNil())
		Expect(*row.PerformedAtSource).To(Equal("legacy_local_time"))
		Expect(row.PerformedAt.Before(row.ConfirmedAt)).To(BeTrue())
	})
	It("records the session WOD description as the prescription when the analysis row has none", func() {
		profile, user := progressOwner()
		p, raw := progressFixture("video")
		sid := "WOD-20261013-PRESCRIPTION"
		testhelpers.CreateSession(dbConn, &db.Session{SessionID: sid, ProfileID: profile.ID, WorkoutType: "wod", WODDescription: "30 Power Cleans (135 lb)"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: raw})
		body := fmt.Sprintf(`{"source_version":%q,"records":[{"interval_id":%q,"start_secs":0,"end_secs":60,"reps":3,"load":{"value":135,"unit":"lb","basis":"total"},"measurement":"window","technique":"singles"}]}`, p.SourceVersion, p.Intervals[0].ID)
		router := newTestRouterWithAuthService(controllers.Config{})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, "/api/v1/sessions/"+sid+"/performance-records", body, &user))
		Expect(response.Code).To(Equal(200), response.Body.String())
		var row db.PerformanceRecord
		Expect(dbConn.Where("profile_id = ? AND session_id = ?", profile.ID, sid).First(&row).Error).To(Succeed())
		Expect(row.WorkoutPrescription).To(Equal("30 Power Cleans (135 lb)"), "the confirmed record carries the prescription shown to the user")
	})
	It("upserts a confirmed interval without touching raw observations and rejects stale sources", func() {
		profile, user := progressOwner()
		p, raw := progressFixture("video")
		s := testhelpers.CreateSession(dbConn, &db.Session{SessionID: "WOD-20261013-CONFIRM", ProfileID: profile.ID, WorkoutType: "wod"})
		a := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: s.SessionID, ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: raw})
		router := newTestRouterWithAuthService(controllers.Config{})
		path := "/api/v1/sessions/" + s.SessionID + "/performance-records"
		for _, reps := range []int{3, 5} {
			body := fmt.Sprintf(`{"source_version":%q,"records":[{"interval_id":%q,"start_secs":0,"end_secs":60,"reps":%d,"load":{"value":135,"unit":"lb","basis":"total"},"measurement":"window","technique":"singles"}]}`, p.SourceVersion, p.Intervals[0].ID, reps)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, body, &user))
			Expect(response.Code).To(Equal(200), response.Body.String())
		}
		var rows []db.PerformanceRecord
		Expect(dbConn.Where("profile_id = ? AND session_id = ?", profile.ID, s.SessionID).Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Reps).To(Equal(5))
		Expect(rows[0].MedianCycleSeconds).To(BeNil())
		Expect(rows[0].LoadKG).NotTo(BeNil())
		Expect(*rows[0].LoadKG).To(Equal(61.2))
		Expect(rows[0].PerformedAt).NotTo(BeNil())
		Expect(rows[0].PerformedAtSource).NotTo(BeNil())
		Expect(*rows[0].PerformedAtSource).To(Equal("session_created_fallback"))
		var stored db.AnalysisResult
		Expect(dbConn.First(&stored, a.ID).Error).To(Succeed())
		var before, after any
		Expect(json.Unmarshal(raw, &before)).To(Succeed())
		Expect(json.Unmarshal(stored.WorkoutProgress, &after)).To(Succeed())
		Expect(after).To(Equal(before))
		body := fmt.Sprintf(`{"source_version":"stale","records":[{"interval_id":%q,"start_secs":0,"end_secs":60,"reps":3,"load":null,"measurement":"window","technique":"unspecified"}]}`, p.Intervals[0].ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, body, &user))
		Expect(response.Code).To(Equal(409))
		body = fmt.Sprintf(`{"source_version":%q,"records":[{"interval_id":%q,"start_secs":0,"end_secs":61,"reps":3,"load":null,"measurement":"window","technique":"unspecified"}]}`, p.SourceVersion, p.Intervals[0].ID)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, body, &user))
		Expect(response.Code).To(Equal(400))
		body = fmt.Sprintf(`{"source_version":%q,"records":[{"interval_id":%q,"start_secs":0,"end_secs":60,"reps":3,"measurement":"window","technique":"unspecified"}]}`, p.SourceVersion, p.Intervals[0].ID)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, body, &user))
		Expect(response.Code).To(Equal(400))
		body = fmt.Sprintf(`{"source_version":%q,"records":[{"interval_id":%q,"start_secs":0,"end_secs":60,"reps":3,"load":null,"measurement":"window","technique":"unspecified"}]}`, p.SourceVersion, p.Intervals[0].ID)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodPut, path, body, &user))
		Expect(response.Code).To(Equal(200))
		Expect(dbConn.First(&rows[0], rows[0].ID).Error).To(Succeed())
		Expect(rows[0].LoadValue).To(BeNil())
	})
})

var _ = Describe("GET /api/v1/performance-benchmarks", func() {
	It("excludes stale sources, pending candidates and archives while comparing current confirmed records", func() {
		profile, user := progressOwner()
		now := time.Now().UTC()
		previous := now.Add(-time.Hour)
		loadValue, loadUnit, loadBasis, kg := 135.0, "lb", "total", 61.2
		for i := 0; i < 4; i++ {
			p, raw := progressFixture(fmt.Sprintf("video-%d", i))
			sid := fmt.Sprintf("WOD-20261013-BENCH%d", i)
			a := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: raw})
			source := p.SourceVersion
			if i == 2 {
				source = "stale"
			}
			at := previous
			reps := 3
			if i == 1 {
				at = now
				reps = 5
			}
			if i == 3 {
				Expect(dbConn.Model(&a).Update("archived_at", now).Error).To(Succeed())
			}
			testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{ProfileID: profile.ID, AnalysisID: a.ID, SessionID: sid, IntervalID: p.Intervals[0].ID, SourceVersion: source, Movement: "Power Clean", StartSecs: 0, EndSecs: 60, Reps: reps, LoadValue: &loadValue, LoadUnit: &loadUnit, LoadBasis: &loadBasis, LoadKG: &kg, Measurement: "window", Technique: "singles", WorkoutPrescription: "Grace", RepsPerMinute: float64(reps), PerformedAt: &at})
		}
		testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{SessionID: "WOD-20261013-BENCH0", ProfileID: profile.ID, Status: "COMPLETED", WorkoutProgress: db.NullableJSONDocument(`{"source_version":"candidate"}`)})
		router := newTestRouterWithAuthService(controllers.Config{})
		path := fmt.Sprintf("/api/v1/performance-benchmarks?profile_id=%d&movement=Power%%20Clean", profile.ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &user))
		Expect(response.Code).To(Equal(200), response.Body.String())
		var body struct {
			Records     []workoutprogress.Record
			Comparisons []workoutprogress.Comparison
			Best        []workoutprogress.Record
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Records).To(HaveLen(2))
		Expect(body.Comparisons).To(HaveLen(1))
		Expect(*body.Comparisons[0].RateChangePercent).To(BeNumerically("~", 66.666666, 0.0001))
		Expect(body.Best).To(HaveLen(1))
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, other.UserID).Error).To(Succeed())
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &otherUser))
		Expect(response.Code).To(Equal(403))
	})
})
