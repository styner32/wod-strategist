package config_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/config"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
)

func setEnv(name string, value string) {
	original, wasSet := os.LookupEnv(name)
	DeferCleanup(func() {
		if !wasSet {
			_ = os.Unsetenv(name)
			return
		}
		_ = os.Setenv(name, original)
	})

	Expect(os.Setenv(name, value)).To(Succeed())
}

var _ = Describe("InitServer", func() {
	BeforeEach(func() {
		setEnv("DATABASE_URL", "postgresql://user:pass@localhost:5432/wod_dev?sslmode=disable")
		setEnv("REDIS_URL", "localhost:6379")
		setEnv("GCS_BUCKET_NAME", "uploads-bucket")
		setEnv("JWT_SIGNING_SECRET", "test-jwt-siging-secret")
	})

	It("snapshots the deep-analysis model for manual recognition diagnoses", func() {
		setEnv("GEMINI_MODEL", "")
		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.GeminiModel).To(Equal(gemini.ModelCarbon4))

		setEnv("GEMINI_MODEL", "  gemini-4-hydrogen  ")
		cfg, err = config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.GeminiModel).To(Equal(gemini.ModelHydrogen4))
	})

	It("keeps Agentic highlights disabled by default and enables only an explicit true value", func() {
		setEnv("ENABLE_AGENTIC_HIGHLIGHTS", "")
		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.AgenticHighlightsEnabled).To(BeFalse())
		setEnv("ENABLE_AGENTIC_HIGHLIGHTS", "true")
		cfg, err = config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.AgenticHighlightsEnabled).To(BeTrue())
	})
	It("keeps GEMINI_INCLUDE_THOUGHTS disabled by default and enables an explicit true value", func() {
		setEnv("GEMINI_INCLUDE_THOUGHTS", "")
		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.GeminiIncludeThoughts).To(BeFalse())
		setEnv("GEMINI_INCLUDE_THOUGHTS", "TrUe")
		cfg, err = config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.GeminiIncludeThoughts).To(BeTrue())
	})
	It("returns config with the default port when PORT is unset", func() {
		setEnv("PORT", "")

		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.DatabaseURL).To(Equal("postgresql://user:pass@localhost:5432/wod_dev?sslmode=disable"))
		Expect(cfg.RedisURL).To(Equal("localhost:6379"))
		Expect(cfg.GCSBucketName).To(Equal("uploads-bucket"))
		Expect(cfg.Port).To(Equal("8080"))
		Expect(cfg.DevAllowedOrigins).To(ContainElements("http://localhost:3000", "http://127.0.0.1:3000"))
	})

	It("uses an explicit port", func() {
		setEnv("PORT", "9090")

		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Port).To(Equal("9090"))
	})

	It("keeps chunk re-analysis disabled unless explicitly enabled", func() {
		setEnv("ENABLE_CHUNK_REANALYSIS", "")

		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ChunkReanalysisEnabled).To(BeFalse())
	})

	It("enables chunk re-analysis from an explicit true value", func() {
		setEnv("ENABLE_CHUNK_REANALYSIS", "TrUe")

		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ChunkReanalysisEnabled).To(BeTrue())
	})

	It("keeps session re-analysis disabled unless explicitly enabled", func() {
		setEnv("ENABLE_SESSION_REANALYSIS", "")

		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.SessionReanalysisEnabled).To(BeFalse())
	})

	It("enables session re-analysis from an explicit true value", func() {
		setEnv("ENABLE_SESSION_REANALYSIS", "TrUe")

		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.SessionReanalysisEnabled).To(BeTrue())
	})

	It("defaults HISTORY_SUMMARY_CACHE_ENABLED to true and disables only on explicit false", func() {
		setEnv("HISTORY_SUMMARY_CACHE_ENABLED", "")
		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.HistorySummaryCacheEnabled).To(BeTrue())

		setEnv("HISTORY_SUMMARY_CACHE_ENABLED", " FaLsE ")
		cfgDisabled, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfgDisabled.HistorySummaryCacheEnabled).To(BeFalse())
	})

	It("leaves the session re-analysis quota to the controller default when unset and parses an explicit value", func() {
		setEnv("SESSION_REANALYSIS_DAILY_QUOTA", "")
		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.SessionReanalysisDailyQuota).To(BeZero())

		setEnv("SESSION_REANALYSIS_DAILY_QUOTA", " 7 ")
		cfg, err = config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.SessionReanalysisDailyQuota).To(Equal(int64(7)))
	})

	DescribeTable("rejects an invalid session re-analysis quota at startup",
		func(value string) {
			setEnv("SESSION_REANALYSIS_DAILY_QUOTA", value)
			_, err := config.InitServer()
			Expect(err).To(MatchError(ContainSubstring("SESSION_REANALYSIS_DAILY_QUOTA must be a positive integer")))
		},
		Entry("non-numeric", "20x"),
		Entry("zero", "0"),
		Entry("negative", "-3"),
	)

	It("defaults server DB_MAX_OPEN_CONNS to 6 and DB_MAX_IDLE_CONNS to 2 and accepts explicit positive integers", func() {
		setEnv("DB_MAX_OPEN_CONNS", "")
		setEnv("DB_MAX_IDLE_CONNS", "")
		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.DBMaxOpenConns).To(Equal(db.DefaultServerMaxOpenConns))
		Expect(cfg.DBMaxIdleConns).To(Equal(db.DefaultMaxIdleConns))

		setEnv("DB_MAX_OPEN_CONNS", " 14 ")
		setEnv("DB_MAX_IDLE_CONNS", " 4 ")
		cfg, err = config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.DBMaxOpenConns).To(Equal(14))
		Expect(cfg.DBMaxIdleConns).To(Equal(4))
	})

	DescribeTable("rejects invalid server DB pool values at startup",
		func(envName, value string) {
			setEnv(envName, value)
			_, err := config.InitServer()
			Expect(err).To(MatchError(ContainSubstring(envName + " must be a positive integer")))
		},
		Entry("non-numeric open", "DB_MAX_OPEN_CONNS", "abc"),
		Entry("zero open", "DB_MAX_OPEN_CONNS", "0"),
		Entry("negative open", "DB_MAX_OPEN_CONNS", "-1"),
		Entry("non-numeric idle", "DB_MAX_IDLE_CONNS", "abc"),
		Entry("zero idle", "DB_MAX_IDLE_CONNS", "0"),
		Entry("negative idle", "DB_MAX_IDLE_CONNS", "-1"),
	)

	It("returns an error listing all missing required variables", func() {
		setEnv("DATABASE_URL", "")
		setEnv("REDIS_URL", "")
		setEnv("GCS_BUCKET_NAME", "")
		setEnv("JWT_SIGNING_SECRET", "")

		_, err := config.InitServer()
		Expect(err).To(MatchError("missing required environment variables: DATABASE_URL, REDIS_URL, GCS_BUCKET_NAME, JWT_SIGNING_SECRET"))
	})

	Context("value validation", func() {
		It("rejects a non-numeric PORT", func() {
			setEnv("PORT", "banana")

			_, err := config.InitServer()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PORT must be a number"))
		})

		It("rejects PORT 0", func() {
			setEnv("PORT", "0")

			_, err := config.InitServer()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PORT must be between 1 and 65535"))
		})

		It("rejects PORT above 65535", func() {
			setEnv("PORT", "70000")

			_, err := config.InitServer()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PORT must be between 1 and 65535"))
		})

		It("rejects a DATABASE_URL without postgres prefix", func() {
			setEnv("DATABASE_URL", "mysql://localhost/db")

			_, err := config.InitServer()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("DATABASE_URL must start with postgres:// or postgresql://"))
		})

		It("accepts DATABASE_URL with postgres:// prefix", func() {
			setEnv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")

			cfg, err := config.InitServer()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.DatabaseURL).To(Equal("postgres://user:pass@localhost:5432/db"))
		})

		It("rejects a REDIS_URL without host:port format", func() {
			setEnv("REDIS_URL", "just-a-host")

			_, err := config.InitServer()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("REDIS_URL must be in host:port format"))
		})

		It("collects multiple validation errors", func() {
			setEnv("PORT", "abc")
			setEnv("DATABASE_URL", "mysql://localhost/db")
			setEnv("REDIS_URL", "bad")

			_, err := config.InitServer()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PORT must be a number"))
			Expect(err.Error()).To(ContainSubstring("DATABASE_URL must start with"))
			Expect(err.Error()).To(ContainSubstring("REDIS_URL must be in host:port"))
		})
	})

	Context("AppEnv", func() {
		It("defaults to production when APP_ENV is unset", func() {
			setEnv("APP_ENV", "")

			cfg, err := config.InitServer()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.AppEnv).To(Equal("production"))
		})

		It("uses the value of APP_ENV when set", func() {
			setEnv("APP_ENV", "development")

			cfg, err := config.InitServer()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.AppEnv).To(Equal("development"))
		})

		It("parses explicit development CORS origins", func() {
			setEnv("DEV_ALLOWED_ORIGINS", "http://localhost:3000, https://qa.example.com ")

			cfg, err := config.InitServer()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.DevAllowedOrigins).To(Equal([]string{"http://localhost:3000", "https://qa.example.com"}))
		})
	})
})

var _ = Describe("InitWorker", func() {
	BeforeEach(func() {
		setEnv("DATABASE_URL", "postgresql://user:pass@localhost:5432/wod_dev?sslmode=disable")
		setEnv("REDIS_URL", "localhost:6379")
		setEnv("GCS_BUCKET_NAME", "uploads-bucket")
		setEnv("GEMINI_API_KEY", "gemini-key")
	})

	It("returns the validated worker config", func() {
		cfg, err := config.InitWorker()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.DatabaseURL).To(Equal("postgresql://user:pass@localhost:5432/wod_dev?sslmode=disable"))
		Expect(cfg.RedisURL).To(Equal("localhost:6379"))
		Expect(cfg.GCSBucketName).To(Equal("uploads-bucket"))
		Expect(cfg.GeminiAPIKey).To(Equal("gemini-key"))
	})

	It("returns an error when a required worker variable is missing", func() {
		setEnv("GEMINI_API_KEY", "")

		_, err := config.InitWorker()
		Expect(err).To(MatchError("missing required environment variables: GEMINI_API_KEY"))
	})

	Context("value validation", func() {
		It("rejects an invalid DATABASE_URL", func() {
			setEnv("DATABASE_URL", "sqlite:///tmp/test.db")

			_, err := config.InitWorker()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("DATABASE_URL must start with postgres:// or postgresql://"))
		})

		It("rejects an invalid REDIS_URL", func() {
			setEnv("REDIS_URL", "no-port")

			_, err := config.InitWorker()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("REDIS_URL must be in host:port format"))
		})
	})

	Context("AppEnv", func() {
		It("defaults to production", func() {
			setEnv("APP_ENV", "")

			cfg, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.AppEnv).To(Equal("production"))
		})
	})

	Context("live feedback feature switches", func() {
		It("defaults all new features off and enables each independently", func() {
			setEnv("ENABLE_CAPTURE_FEEDBACK", "")
			setEnv("ENABLE_CONTEXTUAL_COACHING", "")
			setEnv("ENABLE_ACTIVITY_COUNTING", "")
			cfg, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CaptureFeedbackEnabled).To(BeFalse())
			Expect(cfg.ContextualCoachingEnabled).To(BeFalse())
			Expect(cfg.ActivityCountingEnabled).To(BeFalse())
			setEnv("ENABLE_ACTIVITY_COUNTING", "true")
			cfg, err = config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.ActivityCountingEnabled).To(BeTrue())
			Expect(cfg.CaptureFeedbackEnabled).To(BeFalse())
			Expect(cfg.ContextualCoachingEnabled).To(BeFalse())
		})

		It("enables tracking continuity by default and honours explicit values", func() {
			setEnv("ENABLE_TRACKING_CONTINUITY", "")
			setEnv("APP_ENV", "production")
			cfg, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.TrackingContinuityEnabled).To(BeTrue())
			setEnv("ENABLE_TRACKING_CONTINUITY", "false")
			cfg, err = config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.TrackingContinuityEnabled).To(BeFalse())
			setEnv("ENABLE_TRACKING_CONTINUITY", "TrUe")
			cfg, err = config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.TrackingContinuityEnabled).To(BeTrue())
		})
	})

	Context("GeminiModel and ThinkingConfig", func() {
		It("defaults to ModelCarbon4, HIGH thinking level, and LOW chunk thinking level", func() {
			setEnv("GEMINI_MODEL", "")
			setEnv("GEMINI_THINKING_LEVEL", "")
			setEnv("GEMINI_THINKING_CHUNK", "")
			setEnv("GEMINI_THINKING_BUDGET", "")
			setEnv("GEMINI_INCLUDE_THOUGHTS", "")

			cfg, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.GeminiModel).To(Equal(gemini.ModelCarbon4))
			Expect(cfg.GeminiThinkingLevel).To(Equal("HIGH"))
			Expect(cfg.GeminiThinkingChunk).To(Equal("LOW"))
			Expect(cfg.GeminiThinkingBudget).To(BeNil())
			Expect(cfg.GeminiIncludeThoughts).To(BeFalse())

			setEnv("GEMINI_INCLUDE_THOUGHTS", "true")
			cfgWithThoughts, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgWithThoughts.GeminiIncludeThoughts).To(BeTrue())
		})

		It("accepts custom model, thinking level, chunk thinking level, and thinking budget", func() {
			setEnv("GEMINI_MODEL", "gemini-3.1-pro-preview")
			setEnv("GEMINI_THINKING_LEVEL", "MEDIUM")
			setEnv("GEMINI_THINKING_CHUNK", "MEDIUM")
			setEnv("GEMINI_THINKING_BUDGET", "2048")

			cfg, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.GeminiModel).To(Equal(gemini.ModelPro31Preview))
			Expect(cfg.GeminiThinkingLevel).To(Equal("MEDIUM"))
			Expect(cfg.GeminiThinkingChunk).To(Equal("MEDIUM"))
			Expect(cfg.GeminiThinkingBudget).NotTo(BeNil())
			Expect(*cfg.GeminiThinkingBudget).To(Equal(int32(2048)))
		})

		It("rejects unsupported MINIMAL thinking level", func() {
			setEnv("GEMINI_THINKING_CHUNK", "MINIMAL")
			_, err := config.InitWorker()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not supported by Gemini 4 Hydrogen"))
		})

		It("rejects invalid thinking level strings", func() {
			setEnv("GEMINI_THINKING_LEVEL", "SUPER_HIGH")
			_, err := config.InitWorker()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must be one of HIGH, MEDIUM, LOW"))
		})

		It("accepts thinking budget of 0 (disable thinking) and -1 (dynamic thinking)", func() {
			setEnv("GEMINI_THINKING_BUDGET", "0")
			cfg0, err0 := config.InitWorker()
			Expect(err0).NotTo(HaveOccurred())
			Expect(cfg0.GeminiThinkingBudget).NotTo(BeNil())
			Expect(*cfg0.GeminiThinkingBudget).To(Equal(int32(0)))

			setEnv("GEMINI_THINKING_BUDGET", "-1")
			cfgDyn, errDyn := config.InitWorker()
			Expect(errDyn).NotTo(HaveOccurred())
			Expect(cfgDyn.GeminiThinkingBudget).NotTo(BeNil())
			Expect(*cfgDyn.GeminiThinkingBudget).To(Equal(int32(-1)))
		})

		It("defaults GEMINI_CHUNK_FPS to 2.0 and accepts explicit 0 and fractional values within [1, 24]", func() {
			setEnv("GEMINI_CHUNK_FPS", "")
			cfgDefault, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgDefault.GeminiChunkFPS).To(Equal(2.0))

			setEnv("GEMINI_CHUNK_FPS", "0")
			cfgZero, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgZero.GeminiChunkFPS).To(Equal(0.0))

			setEnv("GEMINI_CHUNK_FPS", " 3.5 ")
			cfgCustom, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgCustom.GeminiChunkFPS).To(Equal(3.5))
		})

		DescribeTable("rejects invalid GEMINI_CHUNK_FPS values at startup",
			func(value string) {
				setEnv("GEMINI_CHUNK_FPS", value)
				_, err := config.InitWorker()
				Expect(err).To(MatchError(ContainSubstring("GEMINI_CHUNK_FPS must be 0 or between 1 and 24")))
			},
			Entry("negative", "-1"),
			Entry("below 1", "0.5"),
			Entry("above 24", "25"),
			Entry("non-numeric", "bad"),
		)

		It("defaults WORKER_CONCURRENCY to 5, DB_MAX_OPEN_CONNS to 6, and DB_MAX_IDLE_CONNS to 2", func() {
			setEnv("WORKER_CONCURRENCY", "")
			setEnv("DB_MAX_OPEN_CONNS", "")
			setEnv("DB_MAX_IDLE_CONNS", "")
			cfg, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Concurrency).To(Equal(5))
			Expect(cfg.DBMaxOpenConns).To(Equal(db.DefaultWorkerMaxOpenConns))
			Expect(cfg.DBMaxIdleConns).To(Equal(db.DefaultMaxIdleConns))

			setEnv("WORKER_CONCURRENCY", " 4 ")
			setEnv("DB_MAX_OPEN_CONNS", " 8 ")
			setEnv("DB_MAX_IDLE_CONNS", " 3 ")
			cfgCustom, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgCustom.Concurrency).To(Equal(4))
			Expect(cfgCustom.DBMaxOpenConns).To(Equal(8))
			Expect(cfgCustom.DBMaxIdleConns).To(Equal(3))
		})

		DescribeTable("rejects invalid worker concurrency or DB pool values at startup",
			func(envName, value string) {
				setEnv(envName, value)
				_, err := config.InitWorker()
				Expect(err).To(MatchError(ContainSubstring(envName + " must be a positive integer")))
			},
			Entry("zero concurrency", "WORKER_CONCURRENCY", "0"),
			Entry("negative concurrency", "WORKER_CONCURRENCY", "-2"),
			Entry("non-numeric concurrency", "WORKER_CONCURRENCY", "five"),
			Entry("zero db pool", "DB_MAX_OPEN_CONNS", "0"),
			Entry("negative db pool", "DB_MAX_OPEN_CONNS", "-4"),
			Entry("zero db idle pool", "DB_MAX_IDLE_CONNS", "0"),
			Entry("negative db idle pool", "DB_MAX_IDLE_CONNS", "-2"),
		)

		It("defaults worker rollback switches to true and disables each only on explicit false", func() {
			setEnv("ACTIVITY_REVIEW_WINDOW_RETRY_ENABLED", "")
			setEnv("PASS2_REST_FALLBACK_SPLIT_ENABLED", "")
			setEnv("WORKER_MEDIA_CPU_SLOTS_ENABLED", "")
			cfg, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.ActivityReviewWindowRetryEnabled).To(BeTrue())
			Expect(cfg.Pass2RestFallbackSplitEnabled).To(BeTrue())
			Expect(cfg.MediaCPUSlotsEnabled).To(BeTrue())

			setEnv("ACTIVITY_REVIEW_WINDOW_RETRY_ENABLED", "false")
			setEnv("PASS2_REST_FALLBACK_SPLIT_ENABLED", " FALSE ")
			setEnv("WORKER_MEDIA_CPU_SLOTS_ENABLED", "False")
			cfgDisabled, err := config.InitWorker()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgDisabled.ActivityReviewWindowRetryEnabled).To(BeFalse())
			Expect(cfgDisabled.Pass2RestFallbackSplitEnabled).To(BeFalse())
			Expect(cfgDisabled.MediaCPUSlotsEnabled).To(BeFalse())
		})
	})
})
