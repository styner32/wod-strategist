package db

import "time"

const RecordingMediaVersion = 1

// RecordingMediaManifestEntry identifies immutable, server-admitted source bytes.
// HTTP receipts encode these integers as strings to preserve JavaScript precision.
type RecordingMediaManifestEntry struct {
	ChunkID          uint  `json:"chunk_id"`
	SourceGeneration int64 `json:"source_generation"`
}

type RecordingMediaRun struct {
	ID                      uint `gorm:"primaryKey"`
	ProfileID               uint
	SessionID               string
	Version                 int
	State                   string
	Manifest                JSONDocument
	ManifestHash            string
	Payload                 JSONDocument
	OutputProfile           JSONDocument
	Intervals               JSONDocument
	MergedObject            string
	MergedGeneration        int64
	MergedBytes             int64
	MergedStagingObject     string
	MergedStagingGeneration int64
	FailedStage             string
	Attempts                int
	AnalysisAttempts        int
	DispatchVersion         int64
	NextAttemptAt           *time.Time
	LeaseToken              string
	LeaseExpiresAt          *time.Time
	SafeError               string
	ActivityDispatched      bool
	AnalysisDispatched      bool
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type RecordingChunk struct {
	ID               uint `gorm:"primaryKey"`
	RunID            uint
	ProfileID        uint
	SessionID        string
	ClientChunkID    string
	CaptureOrder     int
	SourceURI        string
	SourceObject     string
	SourceGeneration int64
	SourceBytes      int64
	CaptureStartSecs float64
	CaptureEndSecs   float64
	SourceProfile    JSONDocument
	SourceTimeline   JSONDocument
	LeadingTrimSecs  float64
	ProbeReady       bool
	AIType           string       `gorm:"column:ai_type"`
	AIPayload        JSONDocument `gorm:"column:ai_payload"`
	AIDispatched     bool         `gorm:"column:ai_dispatched"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ChunkMediaPreparation struct {
	ID               uint `gorm:"primaryKey"`
	RunID            uint
	ChunkID          uint
	SourceGeneration int64
	RecipeVersion    string
	ProfileKey       string
	ResolvedProfile  JSONDocument
	Timeline         JSONDocument
	Signature        JSONDocument
	State            string
	OutputObject     string
	OutputGeneration int64
	OutputBytes      int64
	Attempts         int
	DispatchVersion  int64
	NextAttemptAt    *time.Time
	LeaseToken       string
	LeaseExpiresAt   *time.Time
	SafeError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type AnalysisVideoPreparation struct {
	ID               uint `gorm:"primaryKey"`
	RunID            uint
	SourceObject     string
	SourceGeneration int64
	PresetVersion    string
	State            string
	OutputObject     string
	OutputGeneration int64
	OutputBytes      int64
	Attempts         int
	DispatchVersion  int64
	NextAttemptAt    *time.Time
	LeaseToken       string
	LeaseExpiresAt   *time.Time
	SafeError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
