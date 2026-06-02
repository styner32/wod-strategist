package controllers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (ctl *Controller) CreateSession(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("Failed to parse body for creating session", zap.Any("request", req), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	wodDescription := ""
	if req.WODDescription != "" {
		wodDescription = req.WODDescription
	} else if len(req.Movements) > 0 {
		wodDescription = "Individual movements: " + strings.Join(req.Movements, ",")
	} else {
		logger.Log.Warn("No WOD description or movements provided for CreateSession", zap.Any("request", req))
	}

	profile, err := gorm.G[db.Profile](ctl.db).Where("id = ?", req.ProfileID).First(c)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "profile not found"})
			return
		}

		logger.Log.Error("Failed to find profile", zap.Any("request", req), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to find profile"})
		return
	}

	if !ctl.assertOwnsProfile(c, profile.ID) {
		return
	}

	// WOD-{YYYYMMDDHHMM}-{ULID}
	sessionName := "WOD-" + time.Now().Format("200601021504") + "-" + ulid.Make().String()
	newSession := db.Session{SessionID: sessionName, ProfileID: req.ProfileID, IdempotencyKey: req.IdempotencyKey, WODDescription: wodDescription}
	if err := gorm.G[db.Session](ctl.db).Select("SessionID", "ProfileID", "IdempotencyKey", "WODDescription").Create(c, &newSession); err != nil {
		logger.Log.Error("Failed to create session", zap.Any("request", req), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	c.JSON(http.StatusOK, CreateSessionResponse{
		SessionID: newSession.SessionID,
		Status:    newSession.Status.String(),
	})
}
