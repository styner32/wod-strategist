package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

// assertOwnsProfile checks that the given profile belongs to the authenticated
// user. Returns true if ownership is confirmed. On failure it aborts the
// request with 403 and returns false.
func (ctl *Controller) assertOwnsProfile(c *gin.Context, profileID uint) bool {
	userID := UserIDFromContext(c)
	if userID == "" {
		// No auth context (e.g. auth middleware disabled) — allow through.
		return true
	}

	if ctl.db == nil {
		logger.Log.Error("db not configured for ownership check")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	var profile db.Profile
	err := ctl.db.Where("id = ? AND user_id = ?", profileID, userID).First(&profile).Error
	if err != nil {
		logger.Log.Warn("profile ownership check failed",
			zap.Uint("profile_id", profileID),
			zap.String("user_id", userID),
			zap.Error(err))
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return false
	}

	return true
}

// assertOwnsSession checks that the given session_id belongs to a profile
// owned by the authenticated user. Returns true if ownership is confirmed.
// On failure it aborts with 403 and returns false.
func (ctl *Controller) assertOwnsSession(c *gin.Context, sessionID string) bool {
	userID := UserIDFromContext(c)
	if userID == "" {
		return true
	}

	if ctl.db == nil {
		logger.Log.Error("db not configured for ownership check")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	// Check if any profile owned by this user has analysis data for this session.
	var count int64
	err := ctl.db.Model(&db.AnalysisResult{}).
		Joins("JOIN profiles ON profiles.id = analysis_results.profile_id").
		Where("profiles.user_id = ? AND analysis_results.session_id = ?", userID, sessionID).
		Limit(1).
		Count(&count).Error
	if err != nil {
		logger.Log.Error("session ownership query failed",
			zap.String("session_id", sessionID),
			zap.String("user_id", userID),
			zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	if count == 0 {
		// Also check chunk_analysis_results for sessions that only have chunks
		err = ctl.db.Model(&db.ChunkAnalysisResult{}).
			Joins("JOIN profiles ON profiles.id = chunk_analysis_results.profile_id").
			Where("profiles.user_id = ? AND chunk_analysis_results.session_id = ?", userID, sessionID).
			Limit(1).
			Count(&count).Error
		if err != nil {
			logger.Log.Error("session ownership query (chunks) failed",
				zap.String("session_id", sessionID),
				zap.String("user_id", userID),
				zap.Error(err))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return false
		}
	}

	if count == 0 {
		logger.Log.Warn("session ownership check failed",
			zap.String("session_id", sessionID),
			zap.String("user_id", userID))
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return false
	}

	return true
}

// assertOwnsAnalysis checks that the given analysis result ID belongs to a
// profile owned by the authenticated user. Returns true if ownership is
// confirmed. On failure it aborts with 403 and returns false.
func (ctl *Controller) assertOwnsAnalysis(c *gin.Context, analysisID uint) bool {
	userID := UserIDFromContext(c)
	if userID == "" {
		return true
	}

	if ctl.db == nil {
		logger.Log.Error("db not configured for ownership check")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	var count int64
	err := ctl.db.Model(&db.AnalysisResult{}).
		Joins("JOIN profiles ON profiles.id = analysis_results.profile_id").
		Where("profiles.user_id = ? AND analysis_results.id = ?", userID, analysisID).
		Limit(1).
		Count(&count).Error
	if err != nil {
		logger.Log.Error("analysis ownership query failed",
			zap.Uint("analysis_id", analysisID),
			zap.String("user_id", userID),
			zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	if count == 0 {
		logger.Log.Warn("analysis ownership check failed",
			zap.Uint("analysis_id", analysisID),
			zap.String("user_id", userID))
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return false
	}

	return true
}
