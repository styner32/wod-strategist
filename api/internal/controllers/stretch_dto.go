package controllers

import "time"

type StretchResponse struct {
	ID           uint64    `json:"id"`
	Name         string    `json:"name"`
	TargetArea   string    `json:"target_area"`
	Description  string    `json:"description"`
	DurationHint string    `json:"duration_hint"`
	Caution      string    `json:"caution"`
	ImageURL     string    `json:"image_url,omitempty"`
	VideoURL     string    `json:"video_url,omitempty"`
	Aliases      []string  `json:"aliases"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type StretchInputRequest struct {
	Name         string   `json:"name"`
	TargetArea   string   `json:"target_area"`
	Description  string   `json:"description"`
	DurationHint string   `json:"duration_hint"`
	Caution      string   `json:"caution"`
	Aliases      []string `json:"aliases"`
}

type StretchMediaUploadURLRequest struct {
	MediaType string `json:"media_type"`
	Filename  string `json:"filename"`
}

type StretchMediaUploadURLResponse struct {
	UploadURL  string `json:"upload_url"`
	ObjectName string `json:"object_name"`
}

type SetStretchMediaRequest struct {
	MediaType  string `json:"media_type"`
	ObjectName string `json:"object_name"`
}

type RecommendedStretchSession struct {
	SessionID    string    `json:"session_id"`
	AnalysisID   uint      `json:"analysis_id"`
	AnalysisType string    `json:"analysis_type"`
	TargetArea   string    `json:"target_area"`
	Reason       string    `json:"reason"`
	DurationHint string    `json:"duration_hint,omitempty"`
	Caution      string    `json:"caution,omitempty"`
	Provisional  bool      `json:"provisional"`
	CreatedAt    time.Time `json:"created_at"`
}

type RecommendedStretchResponse struct {
	StretchResponse
	NormalizedKey     string                      `json:"normalized_key"`
	InCatalog         bool                        `json:"in_catalog"`
	SessionCount      int                         `json:"session_count"`
	LastRecommendedAt time.Time                   `json:"last_recommended_at"`
	Sessions          []RecommendedStretchSession `json:"sessions"`
}
