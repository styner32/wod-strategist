package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wod-strategist/api/internal/logger"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type User struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"not null" json:"username"`
	PasswordHash string     `gorm:"not null" json:"-"`
	TokenVersion int        `gorm:"not null;default:1" json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

type Profile struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	UserID       uint       `gorm:"index;not null" json:"user_id"`
	Name         string     `gorm:"not null" json:"name"`
	BirthYear    *int       `json:"birth_year,omitempty"`
	BirthMonth   *int       `json:"birth_month,omitempty"`
	BirthDay     *int       `json:"birth_day,omitempty"`
	Gender       *string    `json:"gender,omitempty"` // male, female, other
	HeightCm     *int       `json:"height_cm,omitempty"`
	WeightKg     *float64   `json:"weight_kg,omitempty"`
	FitnessLevel string     `gorm:"type:text;not null;default:'intermediate'" json:"fitness_level"` // beginner, intermediate, advanced
	Injuries     *string    `gorm:"type:text;not null;default:'[]'" json:"injuries"`                // JSON array of injury strings
	ArchivedAt   *time.Time `json:"archived_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

const (
	AnalysisTypeWOD              = "wod"
	AnalysisTypeInjurySupplement = "injury_supplement"
)

type AnalysisResult struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	SessionID           string     `gorm:"uniqueIndex;not null" json:"session_id"`
	ProfileID           uint       `gorm:"index;not null" json:"profile_id"`
	AnalysisType        string     `gorm:"default:wod" json:"analysis_type"` // wod, injury_supplement
	Status              string     `json:"status"`                           // PENDING, COMPLETED, FAILED
	Output              string     `json:"output"`
	InjuryOutput        string     `json:"injury_output,omitempty"` // Injury supplement analysis (appended, not overwritten)
	InjuryOutputAlt     string     `json:"-"`
	GeminiFileURI       string     `json:"-"`
	GeminiFileName      string     `json:"-"`
	GeminiMIMEType      string     `json:"-"`
	GeminiFileExpiresAt *time.Time `json:"-"`
	HighlightSegments   string     `json:"highlight_segments"` // JSON array of highlight segments
	Verified            *bool      `json:"verified,omitempty"` // nil=unchecked, true=confirmed, false=hallucination detected
	// WODDescription is the user-supplied workout descriptor (e.g. "Fran", "For Time: 5 rounds of...").
	// Injected into analysis prompts to enable benchmark comparison and WOD-type-aware scoring.
	WODDescription string `gorm:"type:text;not null;default:''" json:"wod_description,omitempty"`
	// SessionScore is a compact JSON blob of per-dimension scores (0–100) produced at analysis time.
	// Schema: {"overall":74,"form":68,"intensity":82,"consistency":72,"movements":{},"summary":"..."}
	// Used to inject historical performance context into future analysis prompts.
	SessionScore string     `gorm:"type:text;not null;default:'{}'" json:"session_score,omitempty"`
	ArchivedAt   *time.Time `json:"archived_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type HighlightResult struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	SessionID   string    `gorm:"index;not null" json:"session_id"`
	ProfileID   uint      `gorm:"index;not null" json:"profile_id"`
	Title       string    `json:"title"`         // e.g. "Full Reel", "Best Forms"
	Status      string    `json:"status"`        // PENDING, PROCESSING, COMPLETED, FAILED
	GCSURI      string    `json:"gcs_uri"`       // gs:// URI of the polished highlight video
	MusicGCSURI string    `json:"music_gcs_uri"` // gs:// URI of the Lyria-generated music track
	Segments    string    `json:"segments"`      // JSON: selected segments used
	DurationSec float64   `json:"duration_sec"`  // total highlight duration in seconds
	Output      string    `json:"output"`        // error message or AI summary
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ChunkAnalysisResult struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	SessionID         string    `gorm:"index;not null" json:"session_id"`
	ProfileID         uint      `gorm:"index;not null" json:"profile_id"`
	FilePath          string    `json:"file_path"`
	ExerciseType      string    `json:"exercise_type,omitempty"` // detected movement (e.g. "Snatch", "Pull-up")
	Status            string    `json:"status"`                  // PENDING, COMPLETED, FAILED
	Output            string    `json:"output"`
	ObservedSignals   string    `gorm:"type:text;not null;default:'{}'" json:"observed_signals"` // JSON: estimated workout metrics for benchmarking
	HeartRateBPM      int       `gorm:"not null;default:0" json:"heart_rate_bpm"`                // BLE heart rate associated with the chunk; exact sample time is unavailable (0 = unavailable)
	StartSecs         *float64  `json:"start_secs,omitempty"`
	EndSecs           *float64  `json:"end_secs,omitempty"`
	MediaStartSecs    *float64  `json:"media_start_secs,omitempty"`
	MediaEndSecs      *float64  `json:"media_end_secs,omitempty"`
	WorkoutConfidence float64   `gorm:"not null;default:0.0" json:"workout_confidence"` // confidence if person actually workout
	MotionScore       *float64  `json:"motion_score,omitempty"`
	SkipReason        string    `json:"skip_reason,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type TokenUsage struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	SessionID       string    `gorm:"index;not null" json:"session_id"`
	ProfileID       uint      `gorm:"index;not null" json:"profile_id"`
	TaskType        string    `gorm:"not null" json:"task_type"` // chunk:analysis, video:index, video:segment, etc.
	Model           string    `gorm:"not null" json:"model"`     // gemini-3.1-pro-preview, gemini-3-flash-preview
	PromptTokens    int32     `gorm:"not null;default:0" json:"prompt_tokens"`
	CandidateTokens int32     `gorm:"not null;default:0" json:"candidate_tokens"`
	TotalTokens     int32     `gorm:"not null;default:0" json:"total_tokens"`
	CreatedAt       time.Time `json:"created_at"`
}

const (
	SessionStatusStarted   = "started"
	SessionStatusCompleted = "completed"
	SessionStatusFailed    = "failed"
)

type SessionStatus string

func (s SessionStatus) String() string {
	return string(s)
}

type Session struct {
	ID             uint          `gorm:"primaryKey" json:"id"`
	SessionID      string        `gorm:"uniqueIndex;not null" json:"session_id"`
	Status         SessionStatus `json:"status"` // started, completed, failed
	ProfileID      uint          `gorm:"index;not null" json:"profile_id"`
	IdempotencyKey string        `gorm:"index;not null" json:"idempotency_key"`
	WODDescription string        `gorm:"not null" json:"wod_description"`
	MovementHints  JSONDocument  `json:"movement_hints"`
	WorkoutType    string        `json:"workout_type"`
	UpdatedAt      time.Time     `json:"updated_at"`
	CreatedAt      time.Time     `json:"created_at"`
}

func Connect(databaseURL string) (*gorm.DB, error) {
	dsn, err := normalizeDatabaseURL(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid database configuration: %w", err)
	}
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	logger.Log.Info("Database connection established")
	return db, nil
}

func normalizeDatabaseURL(raw string) (string, error) {
	dsn := strings.TrimSpace(raw)
	if dsn == "" {
		return "", nil
	}

	if len(dsn) >= 2 {
		if dsn[0] == '"' && dsn[len(dsn)-1] == '"' {
			dsn = dsn[1 : len(dsn)-1]
		} else if dsn[0] == '\'' && dsn[len(dsn)-1] == '\'' {
			dsn = dsn[1 : len(dsn)-1]
		}
	}

	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "", fmt.Errorf("DATABASE_URL is empty after trimming whitespace and quotes")
	}

	if _, err := pgx.ParseConfig(dsn); err != nil {
		return "", fmt.Errorf("DATABASE_URL is invalid: %w", err)
	}

	return dsn, nil
}

type PipelineStageMetric struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	SessionID    string    `gorm:"index;not null" json:"session_id"`
	ProfileID    uint      `gorm:"not null;default:0" json:"profile_id"`
	Stage        string    `gorm:"not null" json:"stage"`   // chunk_analysis | injury_analysis | verify_highlights
	Variant      string    `gorm:"not null" json:"variant"` // legacy | optimized
	APICalls     int       `gorm:"not null;default:0" json:"api_calls"`
	SkippedCalls int       `gorm:"not null;default:0" json:"skipped_calls"`
	UploadBytes  int64     `gorm:"not null;default:0" json:"upload_bytes"`
	DurationMs   int64     `gorm:"not null;default:0" json:"duration_ms"`
	CreatedAt    time.Time `json:"created_at"`
}
