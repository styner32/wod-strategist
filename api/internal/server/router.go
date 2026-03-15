package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var GitCommit = "dev"
var Movements = []string{
	"Burpee",
	"Power Snatch",
	"Hang Power Snatch",
	"Squat Snatch",
	"Hang Squat Snatch",
	"DB Snatch",
	"Hang DB Snatch",
	"Clean & Jerk",
	"Hang Clean & Jerk",
	"Power Clean & Jerk",
	"Hang Power Clean & Jerk",
	"Squat Clean & Jerk",
	"Hang Squat Clean & Jerk",
	"DB Clean & Jerk",
	"Hang DB Clean & Jerk",
	"Thruster",
	"Wallball Shot",
	"Double-under",
	"Deadlift",
	"Back Squat",
	"Overhead Squat",
	"Box Jump",
	"Burpee Box Jump Over",
	"Handstand Push-up",
	"Pull-up",
	"Chest to Bar",
	"Muscle-up",
	"Row",
	"Echo Bike",
	"Skierg",
	"Toes to Bar",
}

type ServerConfig struct {
	QueueClient   *asynq.Client
	DB            *gorm.DB
	StorageClient *storage.Client
	APIKey        string
	BucketName    string
}

func SetupRouter(config *ServerConfig) *gin.Engine {
	// Use gin.New() instead of Default() to avoid default logger which uses standard log package
	r := gin.New()
	r.Use(gin.Recovery())

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
		if apiSecret == "" {
			logger.Log.Error("API_SECRET is not set, but is required for this endpoint")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		if apiSecret != "" {
			apiKey := c.GetHeader("X-API-Key")
			apiKeyHash := sha256.Sum256([]byte(apiKey))
			apiSecretHash := sha256.Sum256([]byte(apiSecret))

			if subtle.ConstantTimeCompare(apiKeyHash[:], apiSecretHash[:]) != 1 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				return
			}
		}
		c.Next()
	})

	{
		// 1. Get Signed URL
		api.POST("/upload-url", func(c *gin.Context) {
			var req struct {
				SessionID string `json:"session_id" binding:"required"`
				Filename  string `json:"filename" binding:"required"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}

			safeSessionID := filepath.Base(req.SessionID)
			safeFilename := filepath.Base(req.Filename)
			objectName := fmt.Sprintf("videos/%s_%s", safeSessionID, safeFilename)

			// Generate Signed URL (valid for 15 minutes)
			signedURL, err := config.StorageClient.GenerateSignedURL(objectName, "PUT", 15*time.Minute)
			if err != nil {
				logger.Log.Error("failed to generate signed URL", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"upload_url": signedURL,
				"gcs_uri":    fmt.Sprintf("gs://%s/%s", config.BucketName, objectName),
			})
		})

		// 2. Notify Upload Complete & Start Analysis
		api.POST("/upload-complete", func(c *gin.Context) {
			var req struct {
				SessionID string   `json:"session_id" binding:"required"`
				GcsURI    string   `json:"gcs_uri" binding:"required"`
				Movements []string `json:"movements" binding:"required"`
			}

			if err := c.ShouldBindJSON(&req); err != nil {
				logger.Log.Error("failed to bind JSON", zap.Error(err))
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}

			// Validate GCS URI to prevent SSRF and arbitrary file reads/deletions
if u, err := url.Parse(req.GcsURI); err != nil || u.Scheme != "gs" || u.Host == "" {
				logger.Log.Error("invalid GCS URI: must be a valid gs:// URI with a bucket", zap.String("uri", req.GcsURI))
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid GCS URI"})
				return
			}

			// if req.Movements does not appear in Movements, return error
			if len(req.Movements) > 0 {
				validMovements := false
				for _, movement := range req.Movements {
					for _, m := range Movements {
						if m == movement {
							validMovements = true
							break
						}
					}
				}

				if !validMovements {
					logger.Log.Error("invalid movements", zap.Strings("movements", req.Movements))
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid movements"})
					return
				}
			}

			if len(req.Movements) >= 100 {
				logger.Log.Error("too many movements", zap.Int("count", len(req.Movements)))
				c.JSON(http.StatusBadRequest, gin.H{"error": "too many movements"})
				return
			}

			// Enqueue task with GCS URI
			task, err := worker.NewVideoAnalysisTask(req.SessionID, req.GcsURI, req.Movements...)
			if err != nil {
				logger.Log.Error("failed to create task", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
				return
			}

			info, err := config.QueueClient.Enqueue(task)
			if err != nil {
				logger.Log.Error("failed to enqueue task", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
				return
			}

			c.JSON(http.StatusAccepted, gin.H{
				"message":    "Analysis started",
				"task_id":    info.ID,
				"session_id": req.SessionID,
			})
		})

		// Deprecated: Direct upload (keep for small files/legacy testing if needed, or remove)
		api.POST("/upload", func(c *gin.Context) {
			sessionID := c.PostForm("session_id")
			if sessionID == "" {
				logger.Log.Error("session_id is required")
				c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
				return
			}

			fileHeader, err := c.FormFile("file")
			if err != nil {
				logger.Log.Error("file is required", zap.Error(err))
				c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
				return
			}

			// Open the file
			file, err := fileHeader.Open()
			if err != nil {
				logger.Log.Error("failed to open file", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open file"})
				return
			}
			defer file.Close()

			// Sanitize sessionID to prevent path traversal
			safeSessionID := filepath.Base(sessionID)

			// Generate GCS object name
			objectName := fmt.Sprintf("videos/%s_%s", safeSessionID, filepath.Base(fileHeader.Filename))

			// Upload to GCS
			gcsURI, err := config.StorageClient.UploadFile(c.Request.Context(), file, objectName)
			if err != nil {
				logger.Log.Error("failed to upload file to GCS", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
				return
			}

			// Enqueue task with GCS URI
			task, err := worker.NewVideoAnalysisTask(sessionID, gcsURI)
			if err != nil {
				logger.Log.Error("failed to create task", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
				return
			}

			info, err := config.QueueClient.Enqueue(task)
			if err != nil {
				logger.Log.Error("failed to enqueue task", zap.Error(err))
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
			if err := config.DB.Where("session_id = ?", sessionID).Find(&results).Error; err != nil {
				logger.Log.Error("failed to fetch results", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch results"})
				return
			}

			c.JSON(http.StatusOK, results)
		})

		api.GET("/history", func(c *gin.Context) {
			var results []db.AnalysisResult
			if err := config.DB.Order("created_at desc").Limit(20).Find(&results).Error; err != nil {
				logger.Log.Error("failed to fetch history", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
				return
			}
			c.JSON(http.StatusOK, results)
		})

		api.GET("/movements", func(c *gin.Context) {
			c.JSON(http.StatusOK, Movements)
		})
	}

	return r
}
