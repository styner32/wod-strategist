package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (ctl *Controller) buildStretchResponse(s db.Stretch, aliasList []string) StretchResponse {
	if aliasList == nil {
		aliasList = make([]string, 0)
	}

	var imageURL, videoURL string
	if s.ImageObject != "" {
		url, err := ctl.storageClient.GenerateSignedURL(s.ImageObject, http.MethodGet, time.Hour)
		if err != nil {
			logger.Log.Warn("failed to generate signed image URL for stretch", zap.Uint64("stretch_id", s.ID), zap.Error(err))
		} else {
			imageURL = url
		}
	}

	if s.VideoObject != "" {
		url, err := ctl.storageClient.GenerateSignedURL(s.VideoObject, http.MethodGet, time.Hour)
		if err != nil {
			logger.Log.Warn("failed to generate signed video URL for stretch", zap.Uint64("stretch_id", s.ID), zap.Error(err))
		} else {
			videoURL = url
		}
	}

	return StretchResponse{
		ID:           s.ID,
		Name:         s.Name,
		TargetArea:   s.TargetArea,
		Description:  s.Description,
		DurationHint: s.DurationHint,
		Caution:      s.Caution,
		ImageURL:     imageURL,
		VideoURL:     videoURL,
		Aliases:      aliasList,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
	}
}

// ListStretches handles GET /stretches.
func (ctl *Controller) ListStretches(c *gin.Context) {
	stretches, err := db.ListStretches(c.Request.Context(), ctl.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list stretches"})
		return
	}

	aliases, err := db.ListStretchAliases(c.Request.Context(), ctl.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list stretch aliases"})
		return
	}

	aliasMap := make(map[uint64][]string)
	for _, a := range aliases {
		aliasMap[a.StretchID] = append(aliasMap[a.StretchID], a.Alias)
	}

	resp := make([]StretchResponse, 0, len(stretches))
	for _, s := range stretches {
		resp = append(resp, ctl.buildStretchResponse(s, aliasMap[s.ID]))
	}

	c.JSON(http.StatusOK, resp)
}

// CreateStretch handles POST /stretches.
func (ctl *Controller) CreateStretch(c *gin.Context) {
	var req StretchInputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.TargetArea = strings.TrimSpace(req.TargetArea)
	req.Description = strings.TrimSpace(req.Description)
	req.DurationHint = strings.TrimSpace(req.DurationHint)
	req.Caution = strings.TrimSpace(req.Caution)

	nameRuneCount := utf8.RuneCountInString(req.Name)
	if nameRuneCount < 1 || nameRuneCount > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must be between 1 and 120 characters"})
		return
	}
	if utf8.RuneCountInString(req.Description) > 4000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "description exceeds max length of 4000"})
		return
	}
	if utf8.RuneCountInString(req.DurationHint) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "duration_hint exceeds max length of 200"})
		return
	}
	if utf8.RuneCountInString(req.Caution) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "caution exceeds max length of 1000"})
		return
	}

	nameKey := db.NormalizeStretchKey(req.Name)
	seenAliasKeys := make(map[string]struct{})
	cleanAliases := make([]string, 0)
	cleanAliasKeys := make([]string, 0)

	for _, a := range req.Aliases {
		trimmedA := strings.TrimSpace(a)
		aliasRuneCount := utf8.RuneCountInString(trimmedA)
		if aliasRuneCount < 1 || aliasRuneCount > 120 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "alias must be between 1 and 120 characters"})
			return
		}
		keyA := db.NormalizeStretchKey(trimmedA)
		if keyA == "" || keyA == nameKey {
			continue
		}
		if _, dup := seenAliasKeys[keyA]; dup {
			continue
		}
		seenAliasKeys[keyA] = struct{}{}
		cleanAliases = append(cleanAliases, trimmedA)
		cleanAliasKeys = append(cleanAliasKeys, keyA)
	}

	allKeys := append([]string{nameKey}, cleanAliasKeys...)

	var stretchCount, aliasCount int64
	if err := ctl.db.WithContext(c.Request.Context()).Model(&db.Stretch{}).Where("normalized_key IN ?", allKeys).Count(&stretchCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check stretch key collision"})
		return
	}
	if err := ctl.db.WithContext(c.Request.Context()).Model(&db.StretchAlias{}).Where("normalized_key IN ?", allKeys).Count(&aliasCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check alias key collision"})
		return
	}
	if stretchCount > 0 || aliasCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "name or alias already in use"})
		return
	}

	var createdStretch db.Stretch
	err := ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		createdStretch = db.Stretch{
			Name:          req.Name,
			NormalizedKey: nameKey,
			TargetArea:    req.TargetArea,
			Description:   req.Description,
			DurationHint:  req.DurationHint,
			Caution:       req.Caution,
			ImageObject:   "",
			VideoObject:   "",
		}
		if err := tx.Create(&createdStretch).Error; err != nil {
			return err
		}
		for i, aliasStr := range cleanAliases {
			aliasRow := db.StretchAlias{
				StretchID:     createdStretch.ID,
				Alias:         aliasStr,
				NormalizedKey: cleanAliasKeys[i],
			}
			if err := tx.Create(&aliasRow).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create stretch"})
		return
	}

	c.JSON(http.StatusCreated, ctl.buildStretchResponse(createdStretch, cleanAliases))
}

// UpdateStretch handles PUT /stretches/:id.
func (ctl *Controller) UpdateStretch(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stretch id"})
		return
	}

	var existing db.Stretch
	if err := ctl.db.WithContext(c.Request.Context()).First(&existing, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "stretch not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch stretch"})
		return
	}

	var req StretchInputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.TargetArea = strings.TrimSpace(req.TargetArea)
	req.Description = strings.TrimSpace(req.Description)
	req.DurationHint = strings.TrimSpace(req.DurationHint)
	req.Caution = strings.TrimSpace(req.Caution)

	nameRuneCount := utf8.RuneCountInString(req.Name)
	if nameRuneCount < 1 || nameRuneCount > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must be between 1 and 120 characters"})
		return
	}
	if utf8.RuneCountInString(req.Description) > 4000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "description exceeds max length of 4000"})
		return
	}
	if utf8.RuneCountInString(req.DurationHint) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "duration_hint exceeds max length of 200"})
		return
	}
	if utf8.RuneCountInString(req.Caution) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "caution exceeds max length of 1000"})
		return
	}

	nameKey := db.NormalizeStretchKey(req.Name)
	seenAliasKeys := make(map[string]struct{})
	cleanAliases := make([]string, 0)
	cleanAliasKeys := make([]string, 0)

	for _, a := range req.Aliases {
		trimmedA := strings.TrimSpace(a)
		aliasRuneCount := utf8.RuneCountInString(trimmedA)
		if aliasRuneCount < 1 || aliasRuneCount > 120 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "alias must be between 1 and 120 characters"})
			return
		}
		keyA := db.NormalizeStretchKey(trimmedA)
		if keyA == "" || keyA == nameKey {
			continue
		}
		if _, dup := seenAliasKeys[keyA]; dup {
			continue
		}
		seenAliasKeys[keyA] = struct{}{}
		cleanAliases = append(cleanAliases, trimmedA)
		cleanAliasKeys = append(cleanAliasKeys, keyA)
	}

	allKeys := append([]string{nameKey}, cleanAliasKeys...)

	var stretchCount, aliasCount int64
	if err := ctl.db.WithContext(c.Request.Context()).Model(&db.Stretch{}).Where("normalized_key IN ? AND id != ?", allKeys, id).Count(&stretchCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check stretch key collision"})
		return
	}
	if err := ctl.db.WithContext(c.Request.Context()).Model(&db.StretchAlias{}).Where("normalized_key IN ? AND stretch_id != ?", allKeys, id).Count(&aliasCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check alias key collision"})
		return
	}
	if stretchCount > 0 || aliasCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "name or alias already in use"})
		return
	}

	err = ctl.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		existing.Name = req.Name
		existing.NormalizedKey = nameKey
		existing.TargetArea = req.TargetArea
		existing.Description = req.Description
		existing.DurationHint = req.DurationHint
		existing.Caution = req.Caution
		existing.UpdatedAt = time.Now()

		if err := tx.Model(&existing).Select("name", "normalized_key", "target_area", "description", "duration_hint", "caution", "updated_at").Updates(&existing).Error; err != nil {
			return err
		}

		if err := tx.Where("stretch_id = ?", id).Delete(&db.StretchAlias{}).Error; err != nil {
			return err
		}

		for i, aliasStr := range cleanAliases {
			aliasRow := db.StretchAlias{
				StretchID:     id,
				Alias:         aliasStr,
				NormalizedKey: cleanAliasKeys[i],
			}
			if err := tx.Create(&aliasRow).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update stretch"})
		return
	}

	c.JSON(http.StatusOK, ctl.buildStretchResponse(existing, cleanAliases))
}

// DeleteStretch handles DELETE /stretches/:id.
func (ctl *Controller) DeleteStretch(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stretch id"})
		return
	}

	res := ctl.db.WithContext(c.Request.Context()).Delete(&db.Stretch{}, id)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete stretch"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "stretch not found"})
		return
	}

	c.Status(http.StatusNoContent)
}

// CreateStretchMediaUploadURL handles POST /stretches/:id/media-upload-url.
func (ctl *Controller) CreateStretchMediaUploadURL(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stretch id"})
		return
	}

	var existing db.Stretch
	if err := ctl.db.WithContext(c.Request.Context()).First(&existing, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "stretch not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch stretch"})
		return
	}

	var req StretchMediaUploadURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.MediaType = strings.ToLower(strings.TrimSpace(req.MediaType))
	if req.MediaType != "image" && req.MediaType != "video" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "media_type must be image or video"})
		return
	}

	objectName := fmt.Sprintf("stretches/%d/%d_%s_%s", id, time.Now().Unix(), req.MediaType, sanitizeObjectPart(req.Filename, "media.bin"))
	uploadURL, err := ctl.storageClient.GenerateSignedURL(objectName, http.MethodPut, 15*time.Minute)
	if err != nil {
		logger.Log.Error("failed to generate signed upload URL for stretch", zap.Uint64("stretch_id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload url"})
		return
	}

	c.JSON(http.StatusOK, StretchMediaUploadURLResponse{
		UploadURL:  uploadURL,
		ObjectName: objectName,
	})
}

// SetStretchMedia handles POST /stretches/:id/media.
func (ctl *Controller) SetStretchMedia(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stretch id"})
		return
	}

	var existing db.Stretch
	if err := ctl.db.WithContext(c.Request.Context()).First(&existing, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "stretch not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch stretch"})
		return
	}

	var req SetStretchMediaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	req.MediaType = strings.ToLower(strings.TrimSpace(req.MediaType))
	req.ObjectName = strings.TrimSpace(req.ObjectName)

	if req.MediaType != "image" && req.MediaType != "video" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "media_type must be image or video"})
		return
	}

	expectedPrefix := fmt.Sprintf("stretches/%d/", id)
	if !strings.HasPrefix(req.ObjectName, expectedPrefix) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "object_name must start with stretches/{id}/"})
		return
	}

	columnToUpdate := "image_object"
	if req.MediaType == "image" {
		existing.ImageObject = req.ObjectName
	} else if req.MediaType == "video" {
		existing.VideoObject = req.ObjectName
		columnToUpdate = "video_object"
	}
	existing.UpdatedAt = time.Now()

	if err := ctl.db.WithContext(c.Request.Context()).Model(&existing).Select(columnToUpdate, "updated_at").Updates(&existing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update stretch media"})
		return
	}

	var aliases []db.StretchAlias
	_ = ctl.db.WithContext(c.Request.Context()).Where("stretch_id = ?", id).Find(&aliases).Error
	aliasList := make([]string, 0, len(aliases))
	for _, a := range aliases {
		aliasList = append(aliasList, a.Alias)
	}

	c.JSON(http.StatusOK, ctl.buildStretchResponse(existing, aliasList))
}

// ClearStretchMedia handles DELETE /stretches/:id/media.
// Accepts optional ?kind=image or ?kind=video or ?media_type=image|video to selectively clear one media,
// or clears all media if omitted.
func (ctl *Controller) ClearStretchMedia(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stretch id"})
		return
	}

	var existing db.Stretch
	if err := ctl.db.WithContext(c.Request.Context()).First(&existing, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "stretch not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch stretch"})
		return
	}

	kind := strings.ToLower(strings.TrimSpace(c.DefaultQuery("kind", c.Query("media_type"))))
	if kind == "image" {
		existing.ImageObject = ""
	} else if kind == "video" {
		existing.VideoObject = ""
	} else {
		existing.ImageObject = ""
		existing.VideoObject = ""
	}

	existing.UpdatedAt = time.Now()

	if err := ctl.db.WithContext(c.Request.Context()).Model(&existing).Select("image_object", "video_object", "updated_at").Updates(&existing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clear stretch media"})
		return
	}

	var aliases []db.StretchAlias
	_ = ctl.db.WithContext(c.Request.Context()).Where("stretch_id = ?", id).Find(&aliases).Error
	aliasList := make([]string, 0, len(aliases))
	for _, a := range aliases {
		aliasList = append(aliasList, a.Alias)
	}

	c.JSON(http.StatusOK, ctl.buildStretchResponse(existing, aliasList))
}
