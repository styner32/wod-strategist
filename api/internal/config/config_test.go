package config_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/config"
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
		setEnv("API_SECRET", "secret-key")
	})

	It("returns config with the default port when PORT is unset", func() {
		setEnv("PORT", "")

		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.DatabaseURL).To(Equal("postgresql://user:pass@localhost:5432/wod_dev?sslmode=disable"))
		Expect(cfg.RedisURL).To(Equal("localhost:6379"))
		Expect(cfg.GCSBucketName).To(Equal("uploads-bucket"))
		Expect(cfg.APISecret).To(Equal("secret-key"))
		Expect(cfg.Port).To(Equal("8080"))
	})

	It("uses an explicit port", func() {
		setEnv("PORT", "9090")

		cfg, err := config.InitServer()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Port).To(Equal("9090"))
	})

	It("returns an error listing all missing required variables", func() {
		setEnv("DATABASE_URL", "")
		setEnv("REDIS_URL", "")
		setEnv("GCS_BUCKET_NAME", "")
		setEnv("API_SECRET", "")

		_, err := config.InitServer()
		Expect(err).To(MatchError("missing required environment variables: DATABASE_URL, REDIS_URL, GCS_BUCKET_NAME, API_SECRET"))
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
})
