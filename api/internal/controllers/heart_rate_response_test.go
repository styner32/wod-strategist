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
	It("keeps measured heart rate visible as reference data regardless of legacy bonus", func() {
		for _, bonus := range []float64{0, 20} {
			row := makeResult(2, true, bonus)
			populateHeartRateSummary(&row)
			Expect(row.HeartRate.Status).To(Equal("completed"))
			Expect(row.HeartRate.AvgBPM).To(HaveValue(Equal(130.0)))
			Expect(row.HeartRate.Applied).To(BeFalse())
			Expect(row.HeartRate.ApplicationReason).To(Equal("reference_only"))
			Expect(row.HeartRate.CardioBefore).To(BeNil())
			Expect(row.HeartRate.CardioAfter).To(BeNil())
			Expect(row.HeartRate.CardioDelta).To(BeNil())
		}
		row := db.AnalysisResult{}
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
	It("keeps deterministic fatigue identical with or without optional sensor data", func() {
		for _, intensity := range []int{70, 100} {
			withoutSensor := db.AnalysisResult{
				Status:       "COMPLETED",
				SessionScore: fmt.Sprintf(`{"intensity":%d,"movements":{"Thruster":{},"Pull-up":{}}}`, intensity),
			}
			populateSessionFatigueWithSchema(&withoutSensor, 1)
			Expect(withoutSensor.SessionFatigue).NotTo(BeNil())
			for _, bonus := range []float64{0, 20} {
				withSensor := makeResult(2, true, bonus)
				withSensor.SessionScore = withoutSensor.SessionScore
				populateSessionFatigueWithSchema(&withSensor, 1)
				Expect(withSensor.SessionFatigue).NotTo(BeNil())
				Expect(withSensor.SessionFatigue.Muscles).To(Equal(withoutSensor.SessionFatigue.Muscles))
				Expect(withSensor.SessionFatigue.OverallScore).To(Equal(withoutSensor.SessionFatigue.OverallScore))
				Expect(withSensor.SessionFatigue.State).To(Equal(withoutSensor.SessionFatigue.State))
				Expect(withSensor.SessionFatigue.Guidance).To(Equal(withoutSensor.SessionFatigue.Guidance))
				Expect(withSensor.SessionFatigue.HeartRateAdjusted).To(BeFalse())
				Expect(withSensor.HeartRate.ApplicationReason).To(Equal("reference_only"))
			}
		}
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
	It("exposes calculation version 3 hybrid intensity metrics while keeping deterministic fatigue unchanged", func() {
		row := db.AnalysisResult{
			Status:        "COMPLETED",
			SensorState:   db.SensorStateCompleted,
			SensorVersion: 3,
			SensorProcessing: db.JSONDocument(`{
				"request_id":"r3",
				"target_generation":"105",
				"calculation_inputs":{"calculation_version":3,"resting_hr":60,"enmo_rest_g":0.01,"enmo_ref_g":0.31,"calibration_version":"personal_chest_v1"}
			}`),
			SensorSummary: db.JSONDocument(`{
				"version":3,
				"request_id":"r3",
				"source_generation":"105",
				"calculation_version":3,
				"device_name":"Polar H10 ABC",
				"calculation_inputs":{"calculation_version":3,"max_hr_source":"estimated_220_minus_age","resting_hr":60,"enmo_rest_g":0.01,"enmo_ref_g":0.31,"calibration_version":"personal_chest_v1"},
				"quality":{"valid_hr":true,"is_complete":true},
				"hr_bonus":0,
				"metrics":{
					"hr":{"weighted_mean_bpm":150,"min_bpm":120,"peak_bpm":185,"coverage":0.98,"valid_seconds":588,"excluded_seconds":12,"unknown_seconds":0,"contact_coverage":0.98,"has_hr_zones":true,"estimated_max_hr":180,"zones":{"zone4_seconds":588,"zone4_ratio":1}},
					"hybrid":{
						"heart_intensity":0.75,
						"motion_intensity":0.80,
						"hybrid_intensity":76.25,
						"hybrid_load_au":5.81,
						"valid_hybrid_seconds":588,
						"valid_acc_seconds":588,
						"data_coverage":0.98,
						"peak_exceeded_max_hr":true,
						"quality_flags":["peak_hr_exceeded_max"],
						"calibration_version":"personal_chest_v1",
						"algorithm_version":"hybrid-0.75-0.25-gamma2"
					}
				}
			}`),
			SessionScore: `{"intensity":70,"movements":{"Thruster":{},"Pull-up":{}}}`,
		}

		populateSessionFatigueWithSchema(&row, 1)
		Expect(row.HeartRate).NotTo(BeNil())
		Expect(row.HeartRate.Status).To(Equal("completed"))
		Expect(row.HeartRate.CalculationVersion).To(Equal(3))
		Expect(row.HeartRate.Hybrid).NotTo(BeNil())
		Expect(row.HeartRate.Hybrid.HeartIntensity).To(HaveValue(Equal(0.75)))
		Expect(row.HeartRate.Hybrid.MotionIntensity).To(HaveValue(Equal(0.80)))
		Expect(row.HeartRate.Hybrid.HybridIntensity).To(HaveValue(Equal(76.25)))
		Expect(row.HeartRate.Hybrid.HybridLoadAU).To(HaveValue(Equal(5.81)))
		Expect(row.HeartRate.Hybrid.DataCoverage).To(Equal(0.98))
		Expect(row.HeartRate.Hybrid.PeakExceededMaxHR).To(BeTrue())
		Expect(row.HeartRate.Hybrid.QualityFlags).To(Equal([]string{"peak_hr_exceeded_max"}))
		Expect(row.HeartRate.Hybrid.CalibrationVersion).To(Equal("personal_chest_v1"))
		Expect(row.HeartRate.Hybrid.AlgorithmVersion).To(Equal("hybrid-0.75-0.25-gamma2"))
		Expect(row.SessionFatigue).NotTo(BeNil())
		Expect(row.SessionFatigue.HeartRateAdjusted).To(BeFalse())
	})

})
