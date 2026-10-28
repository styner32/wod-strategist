package controllers

import (
	"time"

	"github.com/wod-strategist/api/internal/fatigue"
)

// PreWODAdviceRequest represents the payload sent when requesting pre-workout strategy advice.
type PreWODAdviceRequest struct {
	ProfileID      uint     `json:"profile_id" binding:"required"`
	WODDescription string   `json:"wod_description"`
	Movements      []string `json:"movements"`
}

// ProfileReadinessResponse represents deterministic muscle readiness and recent WOD summaries for HomeView.
type ProfileReadinessResponse struct {
	ProfileID           uint                   `json:"profile_id"`
	OverallFatigueScore *int                   `json:"overall_fatigue_score,omitempty"`
	OverallState        string                 `json:"overall_state"`
	OverallStateKO      string                 `json:"overall_state_ko"`
	MuscleReadiness     []MuscleReadinessItem  `json:"muscle_readiness,omitempty"`
	RecentWorkouts      []RecentWorkoutSummary `json:"recent_workouts,omitempty"`
	EvidenceStatus      string                 `json:"evidence_status"`
	Evidence            fatigue.EvidenceCounts `json:"evidence"`
	AsOf                time.Time              `json:"as_of"`
	LastWorkoutAt       *time.Time             `json:"last_workout_at,omitempty"`
}

// Aliases referencing canonical fatigue strategy DTOs
type MuscleReadinessItem = fatigue.MuscleReadinessItem
type TargetRPEInfo = fatigue.TargetRPEInfo
type ScalingAdviceItem = fatigue.ScalingAdviceItem
type MobilityWarmupItem = fatigue.MobilityWarmupItem
type HybridSensorBrief = fatigue.HybridSensorBrief
type RecentWorkoutSummary = fatigue.RecentWorkoutSummary
type EffortReflectionInfo = fatigue.EffortReflectionInfo
type PreWODAdviceResponse = fatigue.PreWODAdviceResponse
