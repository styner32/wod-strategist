package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/modules/session"
	"go.uber.org/zap"
)

func (ctl *Controller) CreateSession(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("Failed to parse body for creating session", zap.Any("request", req), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if !ctl.assertOwnsProfile(c, req.ProfileID) {
		return
	}

	newSession, err := session.Create(c.Request.Context(), ctl.db, session.CreateSessionAttr{
		ProfileID:      req.ProfileID,
		SessionID:      req.SessionID,
		Movements:      req.Movements,
		WODDescription: req.WODDescription,
		WorkoutType:    req.WorkoutType,
		IdempotencyKey: req.IdempotencyKey,
	})

	if err != nil {
		logger.Log.Error("Failed to create session", zap.Any("request", req), zap.Error(err))

		switch err {
		case session.ErrInvalidSessionID:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		case session.ErrWorkoutTypeMismatch:
			c.JSON(http.StatusBadRequest, gin.H{"error": "workout type mismatch"})
			return
		case session.ErrSessionProfileConflict:
			c.JSON(http.StatusConflict, gin.H{"error": "session already exists for another profile"})
			return
		case session.ErrIdempotencyConflict:
			c.JSON(http.StatusConflict, gin.H{"error": "session already exists with this key"})
			return
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
			return
		}
	}

	c.JSON(http.StatusOK, CreateSessionResponse{
		SessionID:   newSession.SessionID,
		Status:      newSession.Status.String(),
		WorkoutType: newSession.WorkoutType,
	})
}
