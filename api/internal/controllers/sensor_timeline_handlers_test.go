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
	"github.com/wod-strategist/api/internal/sensor"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("GET /api/v1/sessions/:session_id/sensor-timeline", func() {
	var (
		router   *gin.Engine
		user     db.User
		profile  db.Profile
		user2    db.User
		profile2 db.Profile
	)

	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		testhelpers.CleanupQueue(inspector)
		router = newTestRouterWithAuthService(controllers.Config{})

		birthYear1 := 1990
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{
			BirthYear: &birthYear1,
		})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())

		birthYear2 := 1995
		profile2 = testhelpers.CreateProfile(dbConn, &db.Profile{
			BirthYear: &birthYear2,
		})
		Expect(dbConn.First(&user2, profile2.UserID).Error).NotTo(HaveOccurred())
	})

	It("rejects unauthenticated request", func() {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF/sensor-timeline?profile_id=%d", profile.ID), nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusUnauthorized))
	})

	It("rejects request for profile owned by another user", func() {
		sid := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID: sid,
			ProfileID: profile2.ID,
			Status:    "COMPLETED",
		})

		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/sensor-timeline?profile_id=%d", sid, profile2.ID), nil)
		authorizeRequest(req, &user) // user 1 requests profile 2
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusForbidden))
	})

	It("returns status 'none' when no sensor record exists", func() {
		sid := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:   sid,
			ProfileID:   profile.ID,
			Status:      "COMPLETED",
			SensorState: db.SensorStateNone,
		})

		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/sensor-timeline?profile_id=%d", sid, profile.ID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp controllers.SensorTimelineResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Status).To(Equal("none"))
		Expect(resp.Timeline).To(BeNil())
		Expect(resp.VideoMapping.Kind).To(Equal("merged"))
		Expect(resp.VideoMapping.Segments).To(BeEmpty())
	})

	It("returns status 'pending' when sensor is being processed", func() {
		sid := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:     sid,
			ProfileID:     profile.ID,
			Status:        "COMPLETED",
			SensorVersion: 1,
			SensorState:   db.SensorStateRunning,
		})

		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/sensor-timeline?profile_id=%d", sid, profile.ID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp controllers.SensorTimelineResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Status).To(Equal("pending"))
		Expect(resp.Timeline).To(BeNil())
	})

	It("returns status 'unavailable' for legacy record without timeline", func() {
		sid := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:     sid,
			ProfileID:     profile.ID,
			Status:        "COMPLETED",
			SensorVersion: 1,
			SensorState:   db.SensorStateCompleted,
			SensorSummary: db.JSONDocument(`{"quality":{"valid_hr":true}}`),
		})

		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/sensor-timeline?profile_id=%d", sid, profile.ID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp controllers.SensorTimelineResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Status).To(Equal("unavailable"))
		Expect(resp.Reason).To(Equal("timeline_not_generated"))
		Expect(resp.Timeline).To(BeNil())
	})

	It("reports optional timeline failure independently of the completed sensor summary", func() {
		sid := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:      sid,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			SensorVersion:  1,
			SensorState:    db.SensorStateCompleted,
			SensorSummary:  db.JSONDocument(`{"quality":{"valid_hr":true}}`),
			SensorTimeline: db.JSONDocument(`{"status":"failed","error":"timeline clock is out of range"}`),
		})
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/sensor-timeline?profile_id=%d", sid, profile.ID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
		var resp controllers.SensorTimelineResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Status).To(Equal("failed"))
		Expect(resp.Reason).To(Equal("timeline clock is out of range"))
		Expect(resp.Timeline).To(BeNil())
	})

	It("returns status 'completed' with timeline and dynamic video mapping", func() {
		sid := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"

		// Create chunk boundaries
		cStart1, cEnd1, mStart1, mEnd1 := 0.0, 10.0, 0.0, 9.8
		testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:      sid,
			ProfileID:      profile.ID,
			StartSecs:      &cStart1,
			EndSecs:        &cEnd1,
			MediaStartSecs: &mStart1,
			MediaEndSecs:   &mEnd1,
		})
		cStart2, cEnd2, mStart2, mEnd2 := 12.0, 22.0, 10.0, 19.5
		testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID:      sid,
			ProfileID:      profile.ID,
			StartSecs:      &cStart2,
			EndSecs:        &cEnd2,
			MediaStartSecs: &mStart2,
			MediaEndSecs:   &mEnd2,
		})

		timelineData := sensor.SensorTimelineData{
			SchemaVersion: 1,
			Clock:         "capture_clock",
			BucketMs:      1000,
			DurationMs:    22000,
			Source: sensor.TimelineSource{
				SensorVersion:        "2",
				RequestID:            "req-1",
				SourceGeneration:     "gen-1",
				HRCalculationVersion: 2,
			},
			Points: []sensor.SensorTimelinePoint{
				{
					StartMs: 0,
					EndMs:   1000,
					HeartRateBPM: sensor.TimelineValue{
						Value:  func() *float64 { v := 130.0; return &v }(),
						Status: sensor.StatusValid,
					},
					AccMagnitudeStdG: sensor.TimelineValue{
						Value:  func() *float64 { v := 0.15; return &v }(),
						Status: sensor.StatusValid,
					},
				},
			},
		}
		tlJSON, err := json.Marshal(timelineData)
		Expect(err).NotTo(HaveOccurred())

		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:      sid,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			SensorVersion:  2,
			SensorState:    db.SensorStateCompleted,
			SensorSummary:  db.JSONDocument(`{"quality":{"valid_hr":true}}`),
			SensorTimeline: db.JSONDocument(tlJSON),
		})

		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/sensor-timeline?profile_id=%d", sid, profile.ID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp controllers.SensorTimelineResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Status).To(Equal("completed"))
		Expect(resp.Timeline).NotTo(BeNil())
		Expect(resp.Timeline.Points).To(HaveLen(1))
		Expect(*resp.Timeline.Points[0].HeartRateBPM.Value).To(Equal(130.0))
		Expect(resp.Timeline.Source).To(Equal(timelineData.Source))
		Expect(resp.Timeline.Points[0].AccMagnitudeStdG.Value).To(HaveValue(Equal(0.15)))

		// Check video mapping
		Expect(resp.VideoMapping.Kind).To(Equal("merged"))
		Expect(resp.VideoMapping.Method).To(Equal("chunk_linear"))
		Expect(resp.VideoMapping.Segments).To(HaveLen(2))
		Expect(resp.VideoMapping.Segments[0].CaptureStartMs).To(Equal(int64(0)))
		Expect(resp.VideoMapping.Segments[0].CaptureEndMs).To(Equal(int64(10000)))
		Expect(resp.VideoMapping.Segments[0].MediaStartMs).To(Equal(int64(0)))
		Expect(resp.VideoMapping.Segments[0].MediaEndMs).To(Equal(int64(9800)))
		Expect(resp.VideoMapping.Segments[1].CaptureStartMs).To(Equal(int64(12000)))
		Expect(resp.VideoMapping.Segments[1].CaptureEndMs).To(Equal(int64(22000)))
		Expect(resp.VideoMapping.Segments[1].MediaStartMs).To(Equal(int64(10000)))
		Expect(resp.VideoMapping.Segments[1].MediaEndMs).To(Equal(int64(19500)))
	})

	It("does not include sensor_timeline in general session analysis response", func() {
		sid := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:      sid,
			ProfileID:      profile.ID,
			Status:         "COMPLETED",
			SensorVersion:  1,
			SensorState:    db.SensorStateCompleted,
			SensorTimeline: db.JSONDocument(`{"schema_version":1,"points":[{"start_ms":0}]}`),
		})

		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/analysis?profile_id=%d", sid, profile.ID), nil)
		authorizeRequest(req, &user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var jsonMap map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &jsonMap)).To(Succeed())
		Expect(jsonMap).NotTo(HaveKey("sensor_timeline"))
		Expect(jsonMap).To(HaveKey("analysis"))
		Expect(jsonMap["analysis"]).NotTo(HaveKey("sensor_timeline"))
	})
})
