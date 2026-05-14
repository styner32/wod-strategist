package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/auth"
	"github.com/wod-strategist/api/internal/db"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	username := flag.String("username", "", "Username (3-20 lowercase alphanumeric or underscore)")
	password := flag.String("password", "", "Password (minimum 8 characters)")
	fitnessLevel := flag.String("fitness-level", "intermediate", "Default profile fitness level (beginner, intermediate, advanced)")
	flag.Parse()

	if *username == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "Usage: create-user --username=<name> --password=<pass>")
		os.Exit(1)
	}

	// Load .env if present (same as the server)
	_ = godotenv.Load()

	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required (set in .env or environment)")
		os.Exit(1)
	}

	database, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to database: %v\n", err)
		os.Exit(1)
	}

	// Validate username format
	if len(*username) < 3 || len(*username) > 20 {
		fmt.Fprintln(os.Stderr, "username must be 3-20 characters")
		os.Exit(1)
	}
	if len(*password) < 8 {
		fmt.Fprintln(os.Stderr, "password must be at least 8 characters")
		os.Exit(1)
	}

	hash, err := auth.HashPassword(*password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to hash password: %v\n", err)
		os.Exit(1)
	}

	user := db.User{
		Username:     *username,
		PasswordHash: hash,
		TokenVersion: 1,
	}

	if err := database.Create(&user).Error; err != nil {
		fmt.Fprintf(os.Stderr, "failed to create user: %v\n", err)
		os.Exit(1)
	}

	// Auto-create a default profile (same as auth.Service.Signup)
	profile := db.Profile{
		UserID:       user.ID,
		Name:         *username,
		FitnessLevel: *fitnessLevel,
	}
	if err := database.Create(&profile).Error; err != nil {
		fmt.Fprintf(os.Stderr, "user created (ID=%d) but failed to create default profile: %v\n", user.ID, err)
		os.Exit(1)
	}

	fmt.Printf("Created user %q (ID=%d) with default profile (ID=%d, fitness_level=%s)\n",
		*username, user.ID, profile.ID, *fitnessLevel)
}
