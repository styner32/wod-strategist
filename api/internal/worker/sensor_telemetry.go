package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	gcs "cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

type SensorProcessingRecord struct {
	SchemaVersion    int        `json:"schema_version"`
	RequestID        string     `json:"request_id"`
	ObjectName       string     `json:"object_name"`
	TargetGeneration string     `json:"target_generation"`
	UploadExpiresAt  time.Time  `json:"upload_expires_at"`
	SizeBytes        int64      `json:"size_bytes"`
	SHA256           string     `json:"sha256"`
	LeaseToken       *string    `json:"lease_token,omitempty"`
	LeaseStartedAt   *time.Time `json:"lease_started_at,omitempty"`
	Attempts         int        `json:"attempts"`
	RetryNotBefore   *time.Time `json:"retry_not_before,omitempty"`
	LastErrorCode    *string    `json:"last_error_code,omitempty"`
	CalculationInputs struct {
		CalculationVersion int      `json:"calculation_version"`
		Age                *int     `json:"age,omitempty"`
		MaxHR              *int     `json:"max_hr,omitempty"`
		MaxHRSource        *string  `json:"max_hr_source,omitempty"`
		HasHRZones         bool     `json:"has_hr_zones"`
		RestingHR          *int     `json:"resting_hr,omitempty"`
		RestingHRSource    string   `json:"resting_hr_source,omitempty"`
		ENMORestG          *float64 `json:"enmo_rest_g,omitempty"`
		ENMORefG           *float64 `json:"enmo_ref_g,omitempty"`
		CalibrationVersion string   `json:"calibration_version,omitempty"`
	} `json:"calculation_inputs"`
}

var retryBackoffs = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	45 * time.Second,
	120 * time.Second,
}

// HandleSensorTelemetryTask handles a sensor processing task from the asynq queue.
func (w *Worker) HandleSensorTelemetryTask(ctx context.Context, t *asynq.Task) error {
	var payload SensorTelemetryPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		w.logger.Error("failed to unmarshal sensor task payload", zap.Error(err))
		return nil // Non-retryable
	}

	w.logger.Info("received sensor telemetry task",
		zap.Uint("analysis_result_id", payload.AnalysisResultID),
		zap.Uint("profile_id", payload.ProfileID),
		zap.String("request_id", payload.RequestID),
		zap.Int64("version", payload.Version))

	// Step 1: Short transaction to check and acquire lease
	var (
		row        db.AnalysisResult
		processing SensorProcessingRecord
		leaseToken string
	)

	txErr := func() error {
		tx := w.DB.WithContext(ctx).Begin()
		defer tx.Rollback()

		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND archived_at IS NULL", payload.AnalysisResultID).
			First(&row).Error; err != nil {
			return err // disappeared, archived, or db error
		}

		if row.ProfileID != payload.ProfileID || row.SensorVersion != payload.Version {
			return errors.New("stale_task_version")
		}

		if err := json.Unmarshal([]byte(row.SensorProcessing), &processing); err != nil {
			return fmt.Errorf("corrupt_processing_json: %w", err)
		}

		if processing.RequestID != payload.RequestID {
			return errors.New("stale_task_request_id")
		}

		now := time.Now().UTC()
		canExecute := false

		if row.SensorState == db.SensorStatePending {
			if processing.RetryNotBefore == nil || now.After(*processing.RetryNotBefore) || now.Equal(*processing.RetryNotBefore) {
				canExecute = true
			}
		} else if row.SensorState == db.SensorStateRunning {
			if row.SensorNextAttemptAt != nil && now.After(*row.SensorNextAttemptAt) {
				canExecute = true // expired lease
			}
		}

		if !canExecute {
			return errors.New("not_ready_for_execution")
		}

		if processing.Attempts >= 5 {
			errCode := "MAX_ATTEMPTS_EXCEEDED"
			processing.LastErrorCode = &errCode
			procJSON, _ := json.Marshal(processing)
			_ = tx.Model(&row).Updates(map[string]any{
				"sensor_state":           db.SensorStateFailed,
				"sensor_processing":      db.JSONDocument(procJSON),
				"sensor_next_attempt_at": nil,
				"updated_at":             now,
			}).Error
			_ = tx.Commit().Error
			return errors.New("max_attempts_exceeded")
		}

		// Acquire lease
		leaseToken = uuid.New().String()
		processing.Attempts++
		processing.LeaseToken = &leaseToken
		processing.LeaseStartedAt = &now
		nextAttempt := now.Add(60 * time.Second)

		procJSON, _ := json.Marshal(processing)
		if err := tx.Model(&row).Updates(map[string]any{
			"sensor_state":           db.SensorStateRunning,
			"sensor_processing":      db.JSONDocument(procJSON),
			"sensor_next_attempt_at": &nextAttempt,
			"updated_at":             now,
		}).Error; err != nil {
			return err
		}

		return tx.Commit().Error
	}()

	if txErr != nil {
		w.logger.Info("sensor task preempt check bypassed", zap.Error(txErr))
		return nil
	}

	// Step 2: Set execution context (max 120s) and heartbeat
	execCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)

	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-heartbeatDone:
				return
			case <-execCtx.Done():
				return
			case <-ticker.C:
				res := w.DB.Exec(`
					UPDATE analysis_results
					SET sensor_next_attempt_at = NOW() + INTERVAL '60 seconds',
					    updated_at = NOW()
					WHERE id = ?
					  AND sensor_version = ?
					  AND sensor_state = 'RUNNING'
					  AND archived_at IS NULL
					  AND sensor_processing->>'lease_token' = ?
					  AND sensor_next_attempt_at > NOW()
				`, row.ID, row.SensorVersion, leaseToken)
				if res.Error != nil || res.RowsAffected == 0 {
					w.logger.Warn("heartbeat extension failed or lease expired",
						zap.Error(res.Error),
						zap.Int64("affected", res.RowsAffected))
					return
				}
			}
		}
	}()

	// Step 3: Read fixed generation object from GCS and process
	genInt, err := strconv.ParseInt(processing.TargetGeneration, 10, 64)
	if err != nil {
		w.recordSensorFailure(row.ID, row.ProfileID, row.SensorVersion, leaseToken, processing, "INVALID_TARGET_GENERATION", false)
		return nil
	}

	reader, err := w.StorageClient.NewReaderWithGeneration(execCtx, processing.ObjectName, genInt)
	if err != nil {
		if errors.Is(err, gcs.ErrObjectNotExist) {
			w.recordSensorFailure(row.ID, row.ProfileID, row.SensorVersion, leaseToken, processing, "TARGET_GENERATION_NOT_FOUND", false)
			return nil
		}
		// Temporary GCS error
		w.recordSensorFailure(row.ID, row.ProfileID, row.SensorVersion, leaseToken, processing, "STORAGE_READ_ERROR", true)
		return nil
	}
	defer reader.Close()

	opts := sensor.ParseOptions{
		CalculationVersion: processing.CalculationInputs.CalculationVersion,
		ExpectedProfileID:  row.ProfileID,
		ExpectedSessionID:  row.SessionID,
		ExpectedSHA256:     processing.SHA256,
		ExpectedSizeBytes:  processing.SizeBytes,
		Age:                processing.CalculationInputs.Age,
		EstimatedMaxHR:     processing.CalculationInputs.MaxHR,
		RestingHR:          processing.CalculationInputs.RestingHR,
		RestingHRSource:    processing.CalculationInputs.RestingHRSource,
		ENMORestG:          processing.CalculationInputs.ENMORestG,
		ENMORefG:           processing.CalculationInputs.ENMORefG,
		CalibrationVersion: processing.CalculationInputs.CalibrationVersion,
	}

	timelineSource := sensor.TimelineSource{
		SensorVersion:        fmt.Sprintf("%d", row.SensorVersion),
		RequestID:            processing.RequestID,
		SourceGeneration:     processing.TargetGeneration,
		HRCalculationVersion: processing.CalculationInputs.CalculationVersion,
	}

	processingResult, err := sensor.ParseAndProcessWithTimeline(reader, opts, timelineSource)
	if err != nil {
		w.recordSensorFailure(row.ID, row.ProfileID, row.SensorVersion, leaseToken, processing, "PARSE_FATAL_ERROR", false)
		return nil
	}
	summaryResult := processingResult.Summary

	if !summaryResult.Quality.IsComplete {
		errCode := "QUALITY_CHECK_FAILED"
		if summaryResult.Quality.Status == "incomplete" {
			errCode = "INCOMPLETE_SENSOR_FILE"
		}
		w.recordSensorFailure(row.ID, row.ProfileID, row.SensorVersion, leaseToken, processing, errCode, false)
		return nil
	}

	// Enrich summary result with server-side identity
	summaryResult.Version = row.SensorVersion
	summaryResult.RequestID = processing.RequestID
	summaryResult.SourceGeneration = processing.TargetGeneration

	summaryJSON, err := json.Marshal(summaryResult)
	if err != nil {
		w.recordSensorFailure(row.ID, row.ProfileID, row.SensorVersion, leaseToken, processing, "SUMMARY_SERIALIZATION_ERROR", false)
		return nil
	}

	var timelineStr string
	timelineError := processingResult.TimelineError
	if processingResult.Timeline != nil {
		timelineJSON, err := json.Marshal(processingResult.Timeline)
		if err != nil {
			w.logger.Warn("timeline serialization error; saving error status in timeline", zap.Error(err))
			timelineError = err
		} else {
			timelineStr = string(timelineJSON)
		}
	}

	if timelineError != nil {
		w.logger.Warn("sensor timeline failed; preserving sensor summary", zap.Error(timelineError))
		// Marshal the error, rather than interpolating arbitrary text into JSON.
		failedJSON, _ := json.Marshal(struct {
			SchemaVersion int                   `json:"schema_version"`
			Status        string                `json:"status"`
			Error         string                `json:"error"`
			Source        sensor.TimelineSource `json:"source"`
		}{1, "failed", timelineError.Error(), timelineSource})
		timelineStr = string(failedJSON)
	}

	processing.LeaseToken = nil
	procFinalJSON, _ := json.Marshal(processing)

	// Step 4: Conditional single UPDATE
	res := w.DB.Exec(`
		UPDATE analysis_results
		SET sensor_summary = ?::jsonb,
		    sensor_timeline = NULLIF(?::text, '')::jsonb,
		    sensor_state = 'COMPLETED',
		    sensor_next_attempt_at = NULL,
		    sensor_processing = ?::jsonb,
		    updated_at = NOW()
		WHERE id = ?
		  AND profile_id = ?
		  AND sensor_version = ?
		  AND sensor_state = 'RUNNING'
		  AND archived_at IS NULL
		  AND sensor_processing->>'request_id' = ?
		  AND sensor_processing->>'target_generation' = ?
		  AND sensor_processing->>'lease_token' = ?
		  AND sensor_next_attempt_at > NOW()
	`, string(summaryJSON), timelineStr, string(procFinalJSON), row.ID, row.ProfileID, row.SensorVersion, processing.RequestID, processing.TargetGeneration, leaseToken)

	if res.Error != nil {
		w.logger.Error("failed to commit sensor summary update", zap.Error(res.Error))
		return res.Error
	}

	if res.RowsAffected == 0 {
		w.logger.Warn("sensor summary conditional update matched 0 rows; superseded or lease expired",
			zap.Uint("id", row.ID),
			zap.Int64("version", row.SensorVersion),
			zap.String("lease_token", leaseToken))
		return nil
	}

	w.logger.Info("sensor telemetry processing completed successfully",
		zap.Uint("id", row.ID),
		zap.Int64("version", row.SensorVersion),
		zap.Float64("hr_bonus", summaryResult.HRBonus))

	if summaryResult.CalculationVersion == 3 &&
		summaryResult.Metrics.Hybrid != nil &&
		summaryResult.Metrics.Hybrid.ValidACCSeconds >= 120 &&
		summaryResult.Metrics.Hybrid.ENMOP95G != nil {
		w.maybePromoteProfileENMOCalibration(execCtx, row.ProfileID)
	}

	return nil
}

func medianFloat64(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2.0
}

// maybePromoteProfileENMOCalibration promotes a profile from bootstrap chest ENMO constants
// to a fixed personal_chest_v1 calibration once at least 3 valid completed v3 sensor sessions
// (>= 120s valid ACC) exist. Past sessions retain their frozen CalculationInputs snapshot.
func (w *Worker) maybePromoteProfileENMOCalibration(ctx context.Context, profileID uint) {
	if profileID == 0 || w.DB == nil {
		return
	}
	var current db.Profile
	if err := w.DB.WithContext(ctx).
		Select("id, sensor_calibration_version").
		Where("id = ? AND archived_at IS NULL", profileID).
		First(&current).Error; err != nil {
		return
	}
	if current.SensorCalibrationVersion != nil && *current.SensorCalibrationVersion != "" {
		return
	}

	var rows []db.AnalysisResult
	if err := w.DB.WithContext(ctx).
		Select("id, sensor_version, sensor_summary").
		Where("profile_id = ? AND sensor_state = ? AND archived_at IS NULL", profileID, db.SensorStateCompleted).
		Order("id DESC").
		Limit(25).
		Find(&rows).Error; err != nil {
		return
	}

	var p05List, p95List []float64
	for _, r := range rows {
		if len(r.SensorSummary) == 0 || string(r.SensorSummary) == "{}" {
			continue
		}
		var sum sensor.SensorSummaryResult
		if json.Unmarshal(r.SensorSummary, &sum) != nil {
			continue
		}
		if sum.Version != r.SensorVersion || sum.CalculationVersion < 3 || !sum.Quality.IsComplete || sum.Metrics.Hybrid == nil {
			continue
		}
		hy := sum.Metrics.Hybrid
		// The parser sets both percentiles together (>= 60 valid ACC seconds).
		if hy.ValidACCSeconds < 120 || hy.ENMOP05G == nil || hy.ENMOP95G == nil {
			continue
		}
		if math.IsNaN(*hy.ENMOP05G) || math.IsInf(*hy.ENMOP05G, 0) || math.IsNaN(*hy.ENMOP95G) || math.IsInf(*hy.ENMOP95G, 0) {
			continue
		}
		p05List = append(p05List, *hy.ENMOP05G)
		p95List = append(p95List, *hy.ENMOP95G)
		if len(p95List) >= 5 {
			break
		}
	}

	if len(p95List) < 3 {
		return
	}

	restG := math.Round(math.Max(0.005, math.Min(0.05, medianFloat64(p05List)))*10000) / 10000
	refG := math.Round(math.Max(math.Max(restG+0.08, 0.12), math.Min(1.50, medianFloat64(p95List)))*10000) / 10000

	res := w.DB.WithContext(ctx).Exec(`
		UPDATE profiles
		SET sensor_enmo_rest_g = ?,
		    sensor_enmo_ref_g = ?,
		    sensor_calibration_version = ?,
		    sensor_calibrated_at = NOW(),
		    sensor_calibration_sessions = ?,
		    updated_at = NOW()
		WHERE id = ?
		  AND archived_at IS NULL
		  AND sensor_calibration_version IS NULL
	`, restG, refG, sensor.PersonalCalibrationVersion, len(p95List), profileID)
	if res.Error == nil && res.RowsAffected == 1 {
		w.logger.Info("promoted profile sensor ENMO calibration to personal_chest_v1",
			zap.Uint("profile_id", profileID),
			zap.Float64("enmo_rest_g", restG),
			zap.Float64("enmo_ref_g", refG),
			zap.Int("sessions", len(p95List)))
	}
}

func (w *Worker) recordSensorFailure(id, profileID uint, version int64, leaseToken string, proc SensorProcessingRecord, errCode string, retryable bool) {
	now := time.Now().UTC()
	proc.LastErrorCode = &errCode
	proc.LeaseToken = nil

	if retryable && proc.Attempts < 5 {
		backoffIdx := proc.Attempts - 1
		if backoffIdx >= len(retryBackoffs) {
			backoffIdx = len(retryBackoffs) - 1
		}
		if backoffIdx < 0 {
			backoffIdx = 0
		}
		delay := retryBackoffs[backoffIdx]
		nextAttempt := now.Add(delay)
		proc.RetryNotBefore = &nextAttempt

		procJSON, _ := json.Marshal(proc)
		w.DB.Exec(`
			UPDATE analysis_results
			SET sensor_state = 'PENDING',
			    sensor_processing = ?::jsonb,
			    sensor_next_attempt_at = ?,
			    updated_at = NOW()
			WHERE id = ?
			  AND profile_id = ?
			  AND sensor_version = ?
			  AND sensor_state = 'RUNNING'
			  AND archived_at IS NULL
			  AND sensor_processing->>'lease_token' = ?
		`, string(procJSON), nextAttempt, id, profileID, version, leaseToken)
		w.logger.Info("sensor task rescheduled with backoff",
			zap.Uint("id", id),
			zap.String("err_code", errCode),
			zap.Duration("delay", delay))
		return
	}

	// Permanent failure or max attempts reached
	proc.RetryNotBefore = nil
	procJSON, _ := json.Marshal(proc)
	w.DB.Exec(`
		UPDATE analysis_results
		SET sensor_state = 'FAILED',
		    sensor_processing = ?::jsonb,
		    sensor_next_attempt_at = NULL,
		    updated_at = NOW()
		WHERE id = ?
		  AND profile_id = ?
		  AND sensor_version = ?
		  AND sensor_state = 'RUNNING'
		  AND archived_at IS NULL
		  AND sensor_processing->>'lease_token' = ?
	`, string(procJSON), id, profileID, version, leaseToken)
	w.logger.Warn("sensor task marked FAILED",
		zap.Uint("id", id),
		zap.String("err_code", errCode))
}
