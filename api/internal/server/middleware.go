package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		start := time.Now()

		c.Next()

		logger.Log.Info("Request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
		)
	}
}

func APIKeyMiddleware(configuredKey string) gin.HandlerFunc {
	apiSecret := strings.TrimSpace(configuredKey)
	apiSecretHash := sha256.Sum256([]byte(apiSecret))

	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-Key")
		apiKeyHash := sha256.Sum256([]byte(apiKey))
		if subtle.ConstantTimeCompare(apiKeyHash[:], apiSecretHash[:]) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}
