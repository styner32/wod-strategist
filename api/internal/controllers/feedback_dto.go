package controllers

import "github.com/wod-strategist/api/internal/db"

const MaxFeedbackNoteLength = 500

type FeedbackCorrection struct {
	Accurate      *bool   `json:"accurate,omitempty"`
	MovementName  *string `json:"movement_name,omitempty"`
	ActivityState *string `json:"activity_state,omitempty"`
	FatigueState  *string `json:"fatigue_state,omitempty"`
}

type CreateFeedbackRequest struct {
	ClientRequestID  string             `json:"client_request_id"`
	TargetType       string             `json:"target_type"`
	ChunkID          *uint              `json:"chunk_id,omitempty"`
	Category         string             `json:"category"`
	Correction       FeedbackCorrection `json:"correction"`
	Note             string             `json:"note,omitempty"`
	ConsentToImprove bool               `json:"consent_to_improve,omitempty"`
	ReanalysisRunID  *uint              `json:"reanalysis_run_id,omitempty"`
}

type UpdateFeedbackRequest struct {
	ClientRequestID  string             `json:"client_request_id"`
	ExpectedRevision int                `json:"expected_revision"`
	Category         string             `json:"category,omitempty"`
	Correction       FeedbackCorrection `json:"correction"`
	Note             string             `json:"note,omitempty"`
	ConsentToImprove bool               `json:"consent_to_improve,omitempty"`
	ReanalysisRunID  *uint              `json:"reanalysis_run_id,omitempty"`
}

type DeleteFeedbackRequest struct {
	ClientRequestID  string `json:"client_request_id"`
	ExpectedRevision int    `json:"expected_revision"`
}

type FeedbackResponse struct {
	Feedback db.AnalysisFeedback `json:"feedback"`
}

type FeedbackListResponse struct {
	Current              []db.AnalysisFeedback `json:"current"`
	History              []db.AnalysisFeedback `json:"history"`
	HasActiveCorrections bool                  `json:"has_active_corrections"`
}
