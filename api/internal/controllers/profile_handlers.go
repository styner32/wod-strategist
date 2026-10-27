package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

func (ctl *Controller) CreateProfile(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	var req CreateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("failed to bind JSON for profile creation", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Validate injuries if provided
	var injuriesPtr *string
	if len(req.Injuries) > 0 {
		if !allowedInjuries.containsAll(req.Injuries) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid injuries"})
			return
		}
		injuriesJSON, err := json.Marshal(req.Injuries)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode injuries"})
			return
		}
		s := string(injuriesJSON)
		injuriesPtr = &s
	}

	fitnessLevel := req.FitnessLevel
	if fitnessLevel == "" {
		fitnessLevel = "intermediate"
	}

	var appearanceDoc db.JSONDocument
	if strings.TrimSpace(req.Appearance) != "" {
		doc, err := appearanceDocument(&AppearanceInput{Appearance: req.Appearance})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		appearanceDoc = doc
	}

	profile := &db.Profile{
		UserID:       UserIDFromContext(c),
		Name:         req.Name,
		BirthYear:    req.BirthYear,
		BirthMonth:   req.BirthMonth,
		BirthDay:     req.BirthDay,
		Gender:       req.Gender,
		HeightCm:     req.HeightCm,
		WeightKg:     req.WeightKg,
		RestingHRBPM: req.RestingHRBPM,
		FitnessLevel: fitnessLevel,
		Injuries:     injuriesPtr,
		Appearance:   appearanceDoc,
	}

	if err := ctl.profiles.Create(c.Request.Context(), profile); err != nil {
		logger.Log.Error("failed to create profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create profile"})
		return
	}

	c.JSON(http.StatusCreated, toProfileResponse(profile))
}

func (ctl *Controller) GetProfile(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile id"})
		return
	}

	profile, err := ctl.profiles.FindByID(c.Request.Context(), uint(id))
	if err != nil {
		logger.Log.Error("failed to find profile", zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}

	if !ctl.assertOwnsProfile(c, profile.ID) {
		return
	}

	c.JSON(http.StatusOK, toProfileResponse(profile))
}

func (ctl *Controller) ListProfiles(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	userID := UserIDFromContext(c)
	includeArchived := c.Query("include_archived") == "true"

	profiles, err := ctl.profiles.ListByUser(c.Request.Context(), userID, includeArchived)
	if err != nil {
		logger.Log.Error("failed to list profiles", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list profiles"})
		return
	}

	resp := make([]ProfileResponse, len(profiles))
	for i := range profiles {
		resp[i] = toProfileResponse(&profiles[i])
	}

	c.JSON(http.StatusOK, resp)
}

func (ctl *Controller) UpdateProfile(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile id"})
		return
	}

	profile, err := ctl.profiles.FindByID(c.Request.Context(), uint(id))
	if err != nil {
		logger.Log.Error("failed to find profile", zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}

	if !ctl.assertOwnsProfile(c, profile.ID) {
		return
	}

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Error("failed to parse request body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Name != nil {
		profile.Name = *req.Name
	}
	if req.BirthYear != nil {
		profile.BirthYear = req.BirthYear
	}
	if req.BirthMonth != nil {
		profile.BirthMonth = req.BirthMonth
	}
	if req.BirthDay != nil {
		profile.BirthDay = req.BirthDay
	}
	if req.Gender != nil {
		profile.Gender = req.Gender
	}
	if req.HeightCm != nil {
		profile.HeightCm = req.HeightCm
	}
	if req.WeightKg != nil {
		profile.WeightKg = req.WeightKg
	}
	if req.ClearRestingHR {
		profile.RestingHRBPM = nil
	} else if req.RestingHRBPM != nil {
		profile.RestingHRBPM = req.RestingHRBPM
	}
	if req.FitnessLevel != nil {
		profile.FitnessLevel = *req.FitnessLevel
	}
	if req.Injuries != nil {
		if !allowedInjuries.containsAll(req.Injuries) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid injuries"})
			return
		}
		injuriesJSON, err := json.Marshal(req.Injuries)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode injuries"})
			return
		}
		s := string(injuriesJSON)
		profile.Injuries = &s
	}
	if req.Appearance != nil {
		doc, err := appearanceDocument(&AppearanceInput{Appearance: *req.Appearance})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		profile.Appearance = doc
	}

	if err := ctl.profiles.Update(c.Request.Context(), profile); err != nil {
		logger.Log.Error("failed to update profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}

	c.JSON(http.StatusOK, toProfileResponse(profile))
}

func (ctl *Controller) ArchiveProfile(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile id"})
		return
	}

	if !ctl.assertOwnsProfile(c, uint(id)) {
		return
	}

	if err := ctl.profiles.Archive(c.Request.Context(), uint(id)); err != nil {
		logger.Log.Error("failed to archive profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to archive profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "profile archived"})
}

func (ctl *Controller) UnarchiveProfile(c *gin.Context) {
	if ctl.profiles == nil {
		logger.Log.Error("profile repository is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "profiles not configured"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile id"})
		return
	}

	if !ctl.assertOwnsProfile(c, uint(id)) {
		return
	}

	if err := ctl.profiles.Unarchive(c.Request.Context(), uint(id)); err != nil {
		logger.Log.Error("failed to unarchive profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unarchive profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "profile unarchived"})
}

func toProfileResponse(p *db.Profile) ProfileResponse {
	var injuryList []string
	if p.Injuries != nil && *p.Injuries != "" {
		_ = json.Unmarshal([]byte(*p.Injuries), &injuryList)
	}
	if injuryList == nil {
		injuryList = []string{}
	}

	resp := ProfileResponse{
		ID:                       p.ID,
		Name:                     p.Name,
		BirthYear:                p.BirthYear,
		BirthMonth:               p.BirthMonth,
		BirthDay:                 p.BirthDay,
		Gender:                   p.Gender,
		HeightCm:                 p.HeightCm,
		WeightKg:                 p.WeightKg,
		RestingHRBPM:             p.RestingHRBPM,
		SensorCalibrationVersion: p.SensorCalibrationVersion,
		FitnessLevel:             p.FitnessLevel,
		Injuries:                 injuryList,
		Appearance:               decodeAppearance(p.Appearance),
	}
	if p.ArchivedAt != nil {
		ts := p.ArchivedAt.Format(time.RFC3339)
		resp.ArchivedAt = &ts
	}
	return resp
}
