// live-replay prepares retained benchmark workout fixtures and replays them through live HTTP APIs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	return commandIO(args, os.Stdin, os.Stdout)
}

func commandIO(args []string, in io.Reader, out io.Writer) error {
	// Explicit process environment wins. No need for DATABASE_URL/GCS in run or list mode.
	_ = godotenv.Load(".env.live.local", "../.env", ".env")
	if len(args) == 0 {
		return fmt.Errorf("usage: live-replay prepare|run|list [flags]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	if out != nil {
		flags.SetOutput(out)
	}
	switch args[0] {
	case "list":
		var fixturesRoot string
		flags.StringVar(&fixturesRoot, "fixtures-dir", defaultFixturesRoot, "directory containing prepared benchmark fixtures")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected list arguments")
		}
		entries, err := discoverFixtures(fixturesRoot)
		if err != nil {
			return err
		}
		printFixtureCatalog(out, fixturesRoot, entries)
		return nil
	case "prepare":
		opts := PrepareOptions{DatabaseURL: os.Getenv("DATABASE_URL"), Bucket: os.Getenv("GCS_BUCKET_NAME")}
		var wodFlag, fixtureFlag, startRaw, endRaw string
		flags.StringVar(&wodFlag, "wod", "", "benchmark fixture name under tmp/live-replay/fixtures/<name> (auto-derived from WOD title if omitted)")
		flags.StringVar(&fixtureFlag, "fixture", "", "benchmark fixture name or explicit output directory")
		flags.StringVar(&opts.FixturesRoot, "fixtures-dir", defaultFixturesRoot, "parent directory for named benchmark fixtures")
		flags.StringVar(&opts.FromFixtureDir, "from-fixture", "", "reuse an existing fixture's pinned source and sensors (name or directory); no database needed")
		flags.StringVar(&opts.SourceSessionID, "source-session", "", "source session ID (e.g. WOD-YYYYMMDD-...)")
		flags.UintVar(&opts.SourceProfileID, "source-profile", 1, "source profile")
		flags.StringVar(&startRaw, "start", "0", "workout start in source merged video (MM:SS, HH:MM:SS, or seconds, e.g. 4:50)")
		flags.StringVar(&endRaw, "end", "auto", "workout end in source merged video (MM:SS, seconds, or auto for end of recording)")
		flags.Float64Var(&opts.ChunkSeconds, "chunk-seconds", defaultChunkSeconds, "media seconds per chunk")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected prepare arguments")
		}
		name, outputDir, err := resolvePrepareTarget(wodFlag, fixtureFlag)
		if err != nil {
			return err
		}
		opts.Name, opts.OutputDir = name, outputDir
		startSecs, err := parseMediaTimestamp(startRaw)
		if err != nil {
			return fmt.Errorf("invalid --start: %w", err)
		}
		endSecs, autoEnd, err := parseEndTimestamp(endRaw)
		if err != nil {
			return fmt.Errorf("invalid --end: %w", err)
		}
		opts.MediaStartSecs, opts.MediaEndSecs, opts.AutoEnd = startSecs, endSecs, autoEnd
		if err := logger.Init("development"); err != nil {
			return err
		}
		return prepareFixture(ctx, opts)
	case "run":
		opts := RunOptions{APIBase: os.Getenv("LIVE_TEST_API_URL"), Token: os.Getenv("LIVE_TEST_JWT"), Username: os.Getenv("LIVE_TEST_USERNAME"), Password: os.Getenv("LIVE_TEST_PASSWORD"), WebURL: os.Getenv("LIVE_TEST_WEB_URL")}
		if opts.APIBase == "" {
			opts.APIBase = os.Getenv("EXPO_PUBLIC_API_URL")
		}
		var wodName, fixturesRoot string
		flags.StringVar(&wodName, "wod", "", "prepared benchmark fixture name (e.g. diane, grace) or source session ID")
		flags.StringVar(&opts.FixtureDir, "fixture", "", "prepared benchmark fixture name or directory (prompts when both --wod and --fixture are empty)")
		flags.StringVar(&fixturesRoot, "fixtures-dir", defaultFixturesRoot, "directory containing prepared benchmark fixtures")
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
		selectedDir, err := resolveFixtureSelection(fixturesRoot, wodName, opts.FixtureDir, in, out)
		if err != nil {
			return err
		}
		opts.FixtureDir = selectedDir
		return runReplay(ctx, opts, &http.Client{Timeout: 2 * time.Minute}, out)
	default:
		return fmt.Errorf("unknown command %q; use prepare, run, or list", args[0])
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
