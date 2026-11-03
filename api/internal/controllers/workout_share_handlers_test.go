package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/timeline"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

var _ = Describe("Workout share snapshot projection", func() {
	It("rejects absent authentication even without route middleware", func() {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		(&Controller{}).GetWorkoutShareContext(ctx)
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(response.Header().Get("Cache-Control")).To(Equal("no-store"))
	})

	It("keeps every AI interval separate from confirmations and projects no raw events or private attributes", func() {
		progress := workoutprogress.Build([]workoutprogress.Interval{
			{ID: "exercise", Start: 0, End: 10, Activity: "exercise", Movement: "Power Clean", ObservationRefs: []string{"private-source"},
				RepEvents:        []activity.Observation{{Start: 1, End: 3, Movement: "Power Clean", Unit: "reps", Complete: true, Evidence: "floor to rack"}},
				FormObservations: []workoutprogress.Observation{{Start: 1, End: 3, Evidence: "elbows reach rack"}}},
			{ID: "rest", Start: 10, End: 20, Activity: "rest"},
		}, []workoutprogress.Gap{{Start: 20, End: 30, Clock: "media", Reason: "not_analyzed"}}, "video")
		raw, err := json.Marshal(progress)
		Expect(err).NotTo(HaveOccurred())
		out := buildWorkoutShareContext(db.AnalysisResult{SessionID: "WOD-20261031-SHARE", WorkoutProgress: db.NullableJSONDocument(raw), CreatedAt: time.Now()}, db.Session{WorkoutType: "accessory", WODDescription: "prescribed load only"}, time.UTC)
		Expect(out.Session.WorkoutType).To(Equal("accessory"))
		Expect(out.Session.WorkoutPrescription).To(Equal("prescribed load only"))
		Expect(out.Observations).To(HaveLen(2))
		Expect(out.Observations[0].ObservedReps).To(HaveValue(Equal(1)))
		Expect(out.Observations[1].ObservedReps).To(BeNil())
		Expect(out.Observations[0].ROMObservations).NotTo(BeNil())
		Expect(out.Observations[1].FormObservations).NotTo(BeNil())
		Expect(out.Unassessed).To(HaveLen(1))
		Expect(out.ConfirmedRecords).To(BeEmpty())
		Expect(out.Session.PerformedAt).To(BeNil())
		Expect(out.Warnings).To(ContainElement("workout_time_unknown"))
		encoded, err := json.Marshal(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).NotTo(ContainSubstring("rep_events"))
		Expect(string(encoded)).NotTo(ContainSubstring("private-source"))
		Expect(string(encoded)).NotTo(ContainSubstring("profile_id"))
		Expect(string(encoded)).To(ContainSubstring(`"confirmed_records":[]`))
	})

	It("keeps unreliable persisted workout time unknown and resolves known legacy time without a session row", func() {
		fallback := time.Now().Add(-time.Hour)
		source := timeline.WorkoutAtSourceSessionCreatedFallback
		out := buildWorkoutShareContext(db.AnalysisResult{SessionID: "ACCESSORY-20261031-SHARE", WorkoutAt: &fallback, WorkoutAtSource: &source}, db.Session{}, time.UTC)
		Expect(out.Session.PerformedAt).To(BeNil())
		Expect(out.Session.PerformedAtSource).To(HaveValue(Equal(source)))
		Expect(out.Session.WorkoutType).To(Equal("accessory"))
		at, src := workoutShareTime(db.AnalysisResult{SessionID: "P123-WOD-2026-10-01-09-00", CreatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}, db.Session{})
		Expect(at).NotTo(BeNil())
		Expect(src).To(HaveValue(Equal(timeline.WorkoutAtSourceLegacyLocalTime)))
		Expect(at.UTC()).To(Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)))
	})

	It("projects fresh or limited sensor statistics and suppresses stale successful metrics", func() {
		analysis := db.AnalysisResult{Status: "COMPLETED", SensorState: db.SensorStateCompleted, SensorVersion: 3,
			SensorProcessing: db.JSONDocument(`{"request_id":"r","target_generation":"99","calculation_inputs":{"calculation_version":2}}`),
			SensorSummary:    db.JSONDocument(`{"version":3,"request_id":"r","source_generation":"99","calculation_version":2,"device_name":"private-device","quality":{"valid_hr":false,"is_complete":true},"metrics":{"hr":{"weighted_mean_bpm":130,"peak_bpm":160,"coverage":0.5,"valid_seconds":30,"unknown_seconds":20,"excluded_seconds":10}}}`)}
		out := buildWorkoutShareContext(analysis, db.Session{}, time.UTC)
		Expect(out.SensorSummary).NotTo(BeNil())
		Expect(out.SensorSummary.Status).To(Equal("limited"))
		Expect(out.SensorSummary.AvgBPM).To(HaveValue(Equal(130.0)))
		Expect(out.SensorSummary.PeakBPM).To(HaveValue(Equal(160.0)))
		encoded, err := json.Marshal(out.SensorSummary)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).NotTo(ContainSubstring("private-device"))
		analysis.SensorVersion++
		out = buildWorkoutShareContext(analysis, db.Session{}, time.UTC)
		Expect(out.SensorSummary.Status).To(Equal("unavailable"))
		Expect(out.SensorSummary.AvgBPM).To(BeNil())
		analysis.SensorState = db.SensorStatePending
		out = buildWorkoutShareContext(analysis, db.Session{}, time.UTC)
		Expect(out.SensorSummary.Status).To(Equal("pending"))
		Expect(out.SensorSummary.PeakBPM).To(BeNil())
	})

	It("keeps reference comparisons nonnumerical and includes every referenced baseline", func() {
		now := time.Now().UTC()
		before := now.Add(-time.Hour)
		prior := workoutprogress.Record{ID: 1, SessionID: "prior", Status: "confirmed", Movement: "Power Clean", Reps: workoutprogress.IntPtr(5), RepsPerMinute: workoutprogress.Float64Ptr(5), Start: 0, End: 60, Measurement: "window", Technique: "unspecified", PerformedAt: &before}
		current := prior
		current.ID, current.SessionID, current.PerformedAt = 2, "current", &now
		current.RepsPerMinute = workoutprogress.Float64Ptr(10)
		comparisons, baselines := workoutShareComparisons([]workoutprogress.Record{current}, []workoutprogress.Record{prior})
		Expect(comparisons).To(HaveLen(1))
		Expect(comparisons[0].Kind).To(Equal("reference"))
		Expect(comparisons[0].RateChangePercent).To(BeNil())
		Expect(comparisons[0].ConditionDifferences).To(ContainElement("unknown_technique"))
		Expect(baselines).To(HaveLen(1))
		Expect(baselines[0].ID).To(Equal(comparisons[0].BaselineRecordID))
	})

	It("applies cross-movement supersession within each historical session without changing baseline order", func() {
		at := time.Now().UTC()
		old := workoutprogress.Record{ID: 1, SessionID: "prior", Status: "confirmed", Movement: "Power Clean", Start: 0, End: 60, PerformedAt: &at}
		rest := old
		rest.ID, rest.Outcome, rest.Movement = 2, "rest", ""
		other := old
		other.ID, other.SessionID = 3, "other"
		out := activeWorkoutShareHistory([]workoutprogress.Record{other, old, rest})
		Expect(out).To(Equal([]workoutprogress.Record{other, rest}))
	})
})
