package server

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// rateLimitEntry tracks request counts per IP in a sliding window.
type rateLimitEntry struct {
	count     int
	windowEnd time.Time
}

// RateLimiter provides per-IP rate limiting using a sliding window.
type RateLimiter struct {
	mu       sync.Mutex
	entries  map[string]*rateLimitEntry
	limit    int
	window   time.Duration
	lastPurge time.Time
}

// NewRateLimiter creates a rate limiter with the given max requests per window.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		entries:   make(map[string]*rateLimitEntry),
		limit:     limit,
		window:    window,
		lastPurge: time.Now(),
	}
}

// Allow returns true if the request from the given IP is within the rate limit.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Periodic purge of expired entries (every window duration)
	if now.Sub(rl.lastPurge) > rl.window {
		for key, entry := range rl.entries {
			if now.After(entry.windowEnd) {
				delete(rl.entries, key)
			}
		}
		rl.lastPurge = now
	}

	entry, exists := rl.entries[ip]
	if !exists || now.After(entry.windowEnd) {
		rl.entries[ip] = &rateLimitEntry{
			count:     1,
			windowEnd: now.Add(rl.window),
		}
		return true
	}

	entry.count++
	return entry.count <= rl.limit
}

// RateLimitMiddleware returns a gin middleware that rate limits by client IP.
func RateLimitMiddleware(rl *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !rl.Allow(ip) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests, please try again later",
			})
			return
		}
		c.Next()
	}
}
