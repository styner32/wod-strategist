// live-replay prepares a retained workout fixture and replays it through live HTTP APIs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/logger"
)

func main() {
	if err := command(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, redact(err.Error()))
		os.Exit(1)
	}
}

func command(args []string) error {
	// Explicit process environment wins. No need for DATABASE_URL/GCS in run mode.
	_ = godotenv.Load(".env.live.local", "../.env", ".env")
	if len(args) == 0 {
		return fmt.Errorf("usage: live-replay prepare|run [flags]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	switch args[0] {
	case "prepare":
		opts := PrepareOptions{DatabaseURL: os.Getenv("DATABASE_URL"), Bucket: os.Getenv("GCS_BUCKET_NAME")}
		flags.StringVar(&opts.OutputDir, "fixture", defaultFixtureDir, "new fixture directory")
		flags.StringVar(&opts.FromFixtureDir, "from-fixture", "", "reuse an existing fixture's pinned source and sensors; no database needed")
		flags.StringVar(&opts.SourceSessionID, "source-session", defaultSourceSession, "source session")
		flags.UintVar(&opts.SourceProfileID, "source-profile", 1, "source profile")
		flags.Float64Var(&opts.MediaStartSecs, "start", 290, "source merged-video start in seconds")
		flags.Float64Var(&opts.MediaEndSecs, "end", 590, "source merged-video end in seconds")
		flags.Float64Var(&opts.ChunkSeconds, "chunk-seconds", 10, "media seconds per chunk")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected prepare arguments")
		}
		if err := logger.Init("development"); err != nil {
			return err
		}
		if err := prepareFixture(ctx, opts); err != nil {
			return err
		}
		fmt.Printf("Fixture ready: %s\nRun: make -C api test-live\n", opts.OutputDir)
		return nil
	case "run":
		opts := RunOptions{APIBase: os.Getenv("LIVE_TEST_API_URL"), Token: os.Getenv("LIVE_TEST_JWT"), Username: os.Getenv("LIVE_TEST_USERNAME"), Password: os.Getenv("LIVE_TEST_PASSWORD"), WebURL: os.Getenv("LIVE_TEST_WEB_URL")}
		if opts.APIBase == "" {
			opts.APIBase = os.Getenv("EXPO_PUBLIC_API_URL")
		}
		flags.StringVar(&opts.FixtureDir, "fixture", defaultFixtureDir, "prepared fixture directory")
		flags.StringVar(&opts.OutputDir, "output-dir", "tmp/live-replay/runs", "parent directory for new run reports")
		flags.DurationVar(&opts.PollInterval, "poll-interval", 5*time.Second, "result polling interval")
		flags.DurationVar(&opts.SensorTimeout, "sensor-timeout", 5*time.Minute, "sensor processing deadline")
		flags.DurationVar(&opts.AnalysisTimeout, "analysis-timeout", 30*time.Minute, "deadline after merge submission")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected run arguments")
		}
		return runReplay(ctx, opts, &http.Client{Timeout: 2 * time.Minute}, os.Stdout)
	default:
		return fmt.Errorf("unknown command %q; use prepare or run", args[0])
	}
}

func redact(message string) string {
	for _, key := range []string{"LIVE_TEST_JWT", "LIVE_TEST_PASSWORD", "DATABASE_URL", "GEMINI_API_KEY", "JWT_SIGNING_SECRET"} {
		if value := os.Getenv(key); value != "" {
			message = strings.ReplaceAll(message, value, "[REDACTED]")
		}
	}
	return message
}
