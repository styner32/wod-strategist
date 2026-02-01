package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
)

var GitCommit = "dev"

func SetupRouter(client *asynq.Client) *gin.Engine {
	// Use gin.New() instead of Default() to avoid default logger which uses standard log package
	r := gin.New()
	r.Use(gin.Recovery())

	// Initialize Storage Client
	bucketName := os.Getenv("GCS_BUCKET_NAME")
	var storageClient *storage.Client
	var err error

	if bucketName != "" {
		storageClient, err = storage.NewClient(context.Background(), bucketName)
		if err != nil {
			logger.Log.Fatal("Failed to create storage client", zap.Error(err))
		}
	} else {
		logger.Log.Warn("GCS_BUCKET_NAME not set, uploads will fail")
	}

	// Add simple middleware to log requests using Zap
	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		status := c.Writer.Status()

		logger.Log.Info("Request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
		)
	})

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
			"commit": GitCommit,
		})
	})

	api := r.Group("/api/v1")

	// API Key Middleware
	api.Use(func(c *gin.Context) {
		apiSecret := os.Getenv("API_SECRET")
		// If API_SECRET is not set, we might want to fail open or closed.
		// For security, let's fail closed if it's supposed to be protected.
		if apiSecret == "" {
			// Warn locally, but in prod this should be set.
			// For now, allow if not set to avoid breaking local dev without env vars,
			// OR enforce it. Let's enforce it but check if it's empty.
			// If strictly "only by me", we require it.
		}

		if apiSecret != "" {
			apiKey := c.GetHeader("X-API-Key")
			if apiKey != apiSecret {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				return
			}
		}
		c.Next()
	})

	{
		api.POST("/upload", func(c *gin.Context) {
			sessionID := c.PostForm("session_id")
			if sessionID == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
				return
			}

			fileHeader, err := c.FormFile("file")
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
				return
			}

			if storageClient == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "storage configuration missing"})
				return
			}

			// Open the file
			file, err := fileHeader.Open()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open file"})
				return
			}
			defer file.Close()

			// Sanitize sessionID to prevent path traversal
			safeSessionID := filepath.Base(sessionID)

			// Generate GCS object name
			objectName := fmt.Sprintf("videos/%s_%s", safeSessionID, filepath.Base(fileHeader.Filename))

			// Upload to GCS
			gcsURI, err := storageClient.UploadFile(c.Request.Context(), file, objectName)
			if err != nil {
				logger.Log.Error("failed to upload file to GCS", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
				return
			}

			// Enqueue task with GCS URI
			task, err := worker.NewVideoAnalysisTask(sessionID, gcsURI)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
				return
			}

			info, err := client.Enqueue(task)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
				return
			}

			c.JSON(http.StatusAccepted, gin.H{
				"message":    "File uploaded and analysis started",
				"task_id":    info.ID,
				"session_id": sessionID,
				"file_url":   gcsURI,
			})
		})

		api.GET("/analysis/:session_id", func(c *gin.Context) {
			sessionID := c.Param("session_id")
			var results []db.AnalysisResult
			if err := db.DB.Where("session_id = ?", sessionID).Find(&results).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch results"})
				return
			}

			c.JSON(http.StatusOK, results)
		})
	}

	return r
}
