package db

import (
	"log"
	"os"
	"time"

	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type AnalysisResult struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SessionID string    `gorm:"index;not null" json:"session_id"`
	Status    string    `json:"status"` // PENDING, COMPLETED, FAILED
	Output    string    `json:"output"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func Connect() *gorm.DB {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	var err error
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		logger.Log.Fatal("Failed to connect to database", zap.Error(err))
	}

	logger.Log.Info("Database connection established")
	return db
}
