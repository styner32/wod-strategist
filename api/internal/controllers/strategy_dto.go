package controllers

import (
	"github.com/wod-strategist/api/internal/fatigue"
)

// PreWODAdviceRequest represents the payload sent when requesting pre-workout strategy advice.
type PreWODAdviceRequest struct {
	ProfileID      uint     `json:"profile_id" binding:"required"`
	WODDescription string   `json:"wod_description"`
	Movements      []string `json:"movements"`
}

// Aliases referencing canonical fatigue strategy DTOs
type MuscleReadinessItem = fatigue.MuscleReadinessItem
type TargetRPEInfo = fatigue.TargetRPEInfo
type ScalingAdviceItem = fatigue.ScalingAdviceItem
type MobilityWarmupItem = fatigue.MobilityWarmupItem
type PreWODAdviceResponse = fatigue.PreWODAdviceResponse
