package controllers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// GetSessionAnalysis returns the immutable production analysis together with
// every production chunk and the user's currently active corrections. It is a
// web-only aggregate and does not change the legacy array-shaped endpoints.
func (ctl *Controller) GetSessionAnalysis(c *gin.Context) {
	if ctl.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	var session db.Session
	sessionFound := true
	if err := ctl.db.WithContext(ctx).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sessionFound = false
		} else {
			logger.Log.Error("load session analysis metadata failed", zap.String("session_id", sessionID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
	}

	var analysis db.AnalysisResult
	hasAnalysis := true
	if err := ctl.db.WithContext(ctx).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("created_at DESC, id DESC").
		First(&analysis).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasAnalysis = false
		} else {
			logger.Log.Error("load session analysis result failed", zap.String("session_id", sessionID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
	}

	chunks := make([]db.ChunkAnalysisResult, 0)
	if err := ctl.db.WithContext(ctx).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("COALESCE(media_start_secs, start_secs) ASC NULLS LAST, created_at ASC, id ASC").
		Find(&chunks).Error; err != nil {
		logger.Log.Error("load session analysis chunks failed", zap.String("session_id", sessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	history := make([]db.AnalysisFeedback, 0)
	if err := ctl.db.WithContext(ctx).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("created_at DESC, id DESC").
		Find(&history).Error; err != nil {
		logger.Log.Error("load session analysis feedback failed", zap.String("session_id", sessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	activeFeedback := latestActiveFeedback(history)

	movementHints := make([]string, 0)
	workoutType := ""
	if sessionFound {
		var err error
		movementHints, err = decodeMovementHints(session.MovementHints)
		if err != nil {
			logger.Log.Error("decode session movement hints failed", zap.String("session_id", sessionID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		workoutType = session.WorkoutType
	}

	var analysisResponse *SessionAnalysisResultResponse
	if hasAnalysis {
		analysisResponse = &SessionAnalysisResultResponse{
			AnalysisResult: analysis,
			WorkoutType:    workoutType,
		}
	}

	var correctionsUpdatedAt *time.Time
	if len(activeFeedback) > 0 {
		updatedAt := activeFeedback[0].CreatedAt
		correctionsUpdatedAt = &updatedAt
	}

	c.JSON(http.StatusOK, SessionAnalysisResponse{
		SessionID:                   sessionID,
		Analysis:                    analysisResponse,
		Chunks:                      chunks,
		Feedback:                    activeFeedback,
		MovementHints:               movementHints,
		AdditionalObservedMovements: additionalObservedMovements(chunks, movementHints),
		CorrectionsUpdatedAt:        correctionsUpdatedAt,
	})
}

func additionalObservedMovements(chunks []db.ChunkAnalysisResult, movementHints []string) []string {
	excluded := make(map[string]struct{}, len(movementHints))
	for _, hint := range movementHints {
		excluded[strings.ToLower(strings.TrimSpace(hint))] = struct{}{}
	}

	observed := make([]string, 0)
	seen := make(map[string]struct{})
	for _, chunk := range chunks {
		if !strings.EqualFold(chunk.Status, "COMPLETED") {
			continue
		}
		movement := strings.TrimSpace(chunk.ExerciseType)
		key := strings.ToLower(movement)
		if isAggregateNonExerciseMovement(key) {
			continue
		}
		if _, isHint := excluded[key]; isHint {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		observed = append(observed, movement)
	}
	return observed
}

func isAggregateNonExerciseMovement(value string) bool {
	normalized := strings.NewReplacer("_", " ", "-", " ", "/", " ").Replace(value)
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "", "unknown", "walk", "walking", "rest", "setup", "rest setup", "recovery", "not exercise", "no exercise", "noexercise", "none", "n a":
		return true
	default:
		return false
	}
}
