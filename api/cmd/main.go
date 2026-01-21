package main

import (
	"log"
	"os"

	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/server"
	"github.com/wod-strategist/api/internal/worker"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	// Initialize Database
	db.Connect()

	// Initialize Redis Connection for Asynq
	redisAddr := os.Getenv("REDIS_URL")
	if redisAddr == "" {
		redisAddr = "redis://localhost:6379/1"
	}
	redisOpt := asynq.RedisClientOpt{Addr: redisAddr}

	// Start Asynq Server (Worker)
	srv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: 10,
		},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TypeVideoAnalysis, worker.HandleVideoAnalysisTask)

	go func() {
		if err := srv.Run(mux); err != nil {
			log.Fatalf("could not run asynq server: %v", err)
		}
	}()

	// Start Web Server
	client := asynq.NewClient(redisOpt)
	defer client.Close()

	r := server.SetupRouter(client)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
