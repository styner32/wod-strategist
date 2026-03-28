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
	ProfileID   uint     `json:"profile_id,omitempty"`
}

type CompleteUploadResponse struct {
	Message   string `json:"message"`
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
}

type CreateProfileRequest struct {
	BirthYear  int     `json:"birth_year" binding:"required,min=1900"`
	BirthMonth int     `json:"birth_month" binding:"required,min=1,max=12"`
	BirthDay   int     `json:"birth_day" binding:"required,min=1,max=31"`
	Gender     string  `json:"gender" binding:"required,oneof=male female other"`
	HeightCm   int     `json:"height_cm" binding:"required,min=50,max=300"`
	WeightKg   float64 `json:"weight_kg" binding:"required,min=20,max=500"`
}

type ProfileResponse struct {
	ID         uint    `json:"id"`
	BirthYear  int     `json:"birth_year"`
	BirthMonth int     `json:"birth_month"`
	BirthDay   int     `json:"birth_day"`
	Gender     string  `json:"gender"`
	HeightCm   int     `json:"height_cm"`
	WeightKg   float64 `json:"weight_kg"`
}

type MergeChunksRequest struct {
	SessionID   string   `json:"session_id"`
	WorkoutType string   `json:"workout_type"`
	Movements   []string `json:"movements"`
	Injuries    []string `json:"injuries"`
	ProfileID   uint     `json:"profile_id,omitempty"`
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
	ProfileID   uint     `json:"profile_id,omitempty"`
	StartSecs   float64  `json:"start_secs"`
	EndSecs     float64  `json:"end_secs"`
}
