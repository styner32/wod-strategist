package controllers

import "time"

// PreWODAdviceRequest represents the payload sent when requesting pre-workout strategy advice.
type PreWODAdviceRequest struct {
	ProfileID      uint     `json:"profile_id" binding:"required"`
	WODDescription string   `json:"wod_description"`
	Movements      []string `json:"movements"`
}

// MuscleReadinessItem represents the readiness and coaching note for a specific muscle group.
type MuscleReadinessItem struct {
	Group        string `json:"group"`
	NameKO       string `json:"name_ko"`
	FatigueScore int    `json:"fatigue_score"` // 0 - 100
	State        string `json:"state"`         // fresh, moderate, fatigued, exhausted
	StateKO      string `json:"state_ko"`      // 신선, 보통, 피로 주의, 극심한 피로
	Note         string `json:"note"`          // AI generated context / note
}

// TargetRPEInfo represents the target exertion rating and pacing guide.
type TargetRPEInfo struct {
	Score          int    `json:"score"`           // 1 - 10
	Label          string `json:"label"`           // e.g. "RPE 7 (조절된 페이스)"
	PacingStrategy string `json:"pacing_strategy"` // pacing strategy guidance
}

// ScalingAdviceItem represents a movement-specific modification / weight advice.
type ScalingAdviceItem struct {
	Movement       string `json:"movement"`
	Recommendation string `json:"recommendation"` // e.g. "스케일링 추천", "적정 중량 유지"
	Detail         string `json:"detail"`         // concrete suggestion
}

// MobilityWarmupItem represents a recommended pre-workout mobility / stretch exercise.
type MobilityWarmupItem struct {
	Title      string `json:"title"`
	TargetArea string `json:"target_area"`
	Duration   string `json:"duration"`
	Reason     string `json:"reason"`
}

// PreWODAdviceResponse represents the complete 4-pillar coaching advice for today's WOD.
type PreWODAdviceResponse struct {
	ProfileID           uint                  `json:"profile_id"`
	OverallFatigueScore int                   `json:"overall_fatigue_score"`
	OverallState        string                `json:"overall_state"`
	OverallStateKO      string                `json:"overall_state_ko"`
	MuscleReadiness     []MuscleReadinessItem `json:"muscle_readiness"`
	TargetRPE           TargetRPEInfo         `json:"target_rpe"`
	ScalingAdvice       []ScalingAdviceItem   `json:"scaling_advice"`
	MobilityWarmup      []MobilityWarmupItem  `json:"mobility_warmup"`
	OverallSummary      string                `json:"overall_summary"`
	LastWorkoutAt       *time.Time            `json:"last_workout_at,omitempty"`
}
