package controllers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/cost"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

// GetSessionCost returns the aggregated token usage and cost breakdown for a session.
func (ctl *Controller) GetSessionCost(c *gin.Context) {
	sessionID := sanitizeIdentifier(strings.TrimSpace(c.Param("session_id")))
	if sessionID == "" || !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id"})
		return
	}

	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}

	var usages []db.TokenUsage
	if err := ctl.db.WithContext(c.Request.Context()).
		Omit("usage_metadata").
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("id ASC").
		Find(&usages).Error; err != nil {
		logger.Log.Error("failed to query token usages for session", zap.String("session_id", sessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query token usages"})
		return
	}

	var stageMetrics []db.PipelineStageMetric
	if err := ctl.db.WithContext(c.Request.Context()).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("id ASC").
		Find(&stageMetrics).Error; err != nil {
		logger.Log.Warn("failed to query pipeline stage metrics for session", zap.String("session_id", sessionID), zap.Error(err))
	}

	resp := cost.CalculateSessionCost(sessionID, usages)
	cost.ApplyStageMetrics(&resp, stageMetrics)
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

	// Keep introductory and standard Argon usage (plus batch 50% discount) in separate groups.
	// Uses scalar columns synced at write/migration time instead of scanning usage_metadata JSONB.
	groupExpr := fmt.Sprintf(
		"model, (lower(trim(task_type)) LIKE '%%:batch'), CASE WHEN lower(trim(model)) = '%s' AND created_at < TIMESTAMPTZ '%s' THEN 1 ELSE 0 END",
		gemini.ModelArgon4,
		cost.ArgonStandardPricingStartsAt.Format(time.RFC3339),
	)
	query := ctl.db.WithContext(c.Request.Context()).Model(&db.TokenUsage{}).
		Select(`model,
   (lower(trim(task_type)) LIKE '%:batch') as is_batch,
   MIN(created_at) as pricing_at,
   COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
   COALESCE(SUM(candidate_tokens), 0) as candidate_tokens,
   COALESCE(SUM(total_tokens), 0) as total_tokens,
   COALESCE(SUM(thinking_tokens), 0) as thinking_tokens,
   COALESCE(SUM(tool_use_tokens), 0) as tool_use_tokens,
   COALESCE(SUM(cached_tokens), 0) as cached_tokens,
   COALESCE(SUM(unmeasured_calls), 0) as unmeasured_calls,
   COUNT(*) FILTER (WHERE lower(trim(model)) LIKE 'lyria-%') as unpriced_calls`).
		Group(groupExpr)
	if pidStr != "" {
		query = query.Where("profile_id IN ?", profileIDs)
	} else {
		query = query.Where("profile_id IN ? OR user_id = ?", profileIDs, userID)
	}

	var aggs []cost.ModelTokenAggregate
	if err := query.Scan(&aggs).Error; err != nil {
		logger.Log.Error("failed to query total token usages", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query token usages"})
		return
	}

	resp := cost.CalculateTotalCostFromAggregates(aggs)
	c.JSON(http.StatusOK, resp)
}
