package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
)

func main() {
	if err := godotenv.Load(); err != nil {
		// Log but don't fail
	}

	// Initialize Logger
	logger.Init()
	defer logger.Sync()

	// Initialize Database
	db.Connect()
	// db.Migrate() // Migration can be done in API server or a separate job, but usually safe to run here too if idempotent

	// Initialize Redis Connection for Asynq
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisOpt := asynq.RedisClientOpt{Addr: redisAddr}

	// Start Asynq Server (Worker)
	srv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: 10,
			// Add logger adapter if needed, or Asynq will use its own logger.
			// Asynq supports custom logger via Logger interface.
			// For now, let's keep it simple. Asynq logs to stderr by default.
		},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TypeVideoAnalysis, worker.HandleVideoAnalysisTask)

	// Run blocks and handles signals
	logger.Log.Info("Starting worker server")

	// Create a channel to listen for signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		if err := srv.Run(mux); err != nil {
			logger.Log.Fatal("could not run asynq server", zap.Error(err))
		}
	}()

	// Wait for signal
	<-quit
	logger.Log.Info("Shutting down worker...")

	// Shutdown the server
	srv.Shutdown()

	logger.Log.Info("Worker exiting")
}
