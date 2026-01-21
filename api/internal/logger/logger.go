package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

func Init() {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// Check if running in development (e.g. via environment variable)
	// For now, we default to production JSON logging as requested ("set logger like zap").
	// If the user wanted dev-friendly logs (colored console), we could use NewDevelopmentConfig().
	// Given the context of "running running worker and api separately... graceful shutdown",
	// a structured logger is usually preferred for backend services.
	// But let's check if we are in dev environment to make it nicer for local dev.
	if os.Getenv("APP_ENV") == "development" {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	var err error
	Log, err = config.Build()
	if err != nil {
		panic(err)
	}
}

func Sync() {
	if Log != nil {
		_ = Log.Sync()
	}
}
