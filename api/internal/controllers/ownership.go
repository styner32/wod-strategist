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
	if userID == 0 {
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
			zap.Uint("user_id", userID),
			zap.Error(err))
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return false
	}

	return true
}

// assertOwnsSession checks that the given session_id belongs to a profile
// owned by the authenticated user. Returns true if ownership is confirmed
// or if no analysis data exists yet (brand-new session — caller will return
// empty results anyway). On failure it aborts with 403 and returns false.
func (ctl *Controller) assertOwnsSession(c *gin.Context, sessionID string) bool {
	userID := UserIDFromContext(c)
	if userID == 0 {
		return true
	}

	if ctl.db == nil {
		logger.Log.Error("db not configured for ownership check")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	// One query per table: total rows for the session, and how many of those
	// belong to a profile owned by this user. Errors propagate as 500.
	type counts struct {
		Total int64
		Owned int64
	}

	var analysis counts
	if err := ctl.db.Raw(`
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE p.user_id = ?) AS owned
		FROM analysis_results ar
		LEFT JOIN profiles p ON p.id = ar.profile_id
		WHERE ar.session_id = ?
	`, userID, sessionID).Scan(&analysis).Error; err != nil {
		logger.Log.Error("session ownership query (analysis) failed",
			zap.String("session_id", sessionID),
			zap.Uint("user_id", userID),
			zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	var chunks counts
	if err := ctl.db.Raw(`
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE p.user_id = ?) AS owned
		FROM chunk_analysis_results car
		LEFT JOIN profiles p ON p.id = car.profile_id
		WHERE car.session_id = ?
	`, userID, sessionID).Scan(&chunks).Error; err != nil {
		logger.Log.Error("session ownership query (chunks) failed",
			zap.String("session_id", sessionID),
			zap.Uint("user_id", userID),
			zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	// No data anywhere — brand-new session, still uploading/processing. Allow through.
	if analysis.Total == 0 && chunks.Total == 0 {
		return true
	}

	// Data exists for this session — user must own at least one row.
	if analysis.Owned == 0 && chunks.Owned == 0 {
		logger.Log.Warn("session ownership check failed",
			zap.String("session_id", sessionID),
			zap.Uint("user_id", userID))
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
	if userID == 0 {
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
			zap.Uint("user_id", userID),
			zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}

	if count == 0 {
		logger.Log.Warn("analysis ownership check failed",
			zap.Uint("analysis_id", analysisID),
			zap.Uint("user_id", userID))
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return false
	}

	return true
}
