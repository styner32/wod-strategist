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

var (
	ErrHandlersRequired    = errors.New("handlers are required")
	ErrAuthServiceRequired = errors.New("auth service is required")
)

func SetupRouter(appEnv string, allowedOrigins []string,
	ctl *controllers.Controller, authCtl *controllers.AuthController, authSvc *auth.Service) (*gin.Engine, error) {
	if ctl == nil {
		return nil, ErrHandlersRequired
	}
	if authSvc == nil {
		return nil, ErrAuthServiceRequired
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequestLogger())
	r.Use(DevelopmentCORS(allowedOrigins))

	// Public routes (no auth)
	r.GET("/health", ctl.Health)

	// Mount Swagger UI only in non-production environments
	if appEnv != "production" {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	api := r.Group(APIRoutePrefix)

	// Public credential routes — rate-limited; no JWT required.
	if authCtl != nil {
		authRL := RateLimitMiddleware(NewRateLimiter(10, 15*time.Minute))
		api.POST("/auth/web/signup", authRL, authCtl.WebSignup)
		api.POST("/auth/web/login", authRL, authCtl.WebLogin)
		api.POST("/auth/signup", authRL, authCtl.Signup)
		api.POST("/auth/login", authRL, authCtl.Login)
	}

	// All subsequent routes require a valid JWT (Bearer header or jwt cookie).
	protected := api.Group("", AuthMiddleware(authSvc))

	if authCtl != nil {
		protected.POST("/auth/web/logout", authCtl.WebLogout)
		protected.GET("/auth/me", authCtl.GetMe)
		protected.POST("/auth/logout", authCtl.Logout)
		protected.DELETE("/auth/account", authCtl.DeleteAccount)
	}

	protected.POST("/upload-url", ctl.CreateUploadURL)
	protected.POST("/upload-complete", ctl.CompleteUpload)
	protected.POST("/upload", ctl.Upload)
	protected.GET("/analysis/:session_id", ctl.GetAnalysis)
	protected.GET("/history", ctl.GetHistory)
	protected.POST("/history/:id/archive", ctl.ArchiveHistory)
	protected.POST("/history/:id/unarchive", ctl.UnarchiveHistory)
	protected.GET("/movements", ctl.ListMovements)
	protected.GET("/movement-groups", ctl.ListMovementGroups)
	protected.GET("/injuries", ctl.ListInjuries)
	protected.POST("/chunk-complete", ctl.ChunkComplete)
	protected.GET("/chunk-analysis/:session_id", ctl.GetChunkAnalysis)
	protected.POST("/profiles", ctl.CreateProfile)
	protected.GET("/profiles/:id", ctl.GetProfile)
	protected.GET("/profiles", ctl.ListProfiles)
	protected.PUT("/profiles/:id", ctl.UpdateProfile)
	protected.POST("/profiles/:id/archive", ctl.ArchiveProfile)
	protected.POST("/profiles/:id/unarchive", ctl.UnarchiveProfile)
	protected.POST("/merge-chunks", ctl.MergeChunks)
	protected.GET("/dev/sessions", ctl.ListSessionCatalog)
	protected.GET("/dev/sessions/:session_id/assets", ctl.GetSessionAssets)
	protected.GET("/dev/sessions/:session_id/play-url", ctl.GetPlayURL)
	protected.GET("/subtitles/:session_id", ctl.GetSubtitles)
	protected.POST("/generate-highlight", ctl.GenerateHighlight)
	protected.GET("/highlight/:session_id", ctl.GetHighlight)
	protected.GET("/highlight-download/:id", ctl.GetHighlightDownloadURL)
	protected.POST("/verify-highlights", ctl.VerifyHighlights)
	protected.GET("/video-download/:session_id", ctl.GetVideoDownloadURL)
	protected.POST("/retry-analysis", ctl.RetryAnalysis)
	protected.POST("/generate-hardsub", ctl.GenerateHardSub)
	protected.POST("/debug/telemetry", ctl.UploadDebugTelemetry)
	protected.POST("/parse-workout-image", ctl.ParseWorkoutImage)
	protected.POST("/appearance-from-image", ctl.ParseAppearanceImage)
	protected.POST("/sessions", ctl.CreateSession)
	protected.GET("/sessions/:session_id/analysis", ctl.GetSessionAnalysis)
	protected.GET("/sessions/:session_id/chunks/:chunk_id/play-url", ctl.GetChunkPlayURL)
	protected.POST("/sessions/:session_id/chunks/:chunk_id/reanalyses", ctl.CreateChunkReanalysis)
	protected.GET("/sessions/:session_id/chunks/:chunk_id/reanalyses", ctl.ListChunkReanalyses)
	protected.GET("/sessions/:session_id/chunks/:chunk_id/reanalyses/:run_id", ctl.GetChunkReanalysis)
	protected.POST("/sessions/:session_id/reanalyses", ctl.CreateSessionReanalysis)
	protected.GET("/sessions/:session_id/reanalyses", ctl.ListSessionReanalyses)
	protected.GET("/sessions/:session_id/reanalyses/:run_id", ctl.GetSessionReanalysis)
	protected.POST("/sessions/:session_id/feedback", ctl.CreateFeedback)
	protected.GET("/sessions/:session_id/feedback", ctl.ListFeedback)
	protected.PATCH("/sessions/:session_id/feedback/:feedback_id", ctl.UpdateFeedback)
	protected.DELETE("/sessions/:session_id/feedback/:feedback_id", ctl.DeleteFeedback)

	return r, nil
}
