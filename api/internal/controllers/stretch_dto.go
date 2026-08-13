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
