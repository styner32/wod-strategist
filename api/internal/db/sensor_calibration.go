package db

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/wod-strategist/api/internal/sensor"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RecomputeProfileENMOCalibrationTx uses raw (pre-normalization) percentiles,
// never a historical hybrid score. Call inside a transaction. Profile locking
// serializes archive/restore with the worker's initial calibration promotion.
func RecomputeProfileENMOCalibrationTx(ctx context.Context, tx *gorm.DB, profileID uint, onlyIfUnset bool) error {
	var profile Profile
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&profile, profileID).Error; err != nil {
		return err
	}
	if profile.ArchivedAt != nil {
		return nil
	}
	if onlyIfUnset && profile.SensorCalibrationVersion != nil && *profile.SensorCalibrationVersion != "" {
		return nil
	}
	var rows []AnalysisResult
	if err := tx.WithContext(ctx).Select("id, sensor_version, sensor_summary").Where("profile_id = ? AND sensor_state = ? AND archived_at IS NULL", profileID, SensorStateCompleted).Order("id DESC").Limit(25).Find(&rows).Error; err != nil {
		return err
	}
	var lows, highs []float64
	for _, row := range rows {
		var summary sensor.SensorSummaryResult
		if json.Unmarshal(row.SensorSummary, &summary) != nil || summary.Version != row.SensorVersion || summary.CalculationVersion < 3 || !summary.Quality.IsComplete || summary.Metrics.Hybrid == nil {
			continue
		}
		h := summary.Metrics.Hybrid
		if h.ValidACCSeconds < 120 || h.ENMOP05G == nil || h.ENMOP95G == nil || !finiteCalibration(*h.ENMOP05G) || !finiteCalibration(*h.ENMOP95G) {
			continue
		}
		lows = append(lows, *h.ENMOP05G)
		highs = append(highs, *h.ENMOP95G)
		if len(highs) == 5 {
			break
		}
	}
	updates := map[string]any{"sensor_enmo_rest_g": nil, "sensor_enmo_ref_g": nil, "sensor_calibration_version": nil, "sensor_calibrated_at": nil, "sensor_calibration_sessions": 0}
	if len(highs) >= 3 {
		median := func(values []float64) float64 {
			sort.Float64s(values)
			n := len(values)
			if n%2 == 1 {
				return values[n/2]
			}
			return (values[n/2-1] + values[n/2]) / 2
		}
		rest := math.Round(math.Max(0.005, math.Min(0.05, median(lows)))*10000) / 10000
		ref := math.Round(math.Max(math.Max(rest+0.08, 0.12), math.Min(1.50, median(highs)))*10000) / 10000
		updates["sensor_enmo_rest_g"], updates["sensor_enmo_ref_g"] = rest, ref
		updates["sensor_calibration_version"], updates["sensor_calibrated_at"], updates["sensor_calibration_sessions"] = sensor.PersonalCalibrationVersion, time.Now().UTC(), len(highs)
	} else if onlyIfUnset {
		return nil
	}
	return tx.Table("profiles").Where("id = ?", profileID).Updates(updates).Error
}

func finiteCalibration(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// SensorCalibrationExclusionReason is for current cross-workout advice only;
// original summaries and frozen calculation inputs remain inspectable.
func SensorCalibrationExclusionReason(summary sensor.SensorSummaryResult, profile Profile) string {
	inputs := summary.CalculationInputs
	if summary.Metrics.Hybrid != nil && summary.Metrics.Hybrid.CalibrationVersion != inputs.CalibrationVersion {
		return "calibration_provenance_unknown"
	}
	if inputs.CalibrationVersion == sensor.BootstrapCalibrationVersion {
		if inputs.ENMORestG != nil && inputs.ENMORefG != nil && *inputs.ENMORestG == 0.01 && *inputs.ENMORefG == 0.30 {
			return ""
		}
		return "calibration_provenance_unknown"
	}
	if inputs.CalibrationVersion != sensor.PersonalCalibrationVersion || inputs.ENMORestG == nil || inputs.ENMORefG == nil || !finiteCalibration(*inputs.ENMORestG) || !finiteCalibration(*inputs.ENMORefG) {
		return "calibration_provenance_unknown"
	}
	if profile.SensorCalibrationVersion == nil || *profile.SensorCalibrationVersion != sensor.PersonalCalibrationVersion || profile.SensorENMORestG == nil || profile.SensorENMORefG == nil || *inputs.ENMORestG != *profile.SensorENMORestG || *inputs.ENMORefG != *profile.SensorENMORefG {
		return "calibration_changed"
	}
	return ""
}
