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

		// user_id is set by AuthMiddleware after JWT validation.
		// 0 means no auth context (unauthenticated or auth middleware not applied).
		userID, _ := c.Get("user_id")

		logger.Log.Info("Request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.Any("user_id", userID),
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

	allowMethods := "GET, POST, PUT, DELETE, OPTIONS"
	allowHeaders := "Content-Type, X-API-Key, Authorization"

	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			c.Next()
			return
		}

		if !isOriginAllowed(origin, allowed) {
			logger.Log.Warn("CORS: origin not in allowed list",
				zap.String("origin", origin),
				zap.String("method", c.Request.Method),
				zap.String("path", c.Request.URL.Path))
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
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "3600")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// isOriginAllowed returns true if the origin is in the explicit allowed set,
// or if the origin's host is localhost / 127.0.0.1 (any port).
func isOriginAllowed(origin string, allowed map[string]struct{}) bool {
	if _, ok := allowed[origin]; ok {
		return true
	}
	// Allow any port on localhost / 127.0.0.1 for local development.
	host := extractHost(origin)
	return host == "localhost" || host == "127.0.0.1"
}

// extractHost returns the hostname from an origin URL (e.g. "http://localhost:5173" → "localhost").
func extractHost(origin string) string {
	// Strip scheme (http:// or https://)
	after := origin
	if idx := strings.Index(after, "://"); idx != -1 {
		after = after[idx+3:]
	}
	// Strip port
	if idx := strings.IndexByte(after, ':'); idx != -1 {
		after = after[:idx]
	}
	// Strip path (shouldn't appear in Origin, but be safe)
	if idx := strings.IndexByte(after, '/'); idx != -1 {
		after = after[:idx]
	}
	return after
}

func APIKeyMiddleware(configuredKey string) gin.HandlerFunc {
	apiSecret := strings.TrimSpace(configuredKey)
	apiSecretHash := sha256.Sum256([]byte(apiSecret))

	return func(c *gin.Context) {
		// Skip API key check if a jwt cookie is present — the web SPA
		// authenticates via httpOnly cookie, which AuthMiddleware validates
		// downstream.  Requiring an API key on top of that would force the
		// SPA to embed a secret in client-side JS.
		// NOTE: We must not skip this check for mobile auth endpoints (/auth/login, /auth/signup)
		// because they are not protected by AuthMiddleware, leading to an API key bypass.
		isMobileAuthRoute := c.FullPath() == APIRoutePrefix+"/auth/login" || c.FullPath() == APIRoutePrefix+"/auth/signup"

		if !isMobileAuthRoute {
			if cookie, err := c.Cookie("jwt"); err == nil && cookie != "" {
				c.Next()
				return
			}
		}

		apiKey := c.GetHeader("X-API-Key")
		apiKeyHash := sha256.Sum256([]byte(apiKey))
		if subtle.ConstantTimeCompare(apiKeyHash[:], apiSecretHash[:]) != 1 {
			logger.Log.Warn("APIKeyMiddleware: rejected",
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
				zap.Bool("key_present", apiKey != ""))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}

// AuthMiddleware validates a JWT from either the Authorization header (mobile)
// or the "jwt" cookie (web) and sets "user_id" in the context.
func AuthMiddleware(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var rawToken string

		// 1. Try Authorization: Bearer header (mobile)
		if authHeader := c.GetHeader("Authorization"); authHeader != "" {
			if !strings.HasPrefix(authHeader, "Bearer ") {
				logger.Log.Warn("auth: Authorization header not Bearer",
					zap.String("path", c.Request.URL.Path),
					zap.Int("header_len", len(authHeader)))
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
				return
			}
			rawToken = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// 2. Fallback to "jwt" cookie (web)
		if rawToken == "" {
			cookie, err := c.Cookie("jwt")
			if err != nil || cookie == "" {
				logger.Log.Warn("auth: no Authorization header or jwt cookie",
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method))
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization"})
				return
			}
			rawToken = cookie
		}

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
