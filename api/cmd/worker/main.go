package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	dbConn, err := db.ConnectWithPool(cfg.DatabaseURL, db.PoolConfig{
		MaxOpenConns: cfg.DBMaxOpenConns,
		MaxIdleConns: cfg.DBMaxIdleConns,
	})
	if err != nil {
		logger.Log.Fatal("Failed to connect to database", zap.Error(err))
	}
	poolCtx, cancelPoolReporter := context.WithCancel(context.Background())
	defer cancelPoolReporter()
	if sqlDB, sqlErr := dbConn.DB(); sqlErr == nil {
		defer sqlDB.Close()
		db.StartPoolStatsReporter(poolCtx, sqlDB, logger.Log, "worker", db.PoolStatsReportInterval)
	}
	// db.Migrate() // Migration can be done in API server or a separate job, but usually safe to run here too if idempotent

	// Initialize Redis Connection for Asynq
	redisAddr := cfg.RedisURL
	redisOpt := asynq.RedisClientOpt{Addr: redisAddr, DB: 5} // Use DB 5 for Asynq tasks

	logger.Log.Info("Redis connection established", zap.String("redis_addr", redisAddr))

	// Start Asynq Server (Worker for video processing)
	srv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: cfg.Concurrency,
		},
	)

	// Start dedicated Asynq Server for sensor telemetry (Concurrency: 1)
	sensorSrv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: 1,
			Queues: map[string]int{
				worker.SensorQueueName: 1,
			},
		},
	)

	// Media CPU work has its own bounded queues; network-bound AI work retains
	// the existing concurrency without multiplying per-recording FFmpeg jobs.
	chunkMediaSrv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 1,
		Queues:      map[string]int{worker.RecordingChunkQueueName: 1},
	})
	sessionMediaSrv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 1,
		Queues:      map[string]int{worker.RecordingSessionQueueName: 1},
	})

	storageClient, err := storage.NewClient(context.Background(), cfg.GCSBucketName)
	if err != nil {
		logger.Log.Fatal("Failed to create storage client", zap.Error(err))
	}

	geminiClient, err := gemini.NewClientWithOptions(context.Background(), logger.Log, gemini.Options{
		APIKey:             cfg.GeminiAPIKey,
		Model:              cfg.GeminiModel,
		ThinkingLevel:      cfg.GeminiThinkingLevel,
		ThinkingLevelChunk: cfg.GeminiThinkingChunk,
		ChunkFPS:           cfg.GeminiChunkFPS,
		ThinkingBudget:     cfg.GeminiThinkingBudget,
		IncludeThoughts:    cfg.GeminiIncludeThoughts,
	})
	if err != nil {
		logger.Log.Fatal("Failed to create gemini client", zap.Error(err))
	}

	// Create Asynq client for enqueueing tasks from workers (e.g. merge → analysis)
	queueClient := asynq.NewClient(redisOpt)
	defer queueClient.Close()

	w := worker.NewWorker(dbConn, storageClient, cfg.GCSBucketName, geminiClient, queueClient, logger.Log)
	w.QueueInspector = asynq.NewInspector(redisOpt)
	defer w.QueueInspector.Close()
	w.CaptureFeedbackEnabled = cfg.CaptureFeedbackEnabled
	w.ContextualCoachingEnabled = cfg.ContextualCoachingEnabled
	w.ActivityCountingEnabled = cfg.ActivityCountingEnabled
	w.TrackingContinuityEnabled = cfg.TrackingContinuityEnabled
	w.ActivityReviewWindowRetryEnabled = worker.BoolPtr(cfg.ActivityReviewWindowRetryEnabled)
	w.Pass2RestFallbackSplitEnabled = worker.BoolPtr(cfg.Pass2RestFallbackSplitEnabled)
	w.MediaCPUSlotsEnabled = worker.BoolPtr(cfg.MediaCPUSlotsEnabled)
	w.AgenticHighlightsEnabled = cfg.AgenticHighlightsEnabled
	w.UseCache = cfg.UseCache
	w.PipelineMode = worker.PipelineMode(cfg.PipelineMode)

	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TypeAnalysisEnrichment, w.HandleAnalysisEnrichmentTask)
	mux.HandleFunc(worker.TypeVideoAnalysis, w.HandleVideoAnalysisTask)
	mux.HandleFunc(worker.TypeChunkAnalysis, w.HandleChunkAnalysisTask)
	mux.HandleFunc(worker.TypeChunkAnalysisWithSession, w.HandleChunkAnalysisWithSessionTask)
	mux.HandleFunc(worker.TypeMergeChunks, w.HandleMergeChunksTask)
	mux.HandleFunc(worker.TypeActivityReview, w.HandleActivityReviewTask)
	mux.HandleFunc(worker.TypeInjuryAnalysis, w.HandleInjuryAnalysisTask)
	mux.HandleFunc(worker.TypeGenerateHighlight, w.HandleGenerateHighlightTask)
	mux.HandleFunc(worker.TypeVerifyHighlights, w.HandleVerifyHighlightsTask)
	mux.HandleFunc(worker.TypeGenerateHardSub, w.HandleGenerateHardSubTask)
	mux.HandleFunc(worker.TypeChunkDebugReanalysis, w.HandleChunkDebugReanalysisTask)
	mux.HandleFunc(worker.TypeSessionDebugReanalysis, w.HandleSessionDebugReanalysisTask)
	mux.HandleFunc(worker.TypeRecognitionReview, w.HandleRecognitionReviewTask)
	mux.HandleFunc(worker.TypeCoordinateRecordingMedia, w.HandleCoordinateRecordingMediaTask)

	chunkMediaMux := asynq.NewServeMux()
	chunkMediaMux.HandleFunc(worker.TypePrepareRecordingChunk, w.HandlePrepareRecordingChunkTask)
	sessionMediaMux := asynq.NewServeMux()
	sessionMediaMux.HandleFunc(worker.TypeMergeRecordingMedia, w.HandleMergeRecordingMediaTask)
	sessionMediaMux.HandleFunc(worker.TypePrepareAnalysisVideo, w.HandlePrepareAnalysisVideoTask)

	sensorMux := asynq.NewServeMux()
	sensorMux.HandleFunc(worker.TypeSensorTelemetry, w.HandleSensorTelemetryTask)

	// Sensor recovery manager
	recoveryCtx, cancelRecovery := context.WithCancel(context.Background())
	defer cancelRecovery()
	recoveryMgr := worker.NewSensorRecoveryManager(dbConn, queueClient, logger.Log)
	go recoveryMgr.Start(recoveryCtx, 10*time.Second)
	go w.RunEnrichmentRecovery(recoveryCtx)
	go w.RunRecordingMediaRecovery(recoveryCtx)

	// Run blocks and handles signals
	logger.Log.Info("Starting worker server",
		zap.Int("concurrency", cfg.Concurrency),
		zap.Int("db_max_open_conns", cfg.DBMaxOpenConns),
	)

	// Create a channel to listen for signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start video worker in a goroutine
	go func() {
		if err := srv.Run(mux); err != nil {
			logger.Log.Fatal("could not run asynq video server", zap.Error(err))
		}
	}()

	// Start sensor worker in a goroutine
	go func() {
		if err := sensorSrv.Run(sensorMux); err != nil {
			logger.Log.Fatal("could not run asynq sensor server", zap.Error(err))
		}
	}()
	go func() {
		if err := chunkMediaSrv.Run(chunkMediaMux); err != nil {
			logger.Log.Fatal("could not run recording chunk media server", zap.Error(err))
		}
	}()
	go func() {
		if err := sessionMediaSrv.Run(sessionMediaMux); err != nil {
			logger.Log.Fatal("could not run recording session media server", zap.Error(err))
		}
	}()

	// Wait for signal
	<-quit
	logger.Log.Info("Shutting down worker...")

	cancelRecovery()
	chunkMediaSrv.Shutdown()
	sessionMediaSrv.Shutdown()
	sensorSrv.Shutdown()
	srv.Shutdown()

	logger.Log.Info("Worker exiting")
}
