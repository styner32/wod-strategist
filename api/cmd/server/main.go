// @title           WOD Strategist API
// @version         1.0
// @description     API for WOD Strategist application.

// @host      localhost:8088
// @BasePath  /api/v1
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	_ "github.com/wod-strategist/api/docs"
	"github.com/wod-strategist/api/internal/config"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"github.com/wod-strategist/api/internal/storage"
	"go.uber.org/zap"
)

var GitCommit = "dev"

func main() {
	cfg, err := config.InitServer()
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

	// Initialize Redis Connection for Asynq Client
	redisAddr := cfg.RedisURL

	logger.Log.Info("Redis connection established", zap.String("redis_addr", redisAddr))

	redisOpt := asynq.RedisClientOpt{Addr: redisAddr, DB: 5} // Use DB 5 for Asynq tasks
	client := asynq.NewClient(redisOpt)
	defer client.Close()

	// Initialize Storage Client
	storageClient, err := storage.NewClient(context.Background(), cfg.GCSBucketName)
	if err != nil {
		logger.Log.Fatal("Failed to create storage client", zap.Error(err))
	}

	handlers := controllers.New(controllers.Config{
		QueueClient:     client,
		AnalysisResults: controllers.NewGormAnalysisResultRepository(dbConn),
		StorageClient:   storageClient,
		BucketName:      cfg.GCSBucketName,
		GitCommit:       GitCommit,
	})

	// Setup Router
	r, err := server.SetupRouter(cfg.AppEnv, cfg.APISecret, handlers)
	if err != nil {
		logger.Log.Fatal("Failed to setup router", zap.Error(err))
	}
	port := cfg.Port

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Initializing the server in a goroutine so that
	// it won't block the graceful shutdown handling below
	go func() {
		logger.Log.Info("Starting server", zap.String("port", port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Log.Fatal("listen: %s\n", zap.Error(err))
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 5 seconds.
	quit := make(chan os.Signal, 1)
	// kill (no param) default send syscall.SIGTERM
	// kill -2 is syscall.SIGINT
	// kill -9 is syscall.SIGKILL but can't be caught, so don't need to add it
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Log.Info("Shutting down server...")

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Log.Fatal("Server forced to shutdown:", zap.Error(err))
	}

	logger.Log.Info("Server exiting")
}
