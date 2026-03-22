package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

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

const APIRoutePrefix = "/api/v1"

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
}

func PublicRouteSpecs() []RouteSpec {
	return cloneRouteSpecs(publicRouteDefinitions)
}

func ProtectedRouteSpecs() []RouteSpec {
	return cloneRouteSpecs(protectedRouteDefinitions)
}

func RegisterPublicRoutes(routes gin.IRoutes, handlers Handlers) {
	validateHandlers(handlers)
	registerRoutes(routes, handlers, publicRouteDefinitions)
}

func RegisterProtectedRoutes(routes gin.IRoutes, handlers Handlers) {
	validateHandlers(handlers)
	registerRoutes(routes, handlers, protectedRouteDefinitions)
}

func SetupRouter(apiKey string, handlers Handlers) *gin.Engine {
	validateHandlers(handlers)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequestLogger())

	RegisterPublicRoutes(r, handlers)

	api := r.Group(APIRoutePrefix)
	api.Use(APIKeyMiddleware(apiKey))
	RegisterProtectedRoutes(api, handlers)

	return r
}

func validateHandlers(handlers Handlers) {
	if handlers == nil {
		panic("handlers are required")
	}
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
