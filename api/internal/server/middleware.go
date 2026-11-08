package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/auth"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

const minGzipResponseBytes = 1024

var gzipWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
		return w
	},
}

func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		start := time.Now()
		ctx, qStats := db.WithQueryStats(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		// user_id is set by AuthMiddleware after JWT validation.
		// 0 means no auth context (unauthenticated or auth middleware not applied).
		userID, _ := c.Get("user_id")
		snap := qStats.Snapshot()
		respBytes := c.Writer.Size()
		if respBytes < 0 {
			respBytes = 0
		}

		logger.Log.Info("Request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.Any("user_id", userID),
			zap.Int64("db_queries", snap.Queries),
			zap.Int64("db_ms", snap.Duration.Milliseconds()),
			zap.Int64("db_slow_queries", snap.SlowQueries),
			zap.Int("response_bytes", respBytes),
		)
	}
}

// GzipMiddleware compresses JSON responses >= 1 KiB using stdlib compress/gzip
// when the client advertises Accept-Encoding: gzip and Content-Encoding is unset.
func GzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodHead || !acceptsGzipEncoding(c.GetHeader("Accept-Encoding")) {
			c.Next()
			return
		}
		gw := &gzipThresholdWriter{
			ResponseWriter: c.Writer,
			statusCode:     http.StatusOK,
		}
		c.Writer = gw
		defer gw.finish()
		c.Next()
	}
}

func acceptsGzipEncoding(header string) bool {
	if header == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segments := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(segments[0]))
		if coding != "gzip" {
			continue
		}
		for _, param := range segments[1:] {
			kv := strings.SplitN(strings.TrimSpace(param), "=", 2)
			if len(kv) == 2 && strings.EqualFold(strings.TrimSpace(kv[0]), "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64); err == nil && q <= 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

func addVaryValue(h http.Header, token string) {
	for _, existing := range h.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), token) {
				return
			}
		}
	}
	h.Add("Vary", token)
}

type gzipThresholdWriter struct {
	gin.ResponseWriter
	buf          bytes.Buffer
	gz           *gzip.Writer
	statusCode   int
	wroteHeader  bool
	decided      bool
	compressing  bool
	writtenBytes int
}

func (w *gzipThresholdWriter) WriteHeader(code int) {
	w.statusCode = code
	w.wroteHeader = true
	if code < 200 || code == http.StatusNoContent || code == http.StatusNotModified {
		w.decided = true
		w.compressing = false
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *gzipThresholdWriter) WriteHeaderNow() {
	if !w.decided {
		w.decide(false)
	}
	w.ResponseWriter.WriteHeaderNow()
}

func (w *gzipThresholdWriter) Status() int {
	if w.wroteHeader {
		return w.statusCode
	}
	return w.ResponseWriter.Status()
}

func (w *gzipThresholdWriter) Size() int {
	if w.decided {
		return w.ResponseWriter.Size()
	}
	return w.writtenBytes
}

func (w *gzipThresholdWriter) Written() bool {
	return w.wroteHeader || w.writtenBytes > 0 || w.ResponseWriter.Written()
}

func (w *gzipThresholdWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *gzipThresholdWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if !w.wroteHeader {
		w.statusCode = http.StatusOK
		w.wroteHeader = true
	}
	if !w.decided {
		if w.buf.Len()+len(data) < minGzipResponseBytes {
			n, err := w.buf.Write(data)
			w.writtenBytes += n
			return n, err
		}
		w.decide(true)
	}
	if w.compressing {
		return w.gz.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *gzipThresholdWriter) Flush() {
	if !w.decided {
		w.decide(w.buf.Len() >= minGzipResponseBytes)
	}
	if w.compressing && w.gz != nil {
		_ = w.gz.Flush()
	}
	w.ResponseWriter.Flush()
}

func (w *gzipThresholdWriter) isJSONEligible() bool {
	if w.statusCode < 200 || w.statusCode == http.StatusNoContent || w.statusCode == http.StatusNotModified {
		return false
	}
	h := w.Header()
	if strings.TrimSpace(h.Get("Content-Encoding")) != "" {
		return false
	}
	ct := strings.ToLower(h.Get("Content-Type"))
	return strings.Contains(ct, "application/json")
}

func (w *gzipThresholdWriter) decide(allowCompress bool) {
	if w.decided {
		return
	}
	w.decided = true
	h := w.Header()
	if strings.Contains(strings.ToLower(h.Get("Content-Type")), "application/json") {
		addVaryValue(h, "Accept-Encoding")
	}
	if allowCompress && w.isJSONEligible() {
		w.compressing = true
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		w.ResponseWriter.WriteHeader(w.statusCode)
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.gz = gz
		if w.buf.Len() > 0 {
			_, _ = w.gz.Write(w.buf.Bytes())
			w.buf.Reset()
		}
		return
	}
	w.compressing = false
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(w.statusCode)
	}
	if w.buf.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.buf.Bytes())
		w.buf.Reset()
	}
}

func (w *gzipThresholdWriter) finish() {
	if !w.decided {
		w.decide(w.buf.Len() >= minGzipResponseBytes)
	}
	if w.compressing && w.gz != nil {
		_ = w.gz.Close()
		w.gz.Reset(io.Discard)
		gzipWriterPool.Put(w.gz)
		w.gz = nil
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

	allowMethods := "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	allowHeaders := "Content-Type, Authorization"

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
