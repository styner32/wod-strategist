package controllers

type CreateUploadURLRequest struct {
	SessionID string `json:"session_id"`
	Filename  string `json:"filename"`
}

type CreateUploadURLResponse struct {
	UploadURL string `json:"upload_url"`
	GCSURI    string `json:"gcs_uri"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type CompleteUploadRequest struct {
	SessionID   string   `json:"session_id"`
	GCSURI      string   `json:"gcs_uri"`
	Movements   []string `json:"movements"`
	Injuries    []string `json:"injuries"`
	WorkoutType string   `json:"workout_type"`
	ProfileID   uint     `json:"profile_id"`
}

type CompleteUploadResponse struct {
	Message   string `json:"message"`
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
}

type CreateProfileRequest struct {
	Name       string  `json:"name"`
	BirthYear  int     `json:"birth_year" binding:"required,min=1900"`
	BirthMonth int     `json:"birth_month" binding:"required,min=1,max=12"`
	BirthDay   int     `json:"birth_day" binding:"required,min=1,max=31"`
	Gender     string  `json:"gender" binding:"required,oneof=male female other"`
	HeightCm   int     `json:"height_cm" binding:"required,min=50,max=300"`
	WeightKg   float64 `json:"weight_kg" binding:"required,min=20,max=500"`
}

type UpdateProfileRequest struct {
	Name       *string  `json:"name"`
	BirthYear  *int     `json:"birth_year" binding:"omitempty,min=1900"`
	BirthMonth *int     `json:"birth_month" binding:"omitempty,min=1,max=12"`
	BirthDay   *int     `json:"birth_day" binding:"omitempty,min=1,max=31"`
	Gender     *string  `json:"gender" binding:"omitempty,oneof=male female other"`
	HeightCm   *int     `json:"height_cm" binding:"omitempty,min=50,max=300"`
	WeightKg   *float64 `json:"weight_kg" binding:"omitempty,min=20,max=500"`
}

type ProfileResponse struct {
	ID         uint    `json:"id"`
	Name       string  `json:"name"`
	BirthYear  int     `json:"birth_year"`
	BirthMonth int     `json:"birth_month"`
	BirthDay   int     `json:"birth_day"`
	Gender     string  `json:"gender"`
	HeightCm   int     `json:"height_cm"`
	WeightKg   float64 `json:"weight_kg"`
	ArchivedAt *string `json:"archived_at,omitempty"`
}

type MergeChunksRequest struct {
	SessionID   string   `json:"session_id"`
	WorkoutType string   `json:"workout_type"`
	Movements   []string `json:"movements"`
	Injuries    []string `json:"injuries"`
	ProfileID   uint     `json:"profile_id"`
}

type MergeChunksResponse struct {
	Message   string `json:"message"`
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
}

type ChunkCompleteRequest struct {
	SessionID   string   `json:"session_id"`
	GCSURI      string   `json:"gcs_uri"`
	Movements   []string `json:"movements"`
	Injuries    []string `json:"injuries"`
	WorkoutType string   `json:"workout_type"`
	ProfileID   uint     `json:"profile_id"`
	StartSecs   float64  `json:"start_secs"`
	EndSecs     float64  `json:"end_secs"`
}

type GenerateHighlightRequest struct {
	SessionID   string `json:"session_id"`
	ProfileID   uint   `json:"profile_id"`
	MaxDuration int    `json:"max_duration,omitempty"` // seconds, default 60
}

type VerifyHighlightsRequest struct {
	SessionID string `json:"session_id"`
}

type ChunkAnalysisSummaryResponse struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}

type FullAnalysisStatusResponse struct {
	AnalysisType string `json:"analysis_type"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type SessionAssetResponse struct {
	Kind       string `json:"kind"`
	Variant    string `json:"variant"`
	Label      string `json:"label"`
	GCSURI     string `json:"gcs_uri"`
	PublicURL  string `json:"public_url,omitempty"`
	ObjectName string `json:"object_name"`
	CacheKey   string `json:"cache_key"`
	CreatedAt  string `json:"created_at"`
}

type SessionAssetsResponse struct {
	SessionID         string                       `json:"session_id"`
	SubtitleAvailable bool                         `json:"subtitle_available"`
	ChunkSummary      ChunkAnalysisSummaryResponse `json:"chunk_summary"`
	FullAnalysis      *FullAnalysisStatusResponse  `json:"full_analysis,omitempty"`
	Assets            []SessionAssetResponse       `json:"assets"`
}

type PlayURLResponse struct {
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"`
	Variant   string `json:"variant"`
	GCSURI    string `json:"gcs_uri"`
	PublicURL string `json:"public_url,omitempty"`
	SignedURL string `json:"signed_url"`
	ExpiresAt string `json:"expires_at"`
	CacheKey  string `json:"cache_key"`
}

type SessionCatalogItemResponse struct {
	SessionID       string `json:"session_id"`
	LatestCreatedAt string `json:"latest_created_at"`
	ChunkCount      int    `json:"chunk_count"`
	HasMerged       bool   `json:"has_merged"`
	HasHardsubbed   bool   `json:"has_hardsubbed"`
	HighlightCount  int    `json:"highlight_count"`
}

type SessionCatalogResponse struct {
	Sessions []SessionCatalogItemResponse `json:"sessions"`
}
