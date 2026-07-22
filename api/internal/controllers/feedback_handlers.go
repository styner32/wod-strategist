package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var allowedActivityCorrections = map[string]struct{}{
	"exercise":     {},
	"walking":      {},
	"rest_setup":   {},
	"not_exercise": {},
	"unknown":      {},
}

var allowedFatigueCorrections = map[string]struct{}{
	"fatigued":     {},
	"not_fatigued": {},
	"walking_rest": {},
	"unknown":      {},
}

// CreateFeedback handles POST /sessions/:session_id/feedback. It captures the
// model's current prediction on the server and writes the first immutable event
// in a feedback revision chain.
func (ctl *Controller) CreateFeedback(c *gin.Context) {
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}

	var req CreateFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	req.TargetType = strings.TrimSpace(req.TargetType)
	req.Category = strings.TrimSpace(req.Category)
	req.Note = strings.TrimSpace(req.Note)

	if reason := validateClientRequestID(req.ClientRequestID); reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}

	if reason := validateFeedbackTarget(req.TargetType, req.ChunkID, req.Category); reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}
	if reason := normalizeAndValidateCorrection(req.Category, &req.Correction, req.Note); reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}
	if utf8.RuneCountInString(req.Note) > MaxFeedbackNoteLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "note must be 500 characters or fewer"})
		return
	}
	correction, err := marshalJSONDocument(req.Correction)
	if err != nil {
		logger.Log.Error("marshal feedback correction failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if existing, found, err := findFeedbackByClientRequestID(ctl.db, profileID, req.ClientRequestID); err != nil {
		logger.Log.Error("feedback idempotency lookup failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	} else if found {
		if !feedbackCreateMatches(existing, sessionID, req, correction) {
			c.JSON(http.StatusConflict, gin.H{"error": "client_request_id is already in use"})
			return
		}
		c.JSON(http.StatusOK, FeedbackResponse{Feedback: existing})
		return
	}

	original, err := ctl.captureOriginalPrediction(c, sessionID, profileID, req.TargetType, req.ChunkID)
	if err != nil {
		return
	}
	if err := ctl.validateFeedbackReanalysisRun(c, sessionID, profileID, req.ChunkID, req.ReanalysisRunID); err != nil {
		return
	}
	event := db.AnalysisFeedback{
		FeedbackKey:           uuid.NewString(),
		ProfileID:             profileID,
		SessionID:             sessionID,
		TargetType:            req.TargetType,
		ChunkAnalysisResultID: req.ChunkID,
		Category:              req.Category,
		OriginalPrediction:    original,
		Correction:            correction,
		Note:                  req.Note,
		ConsentToImprove:      req.ConsentToImprove,
		ClientRequestID:       req.ClientRequestID,
		Revision:              1,
		ReanalysisRunID:       req.ReanalysisRunID,
	}

	if err := ctl.db.WithContext(c.Request.Context()).Create(&event).Error; err != nil {
		if isFeedbackUniqueViolation(err) {
			if existing, found, findErr := findFeedbackByClientRequestID(ctl.db, profileID, req.ClientRequestID); findErr == nil && found && feedbackCreateMatches(existing, sessionID, req, correction) {
				c.JSON(http.StatusOK, FeedbackResponse{Feedback: existing})
				return
			}
			c.JSON(http.StatusConflict, gin.H{"error": "feedback conflicts with an existing request"})
			return
		}
		logger.Log.Error("create feedback failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusCreated, FeedbackResponse{Feedback: event})
}

// ListFeedback handles GET /sessions/:session_id/feedback. Current contains
// only the latest non-retracted event from each chain; History contains the
// complete append-only audit trail, newest first.
func (ctl *Controller) ListFeedback(c *gin.Context) {
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}

	history := make([]db.AnalysisFeedback, 0)
	if err := ctl.db.WithContext(c.Request.Context()).
		Where("profile_id = ? AND session_id = ?", profileID, sessionID).
		Order("created_at DESC, id DESC").
		Find(&history).Error; err != nil {
		logger.Log.Error("list feedback failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	current := latestActiveFeedback(history)
	c.JSON(http.StatusOK, FeedbackListResponse{
		Current:              current,
		History:              history,
		HasActiveCorrections: len(current) > 0,
	})
}

// UpdateFeedback handles PATCH /sessions/:session_id/feedback/:feedback_id by
// appending a new event. expected_revision prevents lost updates.
func (ctl *Controller) UpdateFeedback(c *gin.Context) {
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}
	feedbackID, ok := feedbackIDParam(c)
	if !ok {
		return
	}

	var req UpdateFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	req.Category = strings.TrimSpace(req.Category)
	req.Note = strings.TrimSpace(req.Note)
	if reason := validateClientRequestID(req.ClientRequestID); reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}
	if req.ExpectedRevision < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected_revision must be positive"})
		return
	}
	if utf8.RuneCountInString(req.Note) > MaxFeedbackNoteLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "note must be 500 characters or fewer"})
		return
	}

	event, status, err := ctl.appendFeedbackRevision(c, appendFeedbackRevisionParams{
		SessionID:        sessionID,
		ProfileID:        profileID,
		FeedbackID:       feedbackID,
		ClientRequestID:  req.ClientRequestID,
		ExpectedRevision: req.ExpectedRevision,
		Category:         req.Category,
		Correction:       &req.Correction,
		Note:             req.Note,
		ConsentToImprove: req.ConsentToImprove,
		ReanalysisRunID:  req.ReanalysisRunID,
	})
	if err != nil {
		writeFeedbackAppendError(c, status, err)
		return
	}
	c.JSON(http.StatusOK, FeedbackResponse{Feedback: event})
}

// DeleteFeedback handles DELETE /sessions/:session_id/feedback/:feedback_id.
// Deletion is a retraction event; no historical feedback row is removed.
func (ctl *Controller) DeleteFeedback(c *gin.Context) {
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}
	feedbackID, ok := feedbackIDParam(c)
	if !ok {
		return
	}

	var req DeleteFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	if reason := validateClientRequestID(req.ClientRequestID); reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}
	if req.ExpectedRevision < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected_revision must be positive"})
		return
	}

	event, status, err := ctl.appendFeedbackRevision(c, appendFeedbackRevisionParams{
		SessionID:        sessionID,
		ProfileID:        profileID,
		FeedbackID:       feedbackID,
		ClientRequestID:  req.ClientRequestID,
		ExpectedRevision: req.ExpectedRevision,
		Retract:          true,
	})
	if err != nil {
		writeFeedbackAppendError(c, status, err)
		return
	}
	c.JSON(http.StatusOK, FeedbackResponse{Feedback: event})
}

type appendFeedbackRevisionParams struct {
	SessionID        string
	ProfileID        uint
	FeedbackID       uint
	ClientRequestID  string
	ExpectedRevision int
	Category         string
	Correction       *FeedbackCorrection
	Note             string
	ConsentToImprove bool
	ReanalysisRunID  *uint
	Retract          bool
}

var (
	errFeedbackNotFound       = errors.New("feedback not found")
	errFeedbackForbidden      = errors.New("feedback is not owned by this session")
	errFeedbackStaleRevision  = errors.New("feedback has changed; reload and try again")
	errFeedbackAlreadyRetract = errors.New("feedback is already retracted")
	errFeedbackInvalid        = errors.New("invalid feedback")
)

func (ctl *Controller) appendFeedbackRevision(c *gin.Context, params appendFeedbackRevisionParams) (db.AnalysisFeedback, int, error) {
	var response db.AnalysisFeedback
	err := ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if existing, found, err := findFeedbackByClientRequestID(tx, params.ProfileID, params.ClientRequestID); err != nil {
			return err
		} else if found {
			var requested db.AnalysisFeedback
			if err := tx.First(&requested, params.FeedbackID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errFeedbackNotFound
				}
				return err
			}
			if existing.FeedbackKey != requested.FeedbackKey || existing.SessionID != params.SessionID || existing.Revision != params.ExpectedRevision+1 || existing.Retracted != params.Retract {
				return errFeedbackInvalid
			}
			if !params.Retract {
				category := requested.Category
				if params.Category != "" {
					category = params.Category
				}
				if existing.Category != category {
					return errFeedbackInvalid
				}
				if reason := normalizeAndValidateCorrection(category, params.Correction, params.Note); reason != "" {
					return feedbackValidationError{reason: reason}
				}
				correction, err := marshalJSONDocument(*params.Correction)
				if err != nil {
					return err
				}
				if !jsonDocumentsEqual(existing.Correction, correction) || existing.Note != params.Note || existing.ConsentToImprove != params.ConsentToImprove {
					return errFeedbackInvalid
				}
			}
			if params.ReanalysisRunID != nil && (existing.ReanalysisRunID == nil || *existing.ReanalysisRunID != *params.ReanalysisRunID) {
				return errFeedbackInvalid
			}
			response = existing
			return nil
		}

		var requested db.AnalysisFeedback
		if err := tx.First(&requested, params.FeedbackID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errFeedbackNotFound
			}
			return err
		}
		if requested.ProfileID != params.ProfileID || requested.SessionID != params.SessionID {
			return errFeedbackForbidden
		}

		var latest db.AnalysisFeedback
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("feedback_key = ?", requested.FeedbackKey).
			Order("revision DESC").
			First(&latest).Error; err != nil {
			return err
		}
		if latest.Revision != params.ExpectedRevision {
			return errFeedbackStaleRevision
		}
		if params.Retract && latest.Retracted {
			return errFeedbackAlreadyRetract
		}
		nextCategory := latest.Category
		if params.Category != "" {
			nextCategory = params.Category
		}
		if reason := validateFeedbackTarget(latest.TargetType, latest.ChunkAnalysisResultID, nextCategory); reason != "" {
			return feedbackValidationError{reason: reason}
		}

		next := db.AnalysisFeedback{
			FeedbackKey:           latest.FeedbackKey,
			ProfileID:             latest.ProfileID,
			SessionID:             latest.SessionID,
			TargetType:            latest.TargetType,
			ChunkAnalysisResultID: latest.ChunkAnalysisResultID,
			Category:              nextCategory,
			OriginalPrediction:    append(db.JSONDocument(nil), latest.OriginalPrediction...),
			Correction:            append(db.JSONDocument(nil), latest.Correction...),
			Note:                  latest.Note,
			ConsentToImprove:      latest.ConsentToImprove,
			ClientRequestID:       params.ClientRequestID,
			Revision:              latest.Revision + 1,
			SupersedesFeedbackID:  &latest.ID,
			Retracted:             params.Retract,
			ReanalysisRunID:       latest.ReanalysisRunID,
		}

		if !params.Retract {
			if reason := normalizeAndValidateCorrection(nextCategory, params.Correction, params.Note); reason != "" {
				return feedbackValidationError{reason: reason}
			}
			correction, err := marshalJSONDocument(*params.Correction)
			if err != nil {
				return err
			}
			next.Correction = correction
			next.Note = params.Note
			next.ConsentToImprove = params.ConsentToImprove
			if params.ReanalysisRunID != nil {
				if latest.ChunkAnalysisResultID == nil {
					return feedbackValidationError{reason: "reanalysis_run_id requires chunk feedback"}
				}
				valid, err := isValidFeedbackReanalysisRun(tx, latest.SessionID, latest.ProfileID, *latest.ChunkAnalysisResultID, *params.ReanalysisRunID)
				if err != nil {
					return err
				}
				if !valid {
					return feedbackValidationError{reason: "reanalysis_run_id must reference a completed run for this chunk"}
				}
				next.ReanalysisRunID = params.ReanalysisRunID
			}
		}

		if err := tx.Create(&next).Error; err != nil {
			if isFeedbackUniqueViolation(err) {
				return errFeedbackStaleRevision
			}
			return err
		}
		response = next
		return nil
	})

	if err == nil {
		return response, http.StatusOK, nil
	}
	var validationErr feedbackValidationError
	switch {
	case errors.As(err, &validationErr):
		return response, http.StatusBadRequest, validationErr
	case errors.Is(err, errFeedbackNotFound):
		return response, http.StatusNotFound, err
	case errors.Is(err, errFeedbackForbidden):
		return response, http.StatusForbidden, err
	case errors.Is(err, errFeedbackStaleRevision), errors.Is(err, errFeedbackAlreadyRetract), errors.Is(err, errFeedbackInvalid):
		return response, http.StatusConflict, err
	default:
		return response, http.StatusInternalServerError, err
	}
}

type feedbackValidationError struct {
	reason string
}

func (e feedbackValidationError) Error() string { return e.reason }

func writeFeedbackAppendError(c *gin.Context, status int, err error) {
	if status == http.StatusInternalServerError {
		logger.Log.Error("append feedback revision failed", zap.Error(err))
		c.JSON(status, gin.H{"error": "internal error"})
		return
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

func feedbackSessionID(c *gin.Context) (string, bool) {
	sessionID := strings.TrimSpace(c.Param("session_id"))
	if sessionID == "" || len(sessionID) > 200 || !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id"})
		return "", false
	}
	return sessionID, true
}

func feedbackIDParam(c *gin.Context) (uint, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(c.Param("feedback_id")), 10, 64)
	if err != nil || value == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid feedback_id"})
		return 0, false
	}
	return uint(value), true
}

// resolveFeedbackSession is deliberately stricter than assertOwnsSession: a
// missing session is never treated as an in-progress owned session. Sessions
// created before the sessions table existed still resolve through analysis or
// chunk rows.
func (ctl *Controller) resolveFeedbackSession(c *gin.Context, sessionID string) (uint, bool) {
	type ownerRow struct {
		ProfileID uint
		UserID    uint
	}
	var owners []ownerRow
	err := ctl.db.WithContext(c.Request.Context()).Raw(`
		SELECT profile_id, user_id
		FROM (
			SELECT s.profile_id, p.user_id, 1 AS priority
			FROM sessions s
			JOIN profiles p ON p.id = s.profile_id
			WHERE s.session_id = ?
			UNION ALL
			SELECT ar.profile_id, p.user_id, 2 AS priority
			FROM analysis_results ar
			JOIN profiles p ON p.id = ar.profile_id
			WHERE ar.session_id = ?
			UNION ALL
			SELECT car.profile_id, p.user_id, 3 AS priority
			FROM chunk_analysis_results car
			JOIN profiles p ON p.id = car.profile_id
			WHERE car.session_id = ?
		) candidates
		ORDER BY priority
		LIMIT 1
	`, sessionID, sessionID, sessionID).Scan(&owners).Error
	if err != nil {
		logger.Log.Error("feedback session ownership lookup failed", zap.String("session_id", sessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return 0, false
	}
	if len(owners) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return 0, false
	}

	userID := UserIDFromContext(c)
	if userID != 0 && owners[0].UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return 0, false
	}
	return owners[0].ProfileID, true
}

func (ctl *Controller) captureOriginalPrediction(c *gin.Context, sessionID string, profileID uint, targetType string, chunkID *uint) (db.JSONDocument, error) {
	if targetType == db.FeedbackTargetChunk {
		var chunk db.ChunkAnalysisResult
		if err := ctl.db.WithContext(c.Request.Context()).First(&chunk, *chunkID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "chunk not found"})
			} else {
				logger.Log.Error("load feedback chunk failed", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return nil, err
		}
		if chunk.SessionID != sessionID || chunk.ProfileID != profileID {
			c.JSON(http.StatusForbidden, gin.H{"error": "chunk is not owned by this session"})
			return nil, errFeedbackForbidden
		}

		observedSignals := any(chunk.ObservedSignals)
		var parsedSignals any
		if json.Unmarshal([]byte(chunk.ObservedSignals), &parsedSignals) == nil {
			observedSignals = parsedSignals
		}
		return marshalJSONDocument(map[string]any{
			"chunk_id":           chunk.ID,
			"status":             chunk.Status,
			"exercise_type":      chunk.ExerciseType,
			"output":             chunk.Output,
			"observed_signals":   observedSignals,
			"heart_rate_bpm":     chunk.HeartRateBPM,
			"start_secs":         chunk.StartSecs,
			"end_secs":           chunk.EndSecs,
			"media_start_secs":   chunk.MediaStartSecs,
			"media_end_secs":     chunk.MediaEndSecs,
			"motion_score":       chunk.MotionScore,
			"workout_confidence": chunk.WorkoutConfidence,
			"skip_reason":        chunk.SkipReason,
		})
	}

	snapshot := map[string]any{}
	var analysis db.AnalysisResult
	err := ctl.db.WithContext(c.Request.Context()).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("created_at DESC").First(&analysis).Error
	if err == nil {
		snapshot["analysis_id"] = analysis.ID
		snapshot["status"] = analysis.Status
		snapshot["output"] = analysis.Output
		snapshot["highlight_segments"] = analysis.HighlightSegments
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Log.Error("load feedback analysis failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, err
	}

	var session db.Session
	err = ctl.db.WithContext(c.Request.Context()).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		First(&session).Error
	if err == nil {
		snapshot["session_status"] = session.Status
		snapshot["wod_description"] = session.WODDescription
		snapshot["workout_type"] = session.WorkoutType
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Log.Error("load feedback session failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, err
	}
	return marshalJSONDocument(snapshot)
}

func (ctl *Controller) validateFeedbackReanalysisRun(c *gin.Context, sessionID string, profileID uint, chunkID, runID *uint) error {
	if runID == nil {
		return nil
	}
	if chunkID == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reanalysis_run_id requires a chunk target"})
		return errFeedbackInvalid
	}

	valid, err := isValidFeedbackReanalysisRun(ctl.db.WithContext(c.Request.Context()), sessionID, profileID, *chunkID, *runID)
	if err != nil {
		logger.Log.Error("validate feedback reanalysis run failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return err
	}
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reanalysis_run_id must reference a completed run for this chunk"})
		return errFeedbackInvalid
	}
	return nil
}

func isValidFeedbackReanalysisRun(database *gorm.DB, sessionID string, profileID, chunkID, runID uint) (bool, error) {
	var count int64
	err := database.Raw(`
		SELECT COUNT(*)
		FROM chunk_reanalysis_runs
		WHERE id = ?
		  AND session_id = ?
		  AND profile_id = ?
		  AND chunk_analysis_result_id = ?
		  AND status = 'COMPLETED'
	`, runID, sessionID, profileID, chunkID).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

func validateFeedbackTarget(targetType string, chunkID *uint, category string) string {
	switch targetType {
	case db.FeedbackTargetSession:
		if chunkID != nil {
			return "session feedback cannot include chunk_id"
		}
		if category != db.FeedbackCategorySessionAccuracy && category != db.FeedbackCategoryOther {
			return "invalid category for session feedback"
		}
	case db.FeedbackTargetChunk:
		if chunkID == nil || *chunkID == 0 {
			return "chunk feedback requires chunk_id"
		}
		if category != db.FeedbackCategoryMovement && category != db.FeedbackCategoryActivity && category != db.FeedbackCategoryFatigue && category != db.FeedbackCategoryOther {
			return "invalid category for chunk feedback"
		}
	default:
		return "target_type must be session or chunk"
	}
	return ""
}

func normalizeAndValidateCorrection(category string, correction *FeedbackCorrection, note string) string {
	if correction == nil {
		return "correction is required"
	}

	if category == db.FeedbackCategorySessionAccuracy {
		if correction.Accurate == nil || correction.MovementName != nil || correction.ActivityState != nil || correction.FatigueState != nil {
			return "session_accuracy requires only correction.accurate"
		}
		return ""
	}
	if correction.Accurate != nil {
		return "chunk feedback cannot include correction.accurate"
	}

	if correction.MovementName != nil {
		movement := strings.TrimSpace(*correction.MovementName)
		if ok, reason := validateMovements([]string{movement}); !ok {
			return reason
		}
		correction.MovementName = &movement
	}
	if correction.ActivityState != nil {
		activity := strings.TrimSpace(*correction.ActivityState)
		if _, ok := allowedActivityCorrections[activity]; !ok {
			return "invalid activity_state"
		}
		correction.ActivityState = &activity
	}
	if correction.FatigueState != nil {
		fatigue := strings.TrimSpace(*correction.FatigueState)
		if _, ok := allowedFatigueCorrections[fatigue]; !ok {
			return "invalid fatigue_state"
		}
		correction.FatigueState = &fatigue
	}

	if correction.MovementName != nil && correction.ActivityState != nil && *correction.ActivityState != "exercise" {
		return "movement_name requires activity_state exercise"
	}
	if correction.ActivityState != nil && *correction.ActivityState != "exercise" && correction.FatigueState != nil && *correction.FatigueState == "fatigued" {
		return "non-exercise activity cannot be marked fatigued"
	}

	switch category {
	case db.FeedbackCategoryMovement:
		if correction.MovementName == nil {
			return "movement feedback requires correction.movement_name"
		}
	case db.FeedbackCategoryActivity:
		if correction.ActivityState == nil {
			return "activity feedback requires correction.activity_state"
		}
	case db.FeedbackCategoryFatigue:
		if correction.FatigueState == nil {
			return "fatigue feedback requires correction.fatigue_state"
		}
	case db.FeedbackCategoryOther:
		if correction.MovementName != nil || correction.ActivityState != nil || correction.FatigueState != nil {
			return "other feedback accepts a note only"
		}
		if strings.TrimSpace(note) == "" {
			return "other feedback requires a note"
		}
	default:
		return "invalid feedback category"
	}
	return ""
}

func validateClientRequestID(value string) string {
	if value == "" {
		return "client_request_id is required"
	}
	if utf8.RuneCountInString(value) > 128 {
		return "client_request_id must be 128 characters or fewer"
	}
	return ""
}

func marshalJSONDocument(value any) (db.JSONDocument, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return db.JSONDocument(raw), nil
}

func findFeedbackByClientRequestID(database *gorm.DB, profileID uint, clientRequestID string) (db.AnalysisFeedback, bool, error) {
	var event db.AnalysisFeedback
	err := database.Where("profile_id = ? AND client_request_id = ?", profileID, clientRequestID).First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return event, false, nil
	}
	return event, err == nil, err
}

func feedbackCreateMatches(existing db.AnalysisFeedback, sessionID string, req CreateFeedbackRequest, correction db.JSONDocument) bool {
	return existing.Revision == 1 &&
		existing.SupersedesFeedbackID == nil &&
		!existing.Retracted &&
		existing.SessionID == sessionID &&
		existing.TargetType == req.TargetType &&
		equalOptionalUint(existing.ChunkAnalysisResultID, req.ChunkID) &&
		existing.Category == req.Category &&
		jsonDocumentsEqual(existing.Correction, correction) &&
		existing.Note == req.Note &&
		existing.ConsentToImprove == req.ConsentToImprove &&
		equalOptionalUint(existing.ReanalysisRunID, req.ReanalysisRunID)
}

func equalOptionalUint(left, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func jsonDocumentsEqual(left, right db.JSONDocument) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func latestActiveFeedback(history []db.AnalysisFeedback) []db.AnalysisFeedback {
	seen := make(map[string]struct{}, len(history))
	current := make([]db.AnalysisFeedback, 0, len(history))
	for _, event := range history {
		if _, ok := seen[event.FeedbackKey]; ok {
			continue
		}
		seen[event.FeedbackKey] = struct{}{}
		if !event.Retracted {
			current = append(current, event)
		}
	}
	sort.SliceStable(current, func(i, j int) bool {
		if current[i].CreatedAt.Equal(current[j].CreatedAt) {
			return current[i].ID > current[j].ID
		}
		return current[i].CreatedAt.After(current[j].CreatedAt)
	})
	return current
}

func isFeedbackUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "23505") || strings.Contains(message, "duplicate key")
}
