package server_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/server"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

var _ = Describe("GzipMiddleware and RequestLogger", func() {
	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
	})

	It("compresses JSON responses >= 1 KiB when Accept-Encoding includes gzip and skips smaller or disabled responses", func() {
		r := gin.New()
		r.Use(server.GzipMiddleware())

		largePayload := `{"items":["` + strings.Repeat("wallball-shot-rep-", 80) + `"]}`
		Expect(len(largePayload)).To(BeNumerically(">=", 1024))
		smallPayload := `{"status":"ok"}`

		r.GET("/large", func(c *gin.Context) {
			c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(largePayload))
		})
		r.GET("/small", func(c *gin.Context) {
			c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(smallPayload))
		})
		r.GET("/binary", func(c *gin.Context) {
			c.Data(http.StatusOK, "video/mp4", bytes.Repeat([]byte{0x42}, 2048))
		})

		// 1. Large JSON + Accept-Encoding: gzip -> compressed
		req := httptest.NewRequest(http.MethodGet, "/large", nil)
		req.Header.Set("Accept-Encoding", "br, gzip;q=1.0")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(w.Header().Get("Content-Encoding")).To(Equal("gzip"))
		Expect(w.Header().Get("Vary")).To(ContainSubstring("Accept-Encoding"))
		Expect(w.Body.Len()).To(BeNumerically("<", len(largePayload)))

		gz, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
		Expect(err).NotTo(HaveOccurred())
		decompressed, err := io.ReadAll(gz)
		Expect(err).NotTo(HaveOccurred())
		Expect(gz.Close()).To(Succeed())
		Expect(string(decompressed)).To(Equal(largePayload))

		// 2. Small JSON (< 1 KiB) -> uncompressed
		reqSmall := httptest.NewRequest(http.MethodGet, "/small", nil)
		reqSmall.Header.Set("Accept-Encoding", "gzip")
		wSmall := httptest.NewRecorder()
		r.ServeHTTP(wSmall, reqSmall)
		Expect(wSmall.Code).To(Equal(http.StatusOK))
		Expect(wSmall.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(wSmall.Body.String()).To(Equal(smallPayload))

		// 3. gzip;q=0 -> uncompressed
		reqNoGzip := httptest.NewRequest(http.MethodGet, "/large", nil)
		reqNoGzip.Header.Set("Accept-Encoding", "gzip;q=0")
		wNoGzip := httptest.NewRecorder()
		r.ServeHTTP(wNoGzip, reqNoGzip)
		Expect(wNoGzip.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(wNoGzip.Body.String()).To(Equal(largePayload))

		// 4. Non-JSON -> uncompressed
		reqBin := httptest.NewRequest(http.MethodGet, "/binary", nil)
		reqBin.Header.Set("Accept-Encoding", "gzip")
		wBin := httptest.NewRecorder()
		r.ServeHTTP(wBin, reqBin)
		Expect(wBin.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(wBin.Body.Len()).To(Equal(2048))
	})

	It("records per-request SQL query counts, durations, slow queries, and response bytes in RequestLogger", func() {
		core, logs := observer.New(zap.InfoLevel)
		prevLog := logger.Log
		logger.Log = zap.New(core)
		DeferCleanup(func() { logger.Log = prevLog })

		gormLog := db.NewGormLogger(logger.Log, 50*time.Millisecond)
		r := gin.New()
		r.Use(server.RequestLogger())
		r.GET("/api/v1/test", func(c *gin.Context) {
			ctx := c.Request.Context()
			gormLog.Trace(ctx, time.Now().Add(-10*time.Millisecond), func() (string, int64) {
				return "SELECT 1", 1
			}, nil)
			gormLog.Trace(ctx, time.Now().Add(-75*time.Millisecond), func() (string, int64) {
				return "SELECT pg_sleep(0.075)", 1
			}, nil)
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
		Expect(w.Code).To(Equal(http.StatusOK))

		reqLogs := logs.FilterMessage("Request").All()
		Expect(reqLogs).To(HaveLen(1))
		ctxMap := reqLogs[0].ContextMap()
		Expect(ctxMap["db_queries"]).To(Equal(int64(2)))
		Expect(ctxMap["db_slow_queries"]).To(Equal(int64(1)))
		Expect(ctxMap["db_ms"]).To(BeNumerically(">=", int64(80)))
		Expect(ctxMap["response_bytes"]).To(BeNumerically(">", int64(0)))
	})
})
