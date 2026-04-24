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

type Profile struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	Name         string     `json:"name"`
	BirthYear    int        `json:"birth_year"`
	BirthMonth   int        `json:"birth_month"`
	BirthDay     int        `json:"birth_day"`
	Gender       string     `json:"gender"` // male, female, other
	HeightCm     int        `json:"height_cm"`
	WeightKg     float64    `json:"weight_kg"`
	FitnessLevel string     `gorm:"type:text;not null;default:'intermediate'" json:"fitness_level"` // beginner, intermediate, advanced
	Injuries     string     `gorm:"type:text;not null;default:'[]'" json:"injuries"`               // JSON array of injury strings
	ArchivedAt   *time.Time `json:"archived_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

const (
	AnalysisTypeWOD              = "wod"
	AnalysisTypeInjurySupplement = "injury_supplement"
)

type AnalysisResult struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	SessionID         string    `gorm:"index;not null" json:"session_id"`
	ProfileID         *uint     `gorm:"index" json:"profile_id,omitempty"`
	AnalysisType      string    `gorm:"default:wod" json:"analysis_type"` // wod, injury_supplement
	Status            string    `json:"status"`                           // PENDING, COMPLETED, FAILED
	Output            string    `json:"output"`
	InjuryOutput      string    `json:"injury_output,omitempty"` // Injury supplement analysis (appended, not overwritten)
	HighlightSegments string    `json:"highlight_segments"`      // JSON array of highlight segments
	Verified          *bool      `json:"verified,omitempty"`      // nil=unchecked, true=confirmed, false=hallucination detected
	ArchivedAt        *time.Time `json:"archived_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type HighlightResult struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	SessionID   string    `gorm:"index;not null" json:"session_id"`
	ProfileID   *uint     `gorm:"index" json:"profile_id,omitempty"`
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
	ID              uint      `gorm:"primaryKey" json:"id"`
	SessionID       string    `gorm:"index;not null" json:"session_id"`
	ProfileID       *uint     `gorm:"index" json:"profile_id,omitempty"`
	FilePath        string    `json:"file_path"`
	ExerciseType    string    `json:"exercise_type,omitempty"`                           // detected movement (e.g. "Snatch", "Pull-up")
	Status          string    `json:"status"`                                            // PENDING, COMPLETED, FAILED
	Output          string    `json:"output"`
	ObservedSignals string    `gorm:"type:text;not null;default:'{}'" json:"observed_signals"` // JSON: estimated workout metrics for benchmarking
	HeartRateBPM    int       `gorm:"not null;default:0" json:"heart_rate_bpm"`                    // BLE heart rate at chunk capture time (0 = unavailable)
	StartSecs       *float64  `json:"start_secs,omitempty"`
	EndSecs         *float64  `json:"end_secs,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
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
