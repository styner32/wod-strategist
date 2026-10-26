package controllers

import (
	"encoding/json"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
)

func populateHeartRateSummary(res *db.AnalysisResult) {
	if res == nil {
		return
	}
	dto := &db.HeartRateSummaryDTO{Status: "none", ProcessingState: res.SensorState, QualityStatus: "unknown", ApplicationReason: "no_sensor"}
	res.HeartRate = dto
	switch res.SensorState {
	case db.SensorStateUploading, db.SensorStatePending, db.SensorStateRunning:
		dto.Status = "pending"
		dto.ApplicationReason = "pending"
		return
	case db.SensorStateFailed, db.SensorStateExpired:
		dto.Status = "failed"
		dto.ApplicationReason = "failed"
		return
	case db.SensorStateCompleted:
	default:
		return
	}
	fresh := evaluateSensorSummaryFreshness(res)
	if !fresh.Valid {
		dto.Status = "unavailable"
		dto.ApplicationReason = "stale_summary"
		return
	}
	var summary sensor.SensorSummaryResult
	if json.Unmarshal(res.SensorSummary, &summary) != nil {
		dto.Status = "unavailable"
		dto.ApplicationReason = "stale_summary"
		return
	}
	hr := summary.Metrics.HR
	dto.Status = "completed"
	dto.CalculationVersion = summary.CalculationVersion
	dto.DeviceName = summary.DeviceName
	dto.QualityStatus = "adequate"
	if !fresh.ValidHR {
		dto.Status = "limited"
		dto.QualityStatus = "limited"
	}
	if summary.CalculationVersion == 1 {
		dto.QualityStatus = "legacy"
	}
	dto.AvgBPM = hr.WeightedMeanBPM
	dto.MinBPM = hr.MinBPM
	dto.PeakBPM = hr.PeakBPM
	dto.Coverage = &hr.Coverage
	dto.ValidSeconds = &hr.ValidSeconds
	dto.UnknownSeconds = &hr.UnknownSeconds
	if summary.CalculationVersion >= 2 {
		dto.ExcludedSeconds = &hr.ExcludedSeconds
		dto.ExcludedByReason = hr.ExcludedByReason
		dto.LowBPMSeconds = &hr.LowBPMSeconds
		dto.ContactCoverage = hr.ContactCoverage
	}
	if summary.CalculationVersion >= 3 && summary.Metrics.Hybrid != nil {
		hy := summary.Metrics.Hybrid
		flags := hy.QualityFlags
		if flags == nil {
			flags = []string{}
		}
		dto.Hybrid = &db.HybridIntensitySummaryDTO{
			HeartIntensity:     hy.HeartIntensity,
			MotionIntensity:    hy.MotionIntensity,
			HybridIntensity:    hy.HybridIntensity,
			HybridLoadAU:       hy.HybridLoadAU,
			ValidHybridSeconds: hy.ValidHybridSeconds,
			DataCoverage:       hy.DataCoverage,
			PeakExceededMaxHR:  hy.PeakExceededMaxHR,
			QualityFlags:       flags,
			CalibrationVersion: hy.CalibrationVersion,
			AlgorithmVersion:   hy.AlgorithmVersion,
		}
	}
	dto.MaxBPM = hr.EstimatedMaxHR
	dto.MaxBPMSource = summary.CalculationInputs.MaxHRSource
	if z := hr.Zones; z != nil && hr.HasHRZones && hr.EstimatedMaxHR != nil && *hr.EstimatedMaxHR > 0 && hr.ValidSeconds > 0 {
		dto.Zones = []db.HeartRateZoneDTO{
			{Zone: 1, Seconds: z.Zone1Seconds, Ratio: z.Zone1Ratio},
			{Zone: 2, Seconds: z.Zone2Seconds, Ratio: z.Zone2Ratio},
			{Zone: 3, Seconds: z.Zone3Seconds, Ratio: z.Zone3Ratio},
			{Zone: 4, Seconds: z.Zone4Seconds, Ratio: z.Zone4Ratio},
			{Zone: 5, Seconds: z.Zone5Seconds, Ratio: z.Zone5Ratio},
		}
	}
	dto.ApplicationReason = "quality_insufficient"
	if !fresh.ValidHR {
		return
	}
	dto.ApplicationReason = "video_insufficient"
	if res.Status != "COMPLETED" {
		return
	}
	dto.Applied = false
	dto.ApplicationReason = "reference_only"
}
