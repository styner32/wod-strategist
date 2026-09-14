package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
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

	var profile db.Profile
	err := ctl.db.Where("id = ? AND user_id = ?", profileID, userID).First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Log.Warn("No workout profile found for current user",
				zap.Uint("profile_id", profileID),
				zap.Uint("user_id", userID))

			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not authorized for this profile"})
			return false
		}

		logger.Log.Warn("profile ownership check failed", zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
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

// verifySensorSessionOwnership performs strict write-side ownership validation:
// 1. Ensures profileID belongs to the authenticated user.
// 2. Checks sessions, analysis_results, and chunk_analysis_results for existing profile ownership.
// Returns (httpStatus, error). Returns (0, nil) if authorized and valid.
func (ctl *Controller) verifySensorSessionOwnership(ctx context.Context, tx *gorm.DB, sessionID string, profileID uint, userID uint) (int, error) {
	if profileID == 0 {
		return http.StatusBadRequest, errors.New("profile_id is required")
	}

	if userID == 0 {
		return http.StatusUnauthorized, errors.New("unauthorized")
	}

	var profile db.Profile
	if err := tx.WithContext(ctx).Where("id = ? AND user_id = ?", profileID, userID).First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return http.StatusForbidden, errors.New("not authorized for this profile")
		}
		return http.StatusInternalServerError, err
	}

	// 1. Check sessions
	var session db.Session
	err := tx.WithContext(ctx).Select("profile_id").Where("session_id = ?", sessionID).First(&session).Error
	if err == nil {
		if session.ProfileID != profileID {
			if userID > 0 {
				var otherProfile db.Profile
				if checkErr := tx.WithContext(ctx).Where("id = ? AND user_id = ?", session.ProfileID, userID).First(&otherProfile).Error; checkErr == nil {
					return http.StatusConflict, fmt.Errorf("session belongs to profile %d", session.ProfileID)
				}
			}
			return http.StatusForbidden, errors.New("session belongs to another user")
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusInternalServerError, err
	}

	// 2. Check chunk_analysis_results
	var chunkProfiles []uint
	err = tx.WithContext(ctx).Model(&db.ChunkAnalysisResult{}).
		Where("session_id = ?", sessionID).
		Distinct("profile_id").
		Pluck("profile_id", &chunkProfiles).Error
	if err != nil {
		return http.StatusInternalServerError, err
	}
	for _, cPid := range chunkProfiles {
		if cPid != profileID {
			if userID > 0 {
				var otherProfile db.Profile
				if checkErr := tx.WithContext(ctx).Where("id = ? AND user_id = ?", cPid, userID).First(&otherProfile).Error; checkErr == nil {
					return http.StatusConflict, fmt.Errorf("session chunks belong to profile %d", cPid)
				}
			}
			return http.StatusForbidden, errors.New("session chunks belong to another user")
		}
	}

	// 3. Check analysis_results
	var analysis db.AnalysisResult
	err = tx.WithContext(ctx).Select("profile_id").Where("session_id = ?", sessionID).First(&analysis).Error
	if err == nil {
		if analysis.ProfileID != profileID {
			if userID > 0 {
				var otherProfile db.Profile
				if checkErr := tx.WithContext(ctx).Where("id = ? AND user_id = ?", analysis.ProfileID, userID).First(&otherProfile).Error; checkErr == nil {
					return http.StatusConflict, fmt.Errorf("session analysis belongs to profile %d", analysis.ProfileID)
				}
			}
			return http.StatusForbidden, errors.New("session analysis belongs to another user")
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusInternalServerError, err
	}

	return 0, nil
}
