package server

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"github.com/wod-strategist/api/internal/auth"
	"github.com/wod-strategist/api/internal/controllers"
)

const APIRoutePrefix = "/api/v1"

var ErrHandlersRequired = errors.New("handlers are required")

func SetupRouter(appEnv string, apiKey string, allowedOrigins []string,
	ctl *controllers.Controller, authCtl *controllers.AuthController, authSvc *auth.Service) (*gin.Engine, error) {
	if ctl == nil {
		return nil, ErrHandlersRequired
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequestLogger())
	r.Use(DevelopmentCORS(allowedOrigins))

	// Public routes (no API key, no auth)
	r.GET("/health", ctl.Health)

	// Mount Swagger UI only in non-production environments
	if appEnv != "production" {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	api := r.Group(APIRoutePrefix)

	// Web auth routes — no API key required.
	// Web SPAs cannot securely embed an API key in client-side JS.
	// These are protected by rate limiting + CORS + password auth instead.
	if authCtl != nil {
		authRL := RateLimitMiddleware(NewRateLimiter(10, 15*time.Minute))
		api.POST("/auth/web/signup", authRL, authCtl.WebSignup)
		api.POST("/auth/web/login", authRL, authCtl.WebLogin)
	}

	// All routes registered after this point require X-API-Key header.
	api.Use(APIKeyMiddleware(apiKey))

	// Mobile auth routes (signup, login) — API key required, rate-limited.
	if authCtl != nil {
		authRL := RateLimitMiddleware(NewRateLimiter(10, 15*time.Minute))
		api.POST("/auth/signup", authRL, authCtl.Signup)
		api.POST("/auth/login", authRL, authCtl.Login)
	}

	// Web-authenticated routes — JWT required, no API key.
	if authCtl != nil && authSvc != nil {
		webAuth := r.Group(APIRoutePrefix, AuthMiddleware(authSvc))
		webAuth.POST("/auth/web/logout", authCtl.WebLogout)
		webAuth.GET("/auth/me", authCtl.GetMe)
	}

	// Apply auth middleware for all subsequent routes (API key + JWT)
	if authSvc != nil {
		api.Use(AuthMiddleware(authSvc))
	}

	// Protected auth routes (mobile — API key + JWT required)
	if authCtl != nil {
		api.POST("/auth/logout", authCtl.Logout)
		api.DELETE("/auth/account", authCtl.DeleteAccount)
	}

	// Protected data routes (API key + JWT required)
	api.POST("/upload-url", ctl.CreateUploadURL)
	api.POST("/upload-complete", ctl.CompleteUpload)
	api.POST("/upload", ctl.Upload)
	api.GET("/analysis/:session_id", ctl.GetAnalysis)
	api.GET("/history", ctl.GetHistory)
	api.POST("/history/:id/archive", ctl.ArchiveHistory)
	api.POST("/history/:id/unarchive", ctl.UnarchiveHistory)
	api.GET("/movements", ctl.ListMovements)
	api.GET("/movement-groups", ctl.ListMovementGroups)
	api.GET("/injuries", ctl.ListInjuries)
	api.POST("/chunk-complete", ctl.ChunkComplete)
	api.GET("/chunk-analysis/:session_id", ctl.GetChunkAnalysis)
	api.POST("/profiles", ctl.CreateProfile)
	api.GET("/profiles/:id", ctl.GetProfile)
	api.GET("/profiles", ctl.ListProfiles)
	api.PUT("/profiles/:id", ctl.UpdateProfile)
	api.POST("/profiles/:id/archive", ctl.ArchiveProfile)
	api.POST("/profiles/:id/unarchive", ctl.UnarchiveProfile)
	api.POST("/merge-chunks", ctl.MergeChunks)
	api.GET("/dev/sessions", ctl.ListSessionCatalog)
	api.GET("/dev/sessions/:session_id/assets", ctl.GetSessionAssets)
	api.GET("/dev/sessions/:session_id/play-url", ctl.GetPlayURL)
	api.GET("/subtitles/:session_id", ctl.GetSubtitles)
	api.POST("/generate-highlight", ctl.GenerateHighlight)
	api.GET("/highlight/:session_id", ctl.GetHighlight)
	api.GET("/highlight-download/:id", ctl.GetHighlightDownloadURL)
	api.POST("/verify-highlights", ctl.VerifyHighlights)
	api.GET("/video-download/:session_id", ctl.GetVideoDownloadURL)
	api.POST("/retry-analysis", ctl.RetryAnalysis)
	api.POST("/generate-hardsub", ctl.GenerateHardSub)
	api.POST("/debug/telemetry", ctl.UploadDebugTelemetry)
	api.POST("/parse-workout-image", ctl.ParseWorkoutImage)

	return r, nil
}
