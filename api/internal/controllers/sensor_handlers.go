package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/timeline"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	uuidRegex   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	sha256Regex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type PrepareSensorUploadRequest struct {
	CalculationVersion int    `json:"calculation_version"`
	ProfileID          uint   `json:"profile_id" binding:"required"`
	RequestID          string `json:"request_id" binding:"required"`
	ExpectedVersion    string `json:"expected_version"`
	SizeBytes          int64  `json:"size_bytes" binding:"required"`
	SHA256             string `json:"sha256" binding:"required"`
}

type CalculationInputsSnapshot struct {
	CalculationVersion int     `json:"calculation_version"`
	Age                *int    `json:"age"`
	MaxHR              *int    `json:"max_hr"`
	MaxHRSource        *string `json:"max_hr_source"`
	HasHRZones         bool    `json:"has_hr_zones"`
}

type SensorProcessingData struct {
	SchemaVersion     int                       `json:"schema_version"`
	RequestID         string                    `json:"request_id"`
	BaseVersion       string                    `json:"base_version"`
	ObjectName        string                    `json:"object_name"`
	TargetGeneration  *string                   `json:"target_generation"`
	ExpectedSizeBytes int64                     `json:"expected_size_bytes"`
	ExpectedSHA256    string                    `json:"expected_sha256"`
	CalculationInputs CalculationInputsSnapshot `json:"calculation_inputs"`
	RequestedAt       time.Time                 `json:"requested_at"`
	UploadExpiresAt   time.Time                 `json:"upload_expires_at"`
	Attempts          int                       `json:"attempts"`
	LeaseToken        *string                   `json:"lease_token"`
	LeaseStartedAt    *time.Time                `json:"lease_started_at"`
	LastErrorMsg      *string                   `json:"last_error_msg,omitempty"`
	LastErrorCode     *string                   `json:"last_error_code"`
	RetryNotBefore    *time.Time                `json:"retry_not_before,omitempty"`
}

type PrepareSensorUploadResponse struct {
	RequestID       string            `json:"request_id"`
	Version         string            `json:"version"`
	State           string            `json:"state"`
	ObjectName      string            `json:"object_name"`
	UploadURL       string            `json:"upload_url,omitempty"`
	RequiredHeaders map[string]string `json:"required_headers,omitempty"`
	ExpiresAt       string            `json:"expires_at,omitempty"`
}

type CompleteSensorUploadRequest struct {
	ProfileID uint   `json:"profile_id" binding:"required"`
	RequestID string `json:"request_id" binding:"required"`
	Version   string `json:"version" binding:"required"`
}

type CompleteSensorUploadResponse struct {
	Accepted  bool   `json:"accepted"`
	RequestID string `json:"request_id,omitempty"`
	Version   string `json:"version,omitempty"`
	State     string `json:"state"`
	ErrorCode string `json:"error_code,omitempty"`
	Retryable bool   `json:"retryable"`
}

type SensorStatusResponse struct {
	RequestID     string  `json:"request_id"`
	Version       string  `json:"version"`
	State         string  `json:"state"`
	LastErrorCode *string `json:"last_error_code"`
	Retryable     bool    `json:"retryable"`
}

// PrepareSensorUpload handles POST /api/v1/sessions/:session_id/sensor-upload
func (ctl *Controller) PrepareSensorUpload(c *gin.Context) {
	sessionID := c.Param("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}

	var req PrepareSensorUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body", "details": err.Error()})
		return
	}

	if req.CalculationVersion == 0 {
		req.CalculationVersion = 1
	}
	if req.CalculationVersion != 1 && req.CalculationVersion != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported calculation_version"})
		return
	}
	if req.ProfileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id must be greater than 0"})
		return
	}
	if !uuidRegex.MatchString(req.RequestID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "request_id must be a valid UUID"})
		return
	}
	if req.SizeBytes < 1 || req.SizeBytes > 20*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "size_bytes must be between 1 and 20971520 bytes (20 MiB)"})
		return
	}
	if !sha256Regex.MatchString(req.SHA256) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sha256 must be a 64-character lowercase hex string"})
		return
	}

	expectedVersionInt, err := strconv.ParseInt(strings.TrimSpace(req.ExpectedVersion), 10, 64)
	if err != nil || expectedVersionInt < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected_version must be a non-negative decimal string"})
		return
	}

	userID := UserIDFromContext(c)
	ctx := c.Request.Context()

	var resp PrepareSensorUploadResponse
	var objectName string
	var newVersion int64
	var alreadyAccepted bool

	txErr := ctl.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Short lock and statement timeout as required by §4
		_ = tx.Exec("SET LOCAL lock_timeout = '500ms'").Error
		_ = tx.Exec("SET LOCAL statement_timeout = '2s'").Error

		status, authErr := ctl.verifySensorSessionOwnership(ctx, tx, sessionID, req.ProfileID, userID)
		if authErr != nil {
			c.JSON(status, gin.H{"error": authErr.Error()})
			return authErr
		}

		// Ensure minimal row exists in analysis_results without changing ownership
		initResult := &db.AnalysisResult{
			SessionID:    sessionID,
			ProfileID:    req.ProfileID,
			Status:       "PENDING",
			AnalysisType: db.AnalysisTypeWOD,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "session_id"}},
			DoNothing: true,
		}).Create(initResult).Error; err != nil {
			return fmt.Errorf("failed to ensure initial analysis row: %w", err)
		}

		var row db.AnalysisResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("session_id = ?", sessionID).
			First(&row).Error; err != nil {
			return fmt.Errorf("failed to lock analysis row: %w", err)
		}

		if row.ProfileID != req.ProfileID {
			c.JSON(http.StatusConflict, gin.H{"error": "session belongs to another profile", "error_code": "PROFILE_MISMATCH"})
			return errors.New("profile mismatch")
		}

		if row.ArchivedAt != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "session is archived", "error_code": "SESSION_ARCHIVED"})
			return errors.New("session archived")
		}

		var processing SensorProcessingData
		if len(row.SensorProcessing) > 0 && string(row.SensorProcessing) != "{}" {
			_ = json.Unmarshal(row.SensorProcessing, &processing)
		}

		// Check if request_id matches current request
		if processing.RequestID == req.RequestID {
			// Idempotent re-send check: verify payload content
			if processing.ExpectedSizeBytes != req.SizeBytes ||
				processing.ExpectedSHA256 != req.SHA256 ||
				processing.BaseVersion != req.ExpectedVersion ||
				(processing.CalculationInputs.CalculationVersion != req.CalculationVersion && !(processing.CalculationInputs.CalculationVersion == 0 && req.CalculationVersion == 1)) {
				c.JSON(http.StatusConflict, gin.H{"error": "request content conflict", "error_code": "REQUEST_CONTENT_CONFLICT"})
				return errors.New("request content conflict")
			}

			if row.SensorState == db.SensorStatePending ||
				row.SensorState == db.SensorStateRunning ||
				row.SensorState == db.SensorStateCompleted {
				alreadyAccepted = true
				resp = PrepareSensorUploadResponse{
					RequestID:  req.RequestID,
					Version:    strconv.FormatInt(row.SensorVersion, 10),
					State:      row.SensorState,
					ObjectName: processing.ObjectName,
				}
				return nil
			}

			if row.SensorState == db.SensorStateFailed || row.SensorState == db.SensorStateExpired {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":      fmt.Sprintf("request has ended in state %s", row.SensorState),
					"error_code": "REQUEST_TERMINATED",
				})
				return errors.New("request terminated")
			}

			// State is UPLOADING: re-issue URL for existing object
			objectName = processing.ObjectName
			newVersion = row.SensorVersion
			return nil
		}

		// Different request: check version sequence
		if expectedVersionInt != row.SensorVersion {
			c.JSON(http.StatusConflict, gin.H{
				"error":          fmt.Sprintf("expected version %d does not match server version %d", expectedVersionInt, row.SensorVersion),
				"error_code":     "SENSOR_VERSION_CONFLICT",
				"server_version": strconv.FormatInt(row.SensorVersion, 10),
			})
			return errors.New("sensor version conflict")
		}

		// Resolve canonical workout_at if not set
		if row.WorkoutAt == nil {
			var sess db.Session
			var sessCreatedAt *time.Time
			if err := tx.Select("created_at").Where("session_id = ?", sessionID).First(&sess).Error; err == nil {
				sessCreatedAt = &sess.CreatedAt
			}
			workoutAt, source, _ := timeline.ResolveWorkoutAt(sessionID, sessCreatedAt, &row.CreatedAt)
			if !workoutAt.IsZero() {
				row.WorkoutAt = &workoutAt
				row.WorkoutAtSource = &source
			}
		}

		// Freeze calculation inputs snapshot
		var profile db.Profile
		_ = tx.Select("birth_year").Where("id = ?", req.ProfileID).First(&profile).Error

		var calcInputs CalculationInputsSnapshot
		calcInputs.CalculationVersion = req.CalculationVersion

		refYear := time.Now().UTC().Year()
		if row.WorkoutAt != nil {
			refYear = row.WorkoutAt.UTC().Year()
		}

		if profile.BirthYear != nil && *profile.BirthYear > 1900 {
			age := refYear - *profile.BirthYear
			if age >= 13 && age <= 100 {
				calcInputs.Age = &age
				maxHR := 220 - age
				calcInputs.MaxHR = &maxHR
				src := "estimated_220_minus_age"
				calcInputs.MaxHRSource = &src
				calcInputs.HasHRZones = true
			}
		}

		newVersion = row.SensorVersion + 1
		objectName = fmt.Sprintf("videos/%d/%s/sensor_telemetry_v%d_%s.ndjson", req.ProfileID, sessionID, newVersion, req.RequestID)
		now := time.Now().UTC()
		uploadExpiresAt := now.Add(24 * time.Hour)

		processing = SensorProcessingData{
			SchemaVersion:     1,
			RequestID:         req.RequestID,
			BaseVersion:       req.ExpectedVersion,
			ObjectName:        objectName,
			TargetGeneration:  nil,
			ExpectedSizeBytes: req.SizeBytes,
			ExpectedSHA256:    req.SHA256,
			CalculationInputs: calcInputs,
			RequestedAt:       now,
			UploadExpiresAt:   uploadExpiresAt,
			Attempts:          0,
			LeaseToken:        nil,
			LeaseStartedAt:    nil,
			LastErrorCode:     nil,
			LastErrorMsg:      nil,
			RetryNotBefore:    nil,
		}

		procJSON, _ := json.Marshal(processing)

		updates := map[string]any{
			"sensor_version":         newVersion,
			"sensor_state":           db.SensorStateUploading,
			"sensor_processing":      db.JSONDocument(procJSON),
			"sensor_next_attempt_at": uploadExpiresAt,
			"updated_at":             now,
		}
		if row.WorkoutAt != nil {
			updates["workout_at"] = row.WorkoutAt
			updates["workout_at_source"] = row.WorkoutAtSource
		}

		return tx.Model(&row).Updates(updates).Error
	})

	if txErr != nil {
		return
	}

	if alreadyAccepted {
		c.JSON(http.StatusOK, resp)
		return
	}

	// Generate signed PUT URL outside the database lock
	signedURL, requiredHeaders, err := ctl.storageClient.GenerateCreateSignedURL(
		objectName,
		"application/x-ndjson",
		req.SHA256,
		15*time.Minute,
	)
	if err != nil {
		logger.Log.Error("failed to generate create signed URL for sensor", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
		return
	}

	c.JSON(http.StatusOK, PrepareSensorUploadResponse{
		RequestID:       req.RequestID,
		Version:         strconv.FormatInt(newVersion, 10),
		State:           db.SensorStateUploading,
		ObjectName:      objectName,
		UploadURL:       signedURL,
		RequiredHeaders: requiredHeaders,
		ExpiresAt:       time.Now().Add(15 * time.Minute).Format(time.RFC3339),
	})
}

// CompleteSensorUpload handles POST /api/v1/sessions/:session_id/sensor-complete
func (ctl *Controller) CompleteSensorUpload(c *gin.Context) {
	sessionID := c.Param("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}

	var req CompleteSensorUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body", "details": err.Error()})
		return
	}

	versionInt, err := strconv.ParseInt(strings.TrimSpace(req.Version), 10, 64)
	if err != nil || versionInt <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version must be a positive decimal string"})
		return
	}

	userID := UserIDFromContext(c)
	ctx := c.Request.Context()

	// 1. Initial check: query current state
	var row db.AnalysisResult
	err = ctl.db.WithContext(ctx).Where("session_id = ?", sessionID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session analysis not found", "error_code": "NOT_FOUND"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	status, authErr := ctl.verifySensorSessionOwnership(ctx, ctl.db, sessionID, req.ProfileID, userID)
	if authErr != nil {
		c.JSON(status, gin.H{"error": authErr.Error()})
		return
	}

	if row.ArchivedAt != nil || row.SensorVersion != versionInt {
		c.JSON(http.StatusConflict, gin.H{"error": "sensor request superseded", "error_code": "SENSOR_REQUEST_SUPERSEDED"})
		return
	}

	var processing SensorProcessingData
	if len(row.SensorProcessing) > 0 && string(row.SensorProcessing) != "{}" {
		_ = json.Unmarshal(row.SensorProcessing, &processing)
	}

	if processing.RequestID != req.RequestID {
		c.JSON(http.StatusConflict, gin.H{"error": "sensor request superseded", "error_code": "SENSOR_REQUEST_SUPERSEDED"})
		return
	}

	if row.SensorState == db.SensorStatePending || row.SensorState == db.SensorStateRunning {
		c.JSON(http.StatusAccepted, CompleteSensorUploadResponse{
			Accepted:  true,
			RequestID: req.RequestID,
			Version:   req.Version,
			State:     row.SensorState,
		})
		return
	}

	if row.SensorState == db.SensorStateCompleted {
		c.JSON(http.StatusOK, CompleteSensorUploadResponse{
			Accepted:  true,
			RequestID: req.RequestID,
			Version:   req.Version,
			State:     db.SensorStateCompleted,
		})
		return
	}

	if row.SensorState == db.SensorStateFailed {
		code := "FAILED"
		if processing.LastErrorCode != nil {
			code = *processing.LastErrorCode
		}
		c.JSON(http.StatusUnprocessableEntity, CompleteSensorUploadResponse{
			Accepted:  false,
			RequestID: req.RequestID,
			Version:   req.Version,
			State:     db.SensorStateFailed,
			ErrorCode: code,
			Retryable: false,
		})
		return
	}

	if row.SensorState == db.SensorStateExpired {
		c.JSON(http.StatusUnprocessableEntity, CompleteSensorUploadResponse{
			Accepted:  false,
			RequestID: req.RequestID,
			Version:   req.Version,
			State:     db.SensorStateExpired,
			ErrorCode: "UPLOAD_EXPIRED",
			Retryable: false,
		})
		return
	}

	if row.SensorState != db.SensorStateUploading {
		c.JSON(http.StatusConflict, gin.H{"error": "sensor request superseded", "error_code": "SENSOR_REQUEST_SUPERSEDED"})
		return
	}

	if time.Now().After(processing.UploadExpiresAt) {
		_ = ctl.db.WithContext(ctx).Model(&row).Where("id = ? AND sensor_state = 'UPLOADING'", row.ID).
			Update("sensor_state", db.SensorStateExpired).Error
		c.JSON(http.StatusUnprocessableEntity, CompleteSensorUploadResponse{
			Accepted:  false,
			RequestID: req.RequestID,
			Version:   req.Version,
			State:     db.SensorStateExpired,
			ErrorCode: "UPLOAD_EXPIRED",
			Retryable: false,
		})
		return
	}

	// 2. Outside lock: inspect GCS object attributes
	attrs, gcsErr := ctl.storageClient.ObjectAttrs(ctx, processing.ObjectName)
	if gcsErr != nil {
		if errors.Is(gcsErr, gcs.ErrObjectNotExist) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":      "uploaded object not found in storage",
				"error_code": "UPLOAD_NOT_FOUND",
			})
			return
		}
		logger.Log.Error("failed to query storage attributes for sensor complete", zap.Error(gcsErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage query error"})
		return
	}

	if attrs.Size != processing.ExpectedSizeBytes {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":      fmt.Sprintf("size mismatch: expected %d bytes, got %d", processing.ExpectedSizeBytes, attrs.Size),
			"error_code": "UPLOAD_CONTENT_MISMATCH",
		})
		return
	}

	if !strings.HasPrefix(attrs.ContentType, "application/x-ndjson") {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":      fmt.Sprintf("content type mismatch: expected application/x-ndjson, got %s", attrs.ContentType),
			"error_code": "UPLOAD_CONTENT_MISMATCH",
		})
		return
	}

	if attrs.Metadata != nil && attrs.Metadata["sha256"] != "" && !strings.EqualFold(attrs.Metadata["sha256"], processing.ExpectedSHA256) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":      fmt.Sprintf("metadata sha256 mismatch: expected %s, got %s", processing.ExpectedSHA256, attrs.Metadata["sha256"]),
			"error_code": "UPLOAD_CONTENT_MISMATCH",
		})
		return
	}

	generationStr := strconv.FormatInt(attrs.Generation, 10)

	// 3. Re-acquire lock and update to PENDING
	var analysisResultID uint
	txErr := ctl.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Exec("SET LOCAL lock_timeout = '500ms'").Error
		_ = tx.Exec("SET LOCAL statement_timeout = '2s'").Error

		var lockedRow db.AnalysisResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND profile_id = ? AND sensor_version = ? AND sensor_state = 'UPLOADING' AND archived_at IS NULL",
				row.ID, req.ProfileID, versionInt).
			First(&lockedRow).Error; err != nil {
			return err
		}

		var lockedProcessing SensorProcessingData
		if len(lockedRow.SensorProcessing) > 0 && string(lockedRow.SensorProcessing) != "{}" {
			_ = json.Unmarshal(lockedRow.SensorProcessing, &lockedProcessing)
		}

		if lockedProcessing.RequestID != req.RequestID {
			return errors.New("request superseded during verification")
		}

		analysisResultID = lockedRow.ID
		lockedProcessing.TargetGeneration = &generationStr
		procJSON, _ := json.Marshal(lockedProcessing)
		now := time.Now().UTC()

		return tx.Model(&lockedRow).Updates(map[string]any{
			"sensor_state":           db.SensorStatePending,
			"sensor_processing":      db.JSONDocument(procJSON),
			"sensor_next_attempt_at": now,
			"updated_at":             now,
		}).Error
	})

	if txErr != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "sensor request superseded", "error_code": "SENSOR_REQUEST_SUPERSEDED"})
		return
	}

	// 4. Enqueue task to asynq (SensorQueue)
	if ctl.queueClient != nil {
		task, taskErr := worker.NewSensorTelemetryTask(analysisResultID, req.ProfileID, req.RequestID, versionInt)
		if taskErr == nil {
			if _, enqErr := ctl.queueClient.Enqueue(task, asynq.Queue(worker.SensorQueueName), asynq.MaxRetry(0)); enqErr != nil {
				logger.Log.Warn("failed to enqueue sensor telemetry task immediately (recovery loop will handle it)",
					zap.Uint("analysis_result_id", analysisResultID),
					zap.Error(enqErr))
			}
		}
	}

	c.JSON(http.StatusAccepted, CompleteSensorUploadResponse{
		Accepted:  true,
		RequestID: req.RequestID,
		Version:   req.Version,
		State:     db.SensorStatePending,
	})
}

// GetSensorStatus handles GET /api/v1/sessions/:session_id/sensor-status
func (ctl *Controller) GetSensorStatus(c *gin.Context) {
	sessionID := c.Param("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}

	profileIDStr := c.Query("profile_id")
	profileID, _ := strconv.ParseUint(profileIDStr, 10, 32)
	if profileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id query parameter is required"})
		return
	}

	userID := UserIDFromContext(c)
	ctx := c.Request.Context()

	status, authErr := ctl.verifySensorSessionOwnership(ctx, ctl.db, sessionID, uint(profileID), userID)
	if authErr != nil {
		c.JSON(status, gin.H{"error": authErr.Error()})
		return
	}

	var row db.AnalysisResult
	err := ctl.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		First(&row).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusOK, SensorStatusResponse{
				RequestID: "",
				Version:   "0",
				State:     db.SensorStateNone,
				Retryable: false,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	if row.SensorVersion == 0 {
		c.JSON(http.StatusOK, SensorStatusResponse{
			RequestID: "",
			Version:   "0",
			State:     db.SensorStateNone,
			Retryable: false,
		})
		return
	}

	var processing SensorProcessingData
	if len(row.SensorProcessing) > 0 && string(row.SensorProcessing) != "{}" {
		_ = json.Unmarshal(row.SensorProcessing, &processing)
	}

	retryable := false
	if row.SensorState == db.SensorStateFailed && processing.Attempts < 5 {
		retryable = true
	}

	c.JSON(http.StatusOK, SensorStatusResponse{
		RequestID:     processing.RequestID,
		Version:       strconv.FormatInt(row.SensorVersion, 10),
		State:         row.SensorState,
		LastErrorCode: processing.LastErrorCode,
		Retryable:     retryable,
	})
}
