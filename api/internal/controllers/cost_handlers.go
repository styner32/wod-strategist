package controllers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/cost"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

// GetSessionCost returns the aggregated token usage and cost breakdown for a session.
func (ctl *Controller) GetSessionCost(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("session_id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}

	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}

	var usages []db.TokenUsage
	if err := ctl.db.WithContext(c.Request.Context()).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("id ASC").
		Find(&usages).Error; err != nil {
		logger.Log.Error("failed to query token usages for session", zap.String("session_id", sessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query token usages"})
		return
	}

	resp := cost.CalculateSessionCost(sessionID, usages)
	c.JSON(http.StatusOK, resp)
}

// GetTotalCost returns the cumulative total tokens and costs across all sessions for the authenticated user.
func (ctl *Controller) GetTotalCost(c *gin.Context) {
	userID := UserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var profileIDs []uint
	pidStr := strings.TrimSpace(c.Query("profile_id"))
	if pidStr != "" {
		pid, err := strconv.ParseUint(pidStr, 10, 32)
		if err != nil || pid == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile_id"})
			return
		}
		if !ctl.assertOwnsProfile(c, uint(pid)) {
			return
		}
		profileIDs = []uint{uint(pid)}
	} else {
		if err := ctl.db.WithContext(c.Request.Context()).
			Model(&db.Profile{}).
			Where("user_id = ?", userID).
			Pluck("id", &profileIDs).Error; err != nil {
			logger.Log.Error("failed to find user profiles for cost analytics", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query analytics"})
			return
		}
	}

	if len(profileIDs) == 0 {
		c.JSON(http.StatusOK, cost.TotalCostResponse{})
		return
	}

	query := ctl.db.WithContext(c.Request.Context()).Model(&db.TokenUsage{}).
		Select("model, SUM(prompt_tokens) as prompt_tokens, SUM(candidate_tokens) as candidate_tokens, SUM(total_tokens) as total_tokens").
		Where("profile_id IN ?", profileIDs).
		Group("model")

	var aggs []cost.ModelTokenAggregate
	if err := query.Scan(&aggs).Error; err != nil {
		logger.Log.Error("failed to query total token usages", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query token usages"})
		return
	}

	resp := cost.CalculateTotalCostFromAggregates(aggs)
	c.JSON(http.StatusOK, resp)
}
