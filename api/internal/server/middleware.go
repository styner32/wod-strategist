package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/auth"
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

func DevelopmentCORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		trimmed := strings.TrimSpace(origin)
		if trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}

	allowMethods := "GET, POST, OPTIONS"
	allowHeaders := "Content-Type, X-API-Key, Authorization"

	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			c.Next()
			return
		}

		if _, ok := allowed[origin]; !ok {
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Vary", "Origin")
		c.Header("Access-Control-Allow-Methods", allowMethods)
		c.Header("Access-Control-Allow-Headers", allowHeaders)
		c.Header("Access-Control-Max-Age", "3600")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
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

// AuthMiddleware validates a Bearer JWT token and sets "user_id" in the context.
func AuthMiddleware(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			logger.Log.Warn("auth: no Authorization header",
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") {
			logger.Log.Warn("auth: Authorization header not Bearer",
				zap.String("path", c.Request.URL.Path),
				zap.Int("header_len", len(authHeader)))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
			return
		}

		rawToken := strings.TrimPrefix(authHeader, "Bearer ")
		userID, err := svc.ValidateToken(c.Request.Context(), rawToken)
		if err != nil {
			// Log a short hash digest of the token for correlation across requests
			// without exposing the token itself.
			digest := sha256.Sum256([]byte(rawToken))
			logger.Log.Warn("auth: token validation failed",
				zap.String("path", c.Request.URL.Path),
				zap.String("token_digest", hex.EncodeToString(digest[:4])),
				zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		logger.Log.Debug("auth: token validated",
			zap.Uint("user_id", userID),
			zap.String("path", c.Request.URL.Path))
		c.Set("user_id", userID)
		c.Next()
	}
}
