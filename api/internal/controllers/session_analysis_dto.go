package controllers

import (
	"time"

	"github.com/wod-strategist/api/internal/db"
)

type SessionAnalysisResultResponse struct {
	db.AnalysisResult
	WorkoutType string `json:"workout_type"`
}

type SessionAnalysisResponse struct {
	SessionID                   string                         `json:"session_id"`
	Analysis                    *SessionAnalysisResultResponse `json:"analysis"`
	Chunks                      []db.ChunkAnalysisResult       `json:"chunks"`
	Feedback                    []db.AnalysisFeedback          `json:"feedback"`
	MovementHints               []string                       `json:"movement_hints"`
	AdditionalObservedMovements []string                       `json:"additional_observed_movements"`
	CorrectionsUpdatedAt        *time.Time                     `json:"corrections_updated_at"`
}
