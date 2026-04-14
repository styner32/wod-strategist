// cmd/admin is a local CLI tool for one-off maintenance tasks against the
// production database. It only requires DATABASE_URL (reads from .env).
//
// Usage:
//
//	go run ./cmd/admin <command> [flags]
//
// Commands:
//
//	reparse-highlights   Re-parse highlight_segments from analysis output text
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
)

func main() {
	_ = godotenv.Load() // best-effort .env loading

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "reparse-highlights":
		cmdReparseHighlights(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: admin <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  reparse-highlights   Re-parse highlight_segments from analysis output text")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags for reparse-highlights:")
	fmt.Fprintln(os.Stderr, "  --session-id=ID      Target a specific session (default: all)")
	fmt.Fprintln(os.Stderr, "  --dry-run            Preview changes without writing (default)")
	fmt.Fprintln(os.Stderr, "  --apply              Actually write changes to the database")
}



func cmdReparseHighlights(args []string) {
	var sessionID string
	var apply bool

	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--session-id="):
			sessionID = strings.TrimPrefix(arg, "--session-id=")
		case arg == "--apply":
			apply = true
		case arg == "--dry-run":
			apply = false
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", arg)
			os.Exit(1)
		}
	}

	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required. Set it in .env or environment.")
	}

	if err := logger.Init("development"); err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}

	conn, err := db.Connect(dbURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	mode := "DRY RUN"
	if apply {
		mode = "APPLY"
	}
	logger.Log.Info("Reparse highlights", zap.String("mode", mode), zap.String("session_id", sessionID))

	conn = conn.Debug()

	// Diagnostic: show what's in the DB for this session (unfiltered)
	if sessionID != "" {
		var diag []db.AnalysisResult
		conn.Where("session_id = ?", sessionID).Find(&diag)
		for _, d := range diag {
			logger.Log.Info("DB row found",
				zap.Uint("id", d.ID),
				zap.String("session_id", d.SessionID),
				zap.String("analysis_type", d.AnalysisType),
				zap.String("status", d.Status),
				zap.Int("output_len", len(d.Output)),
				zap.Int("highlight_len", len(d.HighlightSegments)))
		}
		if len(diag) == 0 {
			logger.Log.Warn("No rows found for session_id at all — check DB connection and session ID")
		}
	}

	// Query completed WOD analysis results
	query := conn.Where("analysis_type = ? AND status = ?", db.AnalysisTypeWOD, "COMPLETED")
	if sessionID != "" {
		query = query.Where("session_id = ?", sessionID)
	}

	var results []db.AnalysisResult
	if err := query.Order("created_at DESC").Find(&results).Error; err != nil {
		log.Fatalf("failed to query analysis results: %v", err)
	}

	logger.Log.Info("Found analysis results", zap.Int("count", len(results)))

	var updated, skipped, noChange int

	for _, r := range results {
		oldHL := r.HighlightSegments
		newHL := worker.ParseHighlightSegments(r.Output)

		if newHL == "" {
			logger.Log.Warn("No highlights found in output",
				zap.Uint("id", r.ID),
				zap.String("session_id", r.SessionID))
			skipped++
			continue
		}

		if oldHL == newHL {
			logger.Log.Debug("No change",
				zap.Uint("id", r.ID),
				zap.String("session_id", r.SessionID))
			noChange++
			continue
		}

		// Count segments for logging
		oldCount := strings.Count(oldHL, "start")
		newCount := strings.Count(newHL, "start")

		logger.Log.Info("Highlight change detected",
			zap.Uint("id", r.ID),
			zap.String("session_id", r.SessionID),
			zap.Int("old_segment_count", oldCount),
			zap.Int("new_segment_count", newCount))

		if apply {
			if err := conn.Model(&db.AnalysisResult{}).
				Where("id = ?", r.ID).
				Update("highlight_segments", newHL).Error; err != nil {
				logger.Log.Error("Failed to update",
					zap.Uint("id", r.ID),
					zap.Error(err))
				continue
			}
			updated++
		} else {
			// In dry-run mode, just show what would change
			logger.Log.Info("Would update (dry-run)",
				zap.Uint("id", r.ID),
				zap.String("session_id", r.SessionID))
			updated++
		}
	}

	logger.Log.Info("Reparse complete",
		zap.String("mode", mode),
		zap.Int("total", len(results)),
		zap.Int("updated", updated),
		zap.Int("no_change", noChange),
		zap.Int("skipped_no_highlights", skipped))
}
