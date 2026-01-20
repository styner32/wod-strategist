package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/worker"
)

func SetupRouter(client *asynq.Client) *gin.Engine {
	r := gin.Default()

	api := r.Group("/api/v1")
	{
		api.POST("/upload", func(c *gin.Context) {
			sessionID := c.PostForm("session_id")
			if sessionID == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
				return
			}

			file, err := c.FormFile("file")
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
				return
			}

			// Ensure tmp directory exists
			tmpDir := "tmp"
			if err := os.MkdirAll(tmpDir, 0755); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create temp directory"})
				return
			}

			// Sanitize sessionID to prevent path traversal
			safeSessionID := filepath.Base(sessionID)

			// Save file to a temporary location
			filename := fmt.Sprintf("%s_%s", safeSessionID, filepath.Base(file.Filename))
			filePath := filepath.Join(tmpDir, filename)
			if err := c.SaveUploadedFile(file, filePath); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
				return
			}

			// Enqueue task
			task, err := worker.NewVideoAnalysisTask(sessionID, filePath)
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
