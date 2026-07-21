package controllers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

var (
	errLegacyUploadProfileRequired = errors.New("legacy upload profile_id is required")
	errLegacyUploadProfileInvalid  = errors.New("legacy upload profile_id is invalid")
	errLegacyUploadProfileMissing  = errors.New("legacy upload profile not found")
	errLegacyUploadProfileMismatch = errors.New("legacy upload profile does not match session")

	legacyUploadSessionPattern = regexp.MustCompile(`^P([1-9][0-9]*)-WOD-\d{4}-\d{2}-\d{2}-\d{2}-\d{2}$`)
)

// resolveLegacyUploadProfile keeps POST /upload compatible with its optional
// profile_id form field and old P{id}-WOD session IDs. Current session IDs do
// not encode profile ownership, so they must resolve through the sessions row
// or an explicitly supplied profile_id.
func (ctl *Controller) resolveLegacyUploadProfile(ctx context.Context, sessionID, rawProfileID string) (uint, error) {
	if ctl.db == nil {
		return 0, fmt.Errorf("database is not configured")
	}

	var requestedProfileID uint
	if raw := strings.TrimSpace(rawProfileID); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || parsed == 0 {
			return 0, errLegacyUploadProfileInvalid
		}
		requestedProfileID = uint(parsed)
	}

	var session db.Session
	sessionErr := ctl.db.WithContext(ctx).
		Select("session_id", "profile_id").
		Where("session_id = ?", sessionID).
		First(&session).Error
	if sessionErr != nil && !errors.Is(sessionErr, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("failed to resolve upload session: %w", sessionErr)
	}
	if sessionErr == nil {
		if requestedProfileID != 0 && requestedProfileID != session.ProfileID {
			return 0, errLegacyUploadProfileMismatch
		}
		requestedProfileID = session.ProfileID
	}

	if requestedProfileID == 0 {
		match := legacyUploadSessionPattern.FindStringSubmatch(sessionID)
		if len(match) == 2 {
			parsed, err := strconv.ParseUint(match[1], 10, 32)
			if err == nil {
				requestedProfileID = uint(parsed)
			}
		}
	}

	if requestedProfileID == 0 {
		return 0, errLegacyUploadProfileRequired
	}

	var profile db.Profile
	if err := ctl.db.WithContext(ctx).Select("id").First(&profile, requestedProfileID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errLegacyUploadProfileMissing
		}
		return 0, fmt.Errorf("failed to validate upload profile: %w", err)
	}

	return requestedProfileID, nil
}
