package controllers

import (
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("Heart rate response", func() {
	makeResult := func(version int, valid bool, bonus float64) db.AnalysisResult {
		return db.AnalysisResult{Status: "COMPLETED", SensorState: db.SensorStateCompleted, SensorVersion: 3,
			SensorProcessing: db.JSONDocument(fmt.Sprintf(`{"request_id":"r","target_generation":"99","calculation_inputs":{"calculation_version":%d}}`, version)),
			SensorSummary:    db.JSONDocument(fmt.Sprintf(`{"version":3,"request_id":"r","source_generation":"99","calculation_version":%d,"device_name":"Polar H10 ABC","calculation_inputs":{"max_hr_source":"estimated_220_minus_age"},"quality":{"valid_hr":%t,"is_complete":true},"hr_bonus":%g,"metrics":{"hr":{"weighted_mean_bpm":130,"min_bpm":120,"peak_bpm":150,"coverage":0.8,"valid_seconds":80,"excluded_seconds":10,"unknown_seconds":10,"contact_coverage":0.9,"excluded_by_reason":{"contact_loss":10},"has_hr_zones":true,"estimated_max_hr":190,"zones":{"zone3_seconds":80,"zone3_ratio":1}}}}`, version, valid, bonus)),
			SessionScore:     `{"intensity":70,"movements":{"Thruster":{},"Pull-up":{}}}`,
		}
	}
	It("shows sensor-only success independently of incomplete video and fatigue", func() {
		row := makeResult(2, true, 0)
		row.Status = "PENDING"
		row.SessionScore = ""
		populateSessionFatigueWithSchema(&row, 1)
		Expect(row.HeartRate.Status).To(Equal("completed"))
		Expect(row.HeartRate.AvgBPM).To(HaveValue(Equal(130.0)))
		Expect(row.HeartRate.ApplicationReason).To(Equal("video_insufficient"))
		Expect(row.HeartRate.Applied).To(BeFalse())
		Expect(row.SessionFatigue).To(BeNil())
		bytes, err := json.Marshal(row)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(bytes)).To(ContainSubstring(`"heart_rate":`))
		Expect(string(bytes)).To(ContainSubstring(`"zone":3`))
	})
	It("distinguishes no bonus from no sensor and reports actual cardio delta", func() {
		row := makeResult(2, true, 0)
		populateHeartRateSummary(&row)
		Expect(row.HeartRate.Applied).To(BeTrue())
		Expect(row.HeartRate.ApplicationReason).To(Equal("no_bonus"))
		Expect(row.HeartRate.CardioDelta).To(HaveValue(BeZero()))
		row = makeResult(2, true, 20)
		populateHeartRateSummary(&row)
		Expect(row.HeartRate.CardioDelta).To(HaveValue(BeNumerically(">", 0)))
		Expect(*row.HeartRate.CardioDelta).To(BeNumerically("~", *row.HeartRate.CardioAfter-*row.HeartRate.CardioBefore, 0.05))
		row = db.AnalysisResult{}
		populateHeartRateSummary(&row)
		Expect(row.HeartRate.Status).To(Equal("none"))
		Expect(row.HeartRate.AvgBPM).To(BeNil())
	})
	It("labels version one without inventing contact or excluded durations", func() {
		row := makeResult(1, true, 0)
		populateHeartRateSummary(&row)
		Expect(row.HeartRate.QualityStatus).To(Equal("legacy"))
		Expect(row.HeartRate.CalculationVersion).To(Equal(1))
		Expect(row.HeartRate.ContactCoverage).To(BeNil())
		Expect(row.HeartRate.ExcludedSeconds).To(BeNil())
	})
	It("keeps limited quality statistics visible without applying them", func() {
		row := makeResult(2, false, 0)
		populateHeartRateSummary(&row)
		Expect(row.HeartRate.Status).To(Equal("limited"))
		Expect(row.HeartRate.AvgBPM).NotTo(BeNil())
		Expect(row.HeartRate.Applied).To(BeFalse())
		Expect(row.HeartRate.ApplicationReason).To(Equal("quality_insufficient"))
	})
	It("suppresses previous successful metrics for every mismatching identity", func() {
		for _, processing := range []string{
			`{"request_id":"other","target_generation":"99","calculation_inputs":{"calculation_version":2}}`,
			`{"request_id":"r","target_generation":"other","calculation_inputs":{"calculation_version":2}}`,
			`{"request_id":"r","target_generation":"99","calculation_inputs":{"calculation_version":1}}`,
		} {
			row := makeResult(2, true, 10)
			row.SensorProcessing = db.JSONDocument(processing)
			populateHeartRateSummary(&row)
			Expect(row.HeartRate.Status).To(Equal("unavailable"))
			Expect(row.HeartRate.AvgBPM).To(BeNil())
		}
		row := makeResult(2, true, 10)
		row.SensorVersion++
		populateHeartRateSummary(&row)
		Expect(row.HeartRate.AvgBPM).To(BeNil())
		for _, state := range []string{db.SensorStatePending, db.SensorStateFailed} {
			row := makeResult(2, true, 10)
			row.SensorState = state
			populateHeartRateSummary(&row)
			Expect(row.HeartRate.AvgBPM).To(BeNil())
		}
	})
	It("reports the actual capped cardio difference", func() {
		row := makeResult(2, true, 20)
		row.SessionScore = `{"intensity":100,"movements":{"Thruster":{},"Burpee":{},"Row":{},"Run":{},"Double Under":{},"Wall Ball":{},"Box Jump":{}}}`
		populateHeartRateSummary(&row)
		Expect(row.HeartRate.CardioBefore).To(HaveValue(Equal(100.0)))
		Expect(row.HeartRate.CardioDelta).To(HaveValue(BeZero()))
		Expect(row.HeartRate.ApplicationReason).To(Equal("score_capped"))
	})
	It("omits zones when a saved result has no maximum heart rate basis", func() {
		row := makeResult(2, true, 0)
		var summary map[string]any
		Expect(json.Unmarshal(row.SensorSummary, &summary)).To(Succeed())
		delete(summary["metrics"].(map[string]any)["hr"].(map[string]any), "estimated_max_hr")
		raw, err := json.Marshal(summary)
		Expect(err).NotTo(HaveOccurred())
		row.SensorSummary = db.JSONDocument(raw)
		populateHeartRateSummary(&row)
		Expect(row.HeartRate.Zones).To(BeNil())
	})

})
