package controllers

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/cost"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

// Image parsing happens before a session/profile is selected. Attribute it to
// the authenticated user without fabricating a session or choosing a profile.
func (ctl *Controller) recordTokenUsage(c *gin.Context, profileID uint, task string, usage *gemini.TokenUsage) {
	if ctl.db == nil {
		return
	}
	if err := cost.RecordUsage(ctl.db.WithContext(context.WithoutCancel(c.Request.Context())), "", profileID, UserIDFromContext(c), task, "", usage); err != nil {
		logger.Log.Error("Failed to save token usage", zap.String("task_type", task), zap.Error(err))
	}
}
