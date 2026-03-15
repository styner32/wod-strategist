package server

import "github.com/gin-gonic/gin"

type Handlers interface {
	Health(*gin.Context)
	CreateUploadURL(*gin.Context)
	CompleteUpload(*gin.Context)
	Upload(*gin.Context)
	GetAnalysis(*gin.Context)
	GetHistory(*gin.Context)
	ListMovements(*gin.Context)
	ListInjuries(*gin.Context)
}

func SetupRouter(apiKey string, handlers Handlers) *gin.Engine {
	if handlers == nil {
		panic("handlers are required")
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequestLogger())

	r.GET("/health", handlers.Health)

	api := r.Group("/api/v1")
	api.Use(APIKeyMiddleware(apiKey))
	api.POST("/upload-url", handlers.CreateUploadURL)
	api.POST("/upload-complete", handlers.CompleteUpload)
	api.POST("/upload", handlers.Upload)
	api.GET("/analysis/:session_id", handlers.GetAnalysis)
	api.GET("/history", handlers.GetHistory)
	api.GET("/movements", handlers.ListMovements)
	api.GET("/injuries", handlers.ListInjuries)

	return r
}
