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
	ID         uint      `gorm:"primaryKey" json:"id"`
	BirthYear  int       `json:"birth_year"`
	BirthMonth int       `json:"birth_month"`
	BirthDay   int       `json:"birth_day"`
	Gender     string    `json:"gender"` // male, female, other
	HeightCm   int       `json:"height_cm"`
	WeightKg   float64   `json:"weight_kg"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AnalysisResult struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SessionID string    `gorm:"index;not null" json:"session_id"`
	ProfileID *uint     `gorm:"index" json:"profile_id,omitempty"`
	Status    string    `json:"status"` // PENDING, COMPLETED, FAILED
	Output    string    `json:"output"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChunkAnalysisResult struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SessionID string    `gorm:"index;not null" json:"session_id"`
	ProfileID *uint     `gorm:"index" json:"profile_id,omitempty"`
	Status    string    `json:"status"` // PENDING, COMPLETED, FAILED
	Output    string    `json:"output"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
