package testhelpers

import (
	"os"

	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

func InitLogger() {
	if os.Getenv("SHOW_LOG") == "true" {
		loggerConfig := zap.NewDevelopmentConfig()
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
		loggerConfig.DisableStacktrace = true
		logger.Log, _ = loggerConfig.Build()
	} else {
		logger.Log = zap.NewNop()
	}
}
