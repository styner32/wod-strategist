// compare-video performs a local, read-only-DB Static/Agentic experiment.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/worker"
)

func main() {
	if err := run(); err != nil {
		message := err.Error()
		if key := os.Getenv("GEMINI_API_KEY"); key != "" {
			message = strings.ReplaceAll(message, key, "[REDACTED]")
		}
		fmt.Fprintln(os.Stderr, message)
		os.Exit(1)
	}
}

func run() error {
	var opts worker.VideoComparisonOptions
	flag.StringVar(&opts.SessionID, "session-id", "", "completed WOD session ID")
	flag.BoolVar(&opts.LatestCompletedWOD, "latest-completed-wod", false, "select latest completed WOD with retained source")
	flag.StringVar(&opts.OutputDir, "output-dir", "", "new directory for raw responses and report")
	flag.Parse()
	if flag.NArg() > 0 || (opts.SessionID == "") == !opts.LatestCompletedWOD || opts.OutputDir == "" {
		return fmt.Errorf("use exactly one of --session-id or --latest-completed-wod, plus --output-dir")
	}
	_ = godotenv.Load()
	for _, name := range []string{"DATABASE_URL", "GCS_BUCKET_NAME", "GEMINI_API_KEY"} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	var err error
	opts.OutputDir, err = filepath.Abs(opts.OutputDir)
	if err != nil {
		return err
	}
	opts.APIKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := logger.Init("development"); err != nil {
		return err
	}
	conn, err := db.Connect(os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	sqlDB, err := conn.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	sc, err := storage.NewClient(ctx, os.Getenv("GCS_BUCKET_NAME"))
	if err != nil {
		return err
	}
	gc, err := gemini.NewComparisonClient(ctx, gemini.Options{APIKey: opts.APIKey, ThinkingLevel: "HIGH"})
	if err != nil {
		return err
	}
	return worker.RunVideoComparison(ctx, conn, sc, os.Getenv("GCS_BUCKET_NAME"), gc, opts)
}
