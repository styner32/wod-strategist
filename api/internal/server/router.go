package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

type Handlers interface {
	Health(*gin.Context)
	CreateUploadURL(*gin.Context)
	CompleteUpload(*gin.Context)
	Upload(*gin.Context)
	GetAnalysis(*gin.Context)
	GetChunkAnalysis(*gin.Context)
	GetHistory(*gin.Context)
	ListMovements(*gin.Context)
	ListInjuries(*gin.Context)
	ChunkComplete(*gin.Context)
	CreateProfile(*gin.Context)
	GetProfile(*gin.Context)
	MergeChunks(*gin.Context)
	GetSubtitles(*gin.Context)
	GenerateHighlight(*gin.Context)
	GetHighlight(*gin.Context)
}

const APIRoutePrefix = "/api/v1"

var ErrHandlersRequired = errors.New("handlers are required")

type RouteSpec struct {
	Name   string
	Method string
	Path   string
}

type routeDefinition struct {
	spec     RouteSpec
	register func(gin.IRoutes, Handlers)
}

var publicRouteDefinitions = []routeDefinition{
	{
		spec: RouteSpec{
			Name:   "health",
			Method: http.MethodGet,
			Path:   "/health",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/health", handlers.Health)
		},
	},
}

var protectedRouteDefinitions = []routeDefinition{
	{
		spec: RouteSpec{
			Name:   "upload-url",
			Method: http.MethodPost,
			Path:   APIRoutePrefix + "/upload-url",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.POST("/upload-url", handlers.CreateUploadURL)
		},
	},
	{
		spec: RouteSpec{
			Name:   "upload-complete",
			Method: http.MethodPost,
			Path:   APIRoutePrefix + "/upload-complete",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.POST("/upload-complete", handlers.CompleteUpload)
		},
	},
	{
		spec: RouteSpec{
			Name:   "upload",
			Method: http.MethodPost,
			Path:   APIRoutePrefix + "/upload",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.POST("/upload", handlers.Upload)
		},
	},
	{
		spec: RouteSpec{
			Name:   "analysis",
			Method: http.MethodGet,
			Path:   APIRoutePrefix + "/analysis/:session_id",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/analysis/:session_id", handlers.GetAnalysis)
		},
	},
	{
		spec: RouteSpec{
			Name:   "history",
			Method: http.MethodGet,
			Path:   APIRoutePrefix + "/history",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/history", handlers.GetHistory)
		},
	},
	{
		spec: RouteSpec{
			Name:   "movements",
			Method: http.MethodGet,
			Path:   APIRoutePrefix + "/movements",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/movements", handlers.ListMovements)
		},
	},
	{
		spec: RouteSpec{
			Name:   "injuries",
			Method: http.MethodGet,
			Path:   APIRoutePrefix + "/injuries",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/injuries", handlers.ListInjuries)
		},
	},
	{
		spec: RouteSpec{
			Name:   "chunk-complete",
			Method: http.MethodPost,
			Path:   APIRoutePrefix + "/chunk-complete",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.POST("/chunk-complete", handlers.ChunkComplete)
		},
	},
	{
		spec: RouteSpec{
			Name:   "chunk-analysis",
			Method: http.MethodGet,
			Path:   APIRoutePrefix + "/chunk-analysis/:session_id",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/chunk-analysis/:session_id", handlers.GetChunkAnalysis)
		},
	},
	{
		spec: RouteSpec{
			Name:   "create-profile",
			Method: http.MethodPost,
			Path:   APIRoutePrefix + "/profiles",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.POST("/profiles", handlers.CreateProfile)
		},
	},
	{
		spec: RouteSpec{
			Name:   "get-profile",
			Method: http.MethodGet,
			Path:   APIRoutePrefix + "/profiles/:id",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/profiles/:id", handlers.GetProfile)
		},
	},
	{
		spec: RouteSpec{
			Name:   "merge-chunks",
			Method: http.MethodPost,
			Path:   APIRoutePrefix + "/merge-chunks",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.POST("/merge-chunks", handlers.MergeChunks)
		},
	},
	{
		spec: RouteSpec{
			Name:   "subtitles",
			Method: http.MethodGet,
			Path:   APIRoutePrefix + "/subtitles/:session_id",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/subtitles/:session_id", handlers.GetSubtitles)
		},
	},
	{
		spec: RouteSpec{
			Name:   "generate-highlight",
			Method: http.MethodPost,
			Path:   APIRoutePrefix + "/generate-highlight",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.POST("/generate-highlight", handlers.GenerateHighlight)
		},
	},
	{
		spec: RouteSpec{
			Name:   "highlight",
			Method: http.MethodGet,
			Path:   APIRoutePrefix + "/highlight/:session_id",
		},
		register: func(routes gin.IRoutes, handlers Handlers) {
			routes.GET("/highlight/:session_id", handlers.GetHighlight)
		},
	},
}

func PublicRouteSpecs() []RouteSpec {
	return cloneRouteSpecs(publicRouteDefinitions)
}

func ProtectedRouteSpecs() []RouteSpec {
	return cloneRouteSpecs(protectedRouteDefinitions)
}

func RegisterPublicRoutes(routes gin.IRoutes, handlers Handlers) error {
	if err := validateHandlers(handlers); err != nil {
		return err
	}
	registerRoutes(routes, handlers, publicRouteDefinitions)
	return nil
}

func RegisterProtectedRoutes(routes gin.IRoutes, handlers Handlers) error {
	if err := validateHandlers(handlers); err != nil {
		return err
	}
	registerRoutes(routes, handlers, protectedRouteDefinitions)
	return nil
}

func SetupRouter(appEnv string, apiKey string, handlers Handlers) (*gin.Engine, error) {
	if err := validateHandlers(handlers); err != nil {
		return nil, err
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequestLogger())

	if err := RegisterPublicRoutes(r, handlers); err != nil {
		return nil, err
	}

	// Mount Swagger UI only in non-production environments
	if appEnv != "production" {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	api := r.Group(APIRoutePrefix)
	api.Use(APIKeyMiddleware(apiKey))
	if err := RegisterProtectedRoutes(api, handlers); err != nil {
		return nil, err
	}

	return r, nil
}

func validateHandlers(handlers Handlers) error {
	if handlers == nil {
		return ErrHandlersRequired
	}
	return nil
}

func registerRoutes(routes gin.IRoutes, handlers Handlers, definitions []routeDefinition) {
	for _, definition := range definitions {
		definition.register(routes, handlers)
	}
}

func cloneRouteSpecs(definitions []routeDefinition) []RouteSpec {
	specs := make([]RouteSpec, len(definitions))
	for i, definition := range definitions {
		specs[i] = definition.spec
	}
	return specs
}
