package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/timeline"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

var _ = Describe("GET /api/v1/sessions/:session_id/share-context", func() {
	It("requires authentication, an owned profile and the exact session/profile pair", func() {
		profile, user := progressOwner()
		_, raw := progressFixture("share-video")
		session := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profile.ID, SessionID: "WOD-20261031-SHAREACCESS", WorkoutType: "wod"})
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: profile.ID, SessionID: session.SessionID, Status: "COMPLETED", WorkoutProgress: raw})
		router := newTestRouterWithAuthService(controllers.Config{})
		path := fmt.Sprintf("/api/v1/sessions/%s/share-context?profile_id=%d", session.SessionID, profile.ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		Expect(response.Code).To(Equal(401))
		other := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var otherUser db.User
		Expect(dbConn.First(&otherUser, other.UserID).Error).To(Succeed())
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &otherUser))
		Expect(response.Code).To(Equal(403))
		ownOtherProfile := testhelpers.CreateProfile(dbConn, &db.Profile{UserID: user.ID})
		response = httptest.NewRecorder()
		wrongPair := fmt.Sprintf("/api/v1/sessions/%s/share-context?profile_id=%d", session.SessionID, ownOtherProfile.ID)
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, wrongPair, "", &user))
		Expect(response.Code).To(Equal(403))
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path+"&timezone=Invalid%2FZone", "", &user))
		Expect(response.Code).To(Equal(400))
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, "/api/v1/sessions/WOD-20261031-NOSUCHSESSION/share-context?profile_id="+fmt.Sprint(profile.ID), "", &user))
		Expect(response.Code).To(Equal(404))
	})

	It("rejects archived, incomplete and injury-only analysis", func() {
		profile, user := progressOwner()
		session := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profile.ID, SessionID: "WOD-20261031-SHARESTATE", WorkoutType: "wod"})
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: profile.ID, SessionID: session.SessionID, Status: "PENDING"})
		router := newTestRouterWithAuthService(controllers.Config{})
		path := fmt.Sprintf("/api/v1/sessions/%s/share-context?profile_id=%d", session.SessionID, profile.ID)
		for _, updates := range []map[string]any{
			{"status": "PENDING"},
			{"status": "COMPLETED", "analysis_type": "injury_supplement"},
			{"analysis_type": db.AnalysisTypeWOD, "archived_at": time.Now()},
		} {
			Expect(dbConn.Model(&analysis).Updates(updates).Error).To(Succeed())
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &user))
			Expect(response.Code).To(Equal(409), response.Body.String())
			Expect(response.Header().Get("Cache-Control")).To(Equal("no-store"))
		}
	})

	It("shares every session type and analysis-only legacy history with nonnull empty arrays", func() {
		profile, user := progressOwner()
		router := newTestRouterWithAuthService(controllers.Config{})
		for _, kind := range []string{"wod", "warmup", "accessory", "cooldown"} {
			sid := "WOD-20261031-SHARE" + kind
			testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profile.ID, SessionID: sid, WorkoutType: kind})
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: profile.ID, SessionID: sid, Status: "COMPLETED"})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/share-context?profile_id=%d", sid, profile.ID), "", &user))
			Expect(response.Code).To(Equal(200), response.Body.String())
			var out controllers.WorkoutShareContextResponse
			Expect(json.Unmarshal(response.Body.Bytes(), &out)).To(Succeed())
			Expect(out.Session.WorkoutType).To(Equal(kind))
			Expect(out.Session.PerformedAt).To(BeNil())
			Expect(out.ConfirmedRecords).NotTo(BeNil())
			Expect(out.Comparisons).NotTo(BeNil())
			Expect(out.BaselineRecords).NotTo(BeNil())
			Expect(out.Observations).NotTo(BeNil())
			Expect(out.Unassessed).NotTo(BeNil())
			Expect(out.SensorSummary).To(BeNil())
			Expect(out.Warnings).To(ContainElements("workout_time_unknown", "observations_unavailable", "no_confirmed_records", "no_prior_comparison"))
		}
		sid := "P123-WOD-2026-10-01-09-00"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: profile.ID, SessionID: sid, Status: "COMPLETED"})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/share-context?profile_id=%d", sid, profile.ID), "", &user))
		Expect(response.Code).To(Equal(200), response.Body.String())
		var out controllers.WorkoutShareContextResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Session.PerformedAtSource).To(HaveValue(Equal(timeline.WorkoutAtSourceLegacyLocalTime)))
		Expect(out.Session.PerformedAt).NotTo(BeNil())
	})

	It("includes prior-session comparison dependencies while excluding stale, archived, future and unreliable history", func() {
		profile, user := progressOwner()
		now := time.Now().UTC().Add(-time.Hour)
		source := timeline.WorkoutAtSourceSessionULID
		progress, raw := progressFixture("share-current")
		session := testhelpers.CreateSession(dbConn, &db.Session{ProfileID: profile.ID, SessionID: "WOD-20261031-SHARECOMPARE", WorkoutType: "wod", Effort: workoutprogress.IntPtr(8), WODDescription: "Grace"})
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: profile.ID, SessionID: session.SessionID, Status: "COMPLETED", WorkoutProgress: raw, WorkoutAt: &now, WorkoutAtSource: &source})
		createRecord := func(a db.AnalysisResult, sourceVersion, interval string, start, weight float64, reps int, at time.Time, timeSource string) db.PerformanceRecord {
			return testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{ProfileID: profile.ID, AnalysisID: a.ID, SessionID: a.SessionID, IntervalID: interval, SourceVersion: sourceVersion, Movement: "Power Clean", StartSecs: start, EndSecs: start + 60, Reps: workoutprogress.IntPtr(reps), LoadValue: &weight, LoadUnit: workoutprogress.StringPtr("kg"), LoadBasis: workoutprogress.StringPtr("total"), LoadKG: &weight, Measurement: "window", Technique: "singles", WorkoutPrescription: "Grace", PerformedAt: &at, PerformedAtSource: &timeSource})
		}
		first := createRecord(analysis, progress.SourceVersion, "first", 0, 70, 10, now, source)
		second := createRecord(analysis, progress.SourceVersion, "second", 60, 70, 11, now, source)
		createRecord(analysis, "old-source", "stale-current", 120, 70, 12, now, source)
		var direct, lighter db.PerformanceRecord
		for i, kind := range []string{"direct", "lighter", "stale", "archived", "future", "fallback"} {
			p, r := progressFixture("share-" + kind)
			at := now.Add(-time.Duration(i+1) * time.Hour)
			if kind == "future" {
				at = now.Add(time.Hour)
			}
			a := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: profile.ID, SessionID: "WOD-20261031-SHARE" + kind, Status: "COMPLETED", WorkoutProgress: r, WorkoutAt: &at, WorkoutAtSource: &source})
			version, timeSource, weight, reps := p.SourceVersion, source, 70.0, 8
			switch kind {
			case "lighter":
				weight, reps = 60, 10
			case "stale":
				version = "old-source"
			case "archived":
				Expect(dbConn.Model(&a).Update("archived_at", now).Error).To(Succeed())
			case "fallback":
				timeSource = timeline.WorkoutAtSourceSessionCreatedFallback
			}
			record := createRecord(a, version, kind, 0, weight, reps, at, timeSource)
			if kind == "direct" {
				direct = record
			}
			if kind == "lighter" {
				lighter = record
			}
		}
		router := newTestRouterWithAuthService(controllers.Config{})
		path := fmt.Sprintf("/api/v1/sessions/%s/share-context?profile_id=%d&timezone=Asia%%2FSeoul", session.SessionID, profile.ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &user))
		Expect(response.Code).To(Equal(200), response.Body.String())
		var out controllers.WorkoutShareContextResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &out)).To(Succeed())
		Expect(out.ConfirmedRecords).To(HaveLen(2))
		Expect(out.Comparisons).To(HaveLen(2))
		Expect(out.Comparisons[0].CurrentRecordID).To(Equal(first.ID))
		Expect(out.Comparisons[1].CurrentRecordID).To(Equal(second.ID))
		for _, comparison := range out.Comparisons {
			Expect(comparison.BaselineRecordID).To(Equal(direct.ID))
			Expect(comparison.Kind).To(Equal("direct"))
		}
		Expect(out.Comparisons[0].HeavierLoadBaselineRecordID).To(HaveValue(Equal(lighter.ID)))
		Expect(out.Comparisons[0].HeavierLoadKG).To(HaveValue(Equal(10.0)))
		Expect(out.BaselineRecords).To(HaveLen(2))
		Expect(out.Effort).To(HaveValue(Equal(8)))
		Expect(out.Warnings).To(ContainElement("stale_confirmations_excluded"))
		Expect(out.Timezone).To(Equal("Asia/Seoul"))
		Expect(response.Header().Get("Cache-Control")).To(Equal("no-store"))
		var recordCount int64
		Expect(dbConn.Model(&db.PerformanceRecord{}).Count(&recordCount).Error).To(Succeed())
		Expect(recordCount).To(Equal(int64(9)), "snapshot reads do not create or modify confirmations")
	})

	It("preserves null repetition counts and explicit incomplete and rest confirmations", func() {
		profile, user := progressOwner()
		progress, raw := progressFixture("share-outcomes")
		sid := "WOD-20261031-SHAREOUTCOMES"
		analysis := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{ProfileID: profile.ID, SessionID: sid, Status: "COMPLETED", WorkoutProgress: raw})
		for i, outcome := range []string{"completed", "incomplete", "rest"} {
			movement := "Power Clean"
			if outcome == "rest" {
				movement = ""
			}
			testhelpers.CreatePerformanceRecord(dbConn, &db.PerformanceRecord{ProfileID: profile.ID, AnalysisID: analysis.ID, SessionID: sid, IntervalID: outcome, SourceVersion: progress.SourceVersion, Outcome: outcome, Movement: movement, StartSecs: float64(i * 20), EndSecs: float64((i + 1) * 20)})
		}
		router := newTestRouterWithAuthService(controllers.Config{})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/share-context?profile_id=%d", sid, profile.ID), "", &user))
		Expect(response.Code).To(Equal(200), response.Body.String())
		var out controllers.WorkoutShareContextResponse
		Expect(json.Unmarshal(response.Body.Bytes(), &out)).To(Succeed())
		Expect(out.ConfirmedRecords).To(HaveLen(3))
		for i, outcome := range []string{"completed", "incomplete", "rest"} {
			Expect(out.ConfirmedRecords[i].Outcome).To(Equal(outcome))
			Expect(out.ConfirmedRecords[i].Reps).To(BeNil())
			Expect(out.ConfirmedRecords[i].RepsPerMinute).To(BeNil())
		}
		Expect(out.Comparisons).To(BeEmpty())
		Expect(out.Observations).To(HaveLen(1), "user rest corrections do not change original AI evidence")
	})
})
