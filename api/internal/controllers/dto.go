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
}

type CompleteUploadResponse struct {
	Message   string `json:"message"`
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
}
