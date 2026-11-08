// cmd/admin is a local CLI tool for one-off maintenance and read-only audit tasks.
//
// Usage:
//
//	go run ./cmd/admin <command> [flags]
//
// Commands:
//
//	reparse-highlights            Re-parse highlight_segments from analysis output text
//	backfill-activity-summaries   Recompute stored sessions.activity_summary in small sequential batches
//	storage-audit                 Read-only GCS storage & DB reference measurement (never deletes objects)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
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
	case "backfill-activity-summaries":
		cmdBackfillActivitySummaries(os.Args[2:])
	case "storage-audit":
		cmdStorageAudit(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "  reparse-highlights            Re-parse highlight_segments from analysis output text")
	fmt.Fprintln(os.Stderr, "  backfill-activity-summaries   Recompute stored sessions.activity_summary in small sequential batches")
	fmt.Fprintln(os.Stderr, "  storage-audit                 Read-only GCS object size & DB reference report (0 deletions)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags for backfill-activity-summaries:")
	fmt.Fprintln(os.Stderr, "  --batch-size=N       Sessions per batch (default: 25, max: 100)")
	fmt.Fprintln(os.Stderr, "  --profile-id=ID      Target a specific profile")
	fmt.Fprintln(os.Stderr, "  --session-id=ID      Target a specific session")
	fmt.Fprintln(os.Stderr, "  --dry-run            Preview changes without writing (default)")
	fmt.Fprintln(os.Stderr, "  --apply              Write sessions.activity_summary and cache version")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags for storage-audit:")
	fmt.Fprintln(os.Stderr, "  --bucket=NAME        GCS bucket name (defaults to GCS_BUCKET_NAME)")
	fmt.Fprintln(os.Stderr, "  --prefix=PREFIX      Object prefix to scan (default: videos/)")
}

func cmdBackfillActivitySummaries(args []string) {
	opts := activity.BackfillOptions{
		BatchSize: activity.DefaultBackfillBatchSize,
	}
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--batch-size="):
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--batch-size="))
			if err != nil || n <= 0 {
				log.Fatalf("invalid --batch-size: %s", arg)
			}
			opts.BatchSize = n
		case strings.HasPrefix(arg, "--profile-id="):
			pid, err := strconv.ParseUint(strings.TrimPrefix(arg, "--profile-id="), 10, 32)
			if err != nil || pid == 0 {
				log.Fatalf("invalid --profile-id: %s", arg)
			}
			opts.ProfileID = uint(pid)
		case strings.HasPrefix(arg, "--session-id="):
			opts.SessionID = strings.TrimPrefix(arg, "--session-id=")
		case arg == "--apply":
			opts.Apply = true
		case arg == "--dry-run":
			opts.Apply = false
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
	conn, err := db.ConnectWithPool(dbURL, db.PoolConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	stats, err := activity.BackfillSummaries(context.Background(), conn, opts, logger.Log)
	if err != nil {
		log.Fatalf("backfill-activity-summaries failed: %v", err)
	}
	logger.Log.Info("backfill-activity-summaries completed",
		zap.Bool("apply", opts.Apply),
		zap.Int("scanned_sessions", stats.ScannedSessions),
		zap.Int("already_valid", stats.AlreadyValid),
		zap.Int("updated", stats.Updated),
		zap.Int("would_update", stats.WouldUpdate),
		zap.Int("chunks_loaded", stats.ChunksLoaded),
	)
}

func cmdStorageAudit(args []string) {
	bucket := strings.TrimSpace(os.Getenv("GCS_BUCKET_NAME"))
	prefix := "videos/"
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--bucket="):
			bucket = strings.TrimSpace(strings.TrimPrefix(arg, "--bucket="))
		case strings.HasPrefix(arg, "--prefix="):
			prefix = strings.TrimSpace(strings.TrimPrefix(arg, "--prefix="))
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", arg)
			os.Exit(1)
		}
	}
	if bucket == "" {
		log.Fatal("GCS_BUCKET_NAME or --bucket is required.")
	}
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required. Set it in .env or environment.")
	}
	if err := logger.Init("development"); err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	conn, err := db.ConnectWithPool(dbURL, db.PoolConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	ctx := context.Background()
	gcsClient, err := storage.NewClient(ctx, bucket)
	if err != nil {
		log.Fatalf("failed to create GCS client: %v", err)
	}

	report, err := storage.AuditBucketStorage(ctx, gcsClient, conn, prefix)
	if err != nil {
		log.Fatalf("storage-audit failed: %v", err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatalf("marshal storage-audit report: %v", err)
	}
	fmt.Println(string(encoded))
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

	var updated, wouldUpdate, skipped, noChange int

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
			wouldUpdate++
		}
	}

	logger.Log.Info("Reparse complete",
		zap.String("mode", mode),
		zap.Int("total", len(results)),
		zap.Int("updated", updated),
		zap.Int("would_update", wouldUpdate),
		zap.Int("no_change", noChange),
		zap.Int("skipped_no_highlights", skipped))
}
