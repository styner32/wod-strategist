package logger_test

import (
	"bytes"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var _ = Describe("Cloud Logging configuration", func() {
	It("writes structured JSON to stdout with Cloud Logging severity and message keys", func() {
		cfg := logger.NewCloudLoggingConfig()
		Expect(cfg.OutputPaths).To(Equal([]string{"stdout"}))
		Expect(cfg.ErrorOutputPaths).To(Equal([]string{"stderr"}))
		Expect(cfg.EncoderConfig.LevelKey).To(Equal("severity"))
		Expect(cfg.EncoderConfig.MessageKey).To(Equal("message"))

		var buf bytes.Buffer
		enc := zapcore.NewJSONEncoder(cfg.EncoderConfig)
		log := zap.New(zapcore.NewCore(enc, zapcore.AddSync(&buf), zapcore.DebugLevel))

		log.Debug("debug entry")
		log.Info("info entry")
		log.Warn("warn entry", zap.Int("attempt", 2))
		log.Error("error entry")

		lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
		Expect(lines).To(HaveLen(4))

		expected := []struct {
			severity string
			message  string
		}{
			{"DEBUG", "debug entry"},
			{"INFO", "info entry"},
			{"WARNING", "warn entry"},
			{"ERROR", "error entry"},
		}
		for i, exp := range expected {
			var payload map[string]any
			Expect(json.Unmarshal(lines[i], &payload)).To(Succeed())
			Expect(payload["severity"]).To(Equal(exp.severity))
			Expect(payload["message"]).To(Equal(exp.message))
			Expect(payload["time"]).NotTo(BeEmpty())
		}
	})
})
