package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/timeline"
)

func main() {
	_ = godotenv.Load()

	dryRun := flag.Bool("dry-run", false, "Simulate backfill without applying database updates")
	batchSize := flag.Int("batch-size", 500, "Number of records to process per batch")
	profileID := flag.Uint("profile-id", 0, "Optional profile ID filter")
	flag.Parse()

	if err := logger.Init("development"); err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	dbConn, err := db.Connect(dbURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	fmt.Printf("Starting workout_at backfill (dry-run=%v, batch-size=%d, profile-id=%d)...\n", *dryRun, *batchSize, *profileID)

	sourceCounts := make(map[string]int)
	var lastID uint = 0
	totalProcessed := 0
	totalUpdated := 0

	for {
		var rows []db.AnalysisResult
		query := dbConn.Select("id", "session_id", "profile_id", "created_at").
			Where("workout_at IS NULL AND id > ?", lastID)
		if *profileID > 0 {
			query = query.Where("profile_id = ?", *profileID)
		}

		if err := query.Order("id ASC").Limit(*batchSize).Find(&rows).Error; err != nil {
			log.Fatalf("error fetching batch after id %d: %v", lastID, err)
		}

		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			lastID = row.ID
			totalProcessed++

			var session db.Session
			var sessionCreatedAt *time.Time
			sessErr := dbConn.Select("created_at").Where("session_id = ?", row.SessionID).First(&session).Error
			if sessErr == nil {
				sessionCreatedAt = &session.CreatedAt
			}

			workoutAt, source, _ := timeline.ResolveWorkoutAt(row.SessionID, sessionCreatedAt, &row.CreatedAt)
			if source == "" {
				source = "unresolved"
			}
			sourceCounts[source]++

			if !*dryRun && !workoutAt.IsZero() {
				res := dbConn.Model(&db.AnalysisResult{}).
					Where("id = ? AND workout_at IS NULL", row.ID).
					Updates(map[string]any{
						"workout_at":        workoutAt,
						"workout_at_source": source,
					})
				if res.Error != nil {
					log.Printf("failed to update record %d (%s): %v", row.ID, row.SessionID, res.Error)
				} else if res.RowsAffected > 0 {
					totalUpdated++
				}
			}
		}

		fmt.Printf("Processed batch up to id %d (%d records so far)...\n", lastID, totalProcessed)
	}

	fmt.Println("\n--- Backfill Summary ---")
	fmt.Printf("Total processed: %d\n", totalProcessed)
	if !*dryRun {
		fmt.Printf("Total updated:   %d\n", totalUpdated)
	} else {
		fmt.Println("Mode: DRY RUN (no rows updated)")
	}
	fmt.Println("Source breakdown:")
	for src, count := range sourceCounts {
		fmt.Printf("  - %-26s: %d\n", src, count)
	}
}
