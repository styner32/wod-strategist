package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var errTargetVideoNotFound = errors.New("target video not found")

// @Summary      Get Video Download URL
// @Description  Returns a time-limited signed URL for one requested video kind
// @Tags         video
// @Produce      json
// @Param        session_id path string true "Session ID"
// @Param        kind query string false "Video kind: merged (default), hardsubbed, or encoded"
// @Success      200 {object} VideoDownloadURLResponse
// @Failure      400 {object} ErrorResponse
// @Failure      404 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /video-download/:session_id [get]
func (ctl *Controller) GetVideoDownloadURL(c *gin.Context) {
	if ctl.storageClient == nil {
		logger.Log.Error("storage client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not configured"})
		return
	}

	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" || !isValidSessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required or invalid"})
		return
	}

	profileIDStr := c.DefaultQuery("profile_id", "0")
	var profileID uint
	if _, err := fmt.Sscanf(profileIDStr, "%d", &profileID); err != nil || profileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id query param is required"})
		return
	}

	if !ctl.assertOwnsProfile(c, profileID) {
		return
	}

	kind := strings.ToLower(strings.TrimSpace(c.DefaultQuery("kind", "merged")))
	if !isSupportedVideoKind(kind) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be 'merged', 'hardsubbed', or 'encoded'"})
		return
	}

	// Verify session ownership: use analysis_results as main table,
	// then chunk_analysis_results, and optional sessions table fallback.
	type sessionOwner struct {
		ProfileID uint
	}
	var owner sessionOwner

	// 1. Main table: analysis_results
	dbErr := ctl.db.WithContext(c.Request.Context()).Table("analysis_results").
		Select("profile_id").
		Where("session_id = ?", sessionID).
		First(&owner).Error

	// 2. Fallback: chunk_analysis_results
	if errors.Is(dbErr, gorm.ErrRecordNotFound) {
		dbErr = ctl.db.WithContext(c.Request.Context()).Table("chunk_analysis_results").
			Select("profile_id").
			Where("session_id = ?", sessionID).
			First(&owner).Error
	}

	// 3. Optional fallback: sessions (if table exists)
	if errors.Is(dbErr, gorm.ErrRecordNotFound) && ctl.db.Migrator().HasTable("sessions") {
		dbErr = ctl.db.WithContext(c.Request.Context()).Table("sessions").
			Select("profile_id").
			Where("session_id = ?", sessionID).
			First(&owner).Error
	}

	if dbErr == nil {
		if owner.ProfileID != profileID {
			logger.Log.Warn("video_download: profile id mismatch",
				zap.String("session_id", sessionID),
				zap.Uint("session_profile_id", owner.ProfileID),
				zap.Uint("requested_profile_id", profileID),
				zap.Uint("user_id", UserIDFromContext(c)))
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied to session"})
			return
		}
	} else if errors.Is(dbErr, gorm.ErrRecordNotFound) {
		// Fallback to parsing legacy session ID prefix if present
		match := legacyUploadSessionPattern.FindStringSubmatch(sessionID)
		if len(match) == 2 {
			parsed, err := strconv.ParseUint(match[1], 10, 32)
			if err != nil || uint(parsed) != profileID {
				logger.Log.Warn("video_download: legacy profile id mismatch",
					zap.String("session_id", sessionID),
					zap.Uint64("parsed_profile_id", parsed),
					zap.Uint("requested_profile_id", profileID),
					zap.Uint("user_id", UserIDFromContext(c)))
				c.JSON(http.StatusForbidden, gin.H{"error": "access denied to session for legacy"})
				return
			}
		} else {
			// DB record absent: profile ownership is already confirmed via assertOwnsProfile.
			// Proceed to GCS resolution which checks under videos/{profileID}/{sessionID}/...
			logger.Log.Info("video_download: session DB record absent, proceeding with GCS resolution for owned profile",
				zap.String("session_id", sessionID),
				zap.Uint("requested_profile_id", profileID),
				zap.Uint("user_id", UserIDFromContext(c)))
		}
	} else {
		logger.Log.Error("video_download: failed to query session for ownership check",
			zap.String("session_id", sessionID),
			zap.Error(dbErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify session ownership"})
		return
	}

	objectName, err := ctl.resolveTargetVideoObject(c.Request.Context(), profileID, sessionID, kind)
	if errors.Is(err, errTargetVideoNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("no %s video found for session", kind)})
		return
	}
	if err != nil {
		logger.Log.Error("failed to resolve target video",
			zap.String("session_id", sessionID),
			zap.String("kind", kind),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve video"})
		return
	}

	signedURL, err := ctl.storageClient.GenerateSignedURL(objectName, http.MethodGet, 15*time.Minute)
	if err != nil {
		logger.Log.Error("failed to generate download signed URL",
			zap.String("session_id", sessionID),
			zap.String("kind", kind),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate download URL"})
		return
	}

	c.JSON(http.StatusOK, VideoDownloadURLResponse{
		SessionID:   sessionID,
		Kind:        kind,
		DownloadURL: signedURL,
		Filename:    sessionID + "_" + kind + ".mp4",
		ExpiresAt:   time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339),
	})
}

func (ctl *Controller) resolveTargetVideoObject(ctx context.Context, profileID uint, sessionID, kind string) (string, error) {
	canonicalObject := fmt.Sprintf("videos/%d/%s/%s.mp4", profileID, sessionID, kind)
	objectInfos, err := ctl.storageClient.ListObjectInfos(ctx, canonicalObject)
	if err == nil {
		for _, info := range objectInfos {
			if info.Name == canonicalObject {
				return canonicalObject, nil
			}
		}
	}

	lookupSucceeded := err == nil
	lastErr := err
	prefixes := []string{fmt.Sprintf("videos/%d/%s/", profileID, sessionID)}
	if profileID != 0 {
		prefixes = append(prefixes, fmt.Sprintf("videos/0/%s/", sessionID))
	}
	prefixes = append(prefixes, fmt.Sprintf("videos/%s_", sessionID))

	for _, prefix := range prefixes {
		objectInfos, listErr := ctl.storageClient.ListObjectInfos(ctx, prefix)
		if listErr != nil {
			lastErr = listErr
			continue
		}
		lookupSucceeded = true
		if objectName := newestMatchingVideoObject(objectInfos, kind); objectName != "" {
			return objectName, nil
		}
	}

	if lookupSucceeded {
		return "", errTargetVideoNotFound
	}
	return "", fmt.Errorf("list target video objects: %w", lastErr)
}

func isSupportedVideoKind(kind string) bool {
	switch kind {
	case "merged", "hardsubbed", "encoded":
		return true
	default:
		return false
	}
}

func newestMatchingVideoObject(objectInfos []storage.ObjectInfo, kind string) string {
	var bestObject string
	var bestCreated time.Time
	for _, info := range objectInfos {
		if !matchesVideoKind(info.Name, kind) {
			continue
		}
		if bestObject == "" || info.Created.After(bestCreated) {
			bestObject = info.Name
			bestCreated = info.Created
		}
	}
	return bestObject
}

func matchesVideoKind(objectName, kind string) bool {
	base := filepath.Base(objectName)
	if !strings.HasSuffix(base, ".mp4") {
		return false
	}
	if kind == "encoded" {
		return base == "encoded.mp4" || strings.Contains(base, "_encoded")
	}
	return base == kind+".mp4" || strings.Contains(base, "_"+kind+"_")
}
