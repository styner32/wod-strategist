package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/config"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.InitWorker()
	if err != nil {
		log.Fatalf("failed to initialize config: %v", err)
	}

	// Initialize Logger
	if err := logger.Init(cfg.AppEnv); err != nil {
		log.Fatalf("failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	// Initialize Database
	dbConn, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		logger.Log.Fatal("Failed to connect to database", zap.Error(err))
	}
	// db.Migrate() // Migration can be done in API server or a separate job, but usually safe to run here too if idempotent

	// Initialize Redis Connection for Asynq
	redisAddr := cfg.RedisURL
	redisOpt := asynq.RedisClientOpt{Addr: redisAddr, DB: 5} // Use DB 5 for Asynq tasks

	logger.Log.Info("Redis connection established", zap.String("redis_addr", redisAddr))

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

	storageClient, err := storage.NewClient(context.Background(), cfg.GCSBucketName)
	if err != nil {
		logger.Log.Fatal("Failed to create storage client", zap.Error(err))
	}

	geminiClient, err := gemini.NewClientWithOptions(context.Background(), logger.Log, gemini.Options{
		APIKey: cfg.GeminiAPIKey,
	})
	if err != nil {
		logger.Log.Fatal("Failed to create gemini client", zap.Error(err))
	}

	// Create Asynq client for enqueueing tasks from workers (e.g. merge → analysis)
	queueClient := asynq.NewClient(redisOpt)

	w := worker.NewWorker(dbConn, storageClient, cfg.GCSBucketName, geminiClient, queueClient)

	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TypeVideoAnalysis, w.HandleVideoAnalysisTask)
	mux.HandleFunc(worker.TypeChunkAnalysis, w.HandleChunkAnalysisTask)
	mux.HandleFunc(worker.TypeMergeChunks, w.HandleMergeChunksTask)
	mux.HandleFunc(worker.TypeInjuryAnalysis, w.HandleInjuryAnalysisTask)
	mux.HandleFunc(worker.TypeGenerateHighlight, w.HandleGenerateHighlightTask)

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
