package db

import (
	"log"
	"os"
	"time"

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

var DB *gorm.DB

func Connect() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	log.Println("Database connection established")
}

func Migrate() {
	err := DB.AutoMigrate(&AnalysisResult{})
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}
	log.Println("Database migration completed")
}
