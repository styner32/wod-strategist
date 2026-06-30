package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	"github.com/wod-strategist/api/internal/gemini"
)

const defaultPort = "8080"

var defaultDevAllowedOrigins = []string{
	"http://localhost:3000",
	"http://127.0.0.1:3000",
}

type Common struct {
	DatabaseURL   string
	RedisURL      string
	GCSBucketName string
	AppEnv        string
}

type Server struct {
	Common
	APISecret         string
	JWTSigningSecret  string
	GeminiAPIKey      string // GEMINI_API_KEY — optional; enables /parse-workout-image endpoint
	Port              string
	DevAllowedOrigins []string
	WebOrigin         string // WEB_ORIGIN — production web app origin (e.g. https://app.wod-strategist.com)
	CookieDomain      string // COOKIE_DOMAIN — domain for auth cookie (e.g. .wod-strategist.com); empty = origin-only
	CookieSecure      bool   // COOKIE_SECURE — set Secure flag on cookie; default true in prod, false for local dev
}

type Worker struct {
	Common
	GeminiAPIKey string
	GeminiModel  string // GEMINI_MODEL — default "gemini-3.1-pro-preview"
	UseCache     bool   // GEMINI_USE_CACHE — enable context caching for long videos
}

var loadEnvOnce sync.Once

func InitServer() (Server, error) {
	loadEnvFile()

	appEnv := strings.TrimSpace(os.Getenv("APP_ENV"))
	if appEnv == "" {
		appEnv = "production"
	}

	cfg := Server{
		Common: Common{
			DatabaseURL:   strings.TrimSpace(os.Getenv("DATABASE_URL")),
			RedisURL:      strings.TrimSpace(os.Getenv("REDIS_URL")),
			GCSBucketName: strings.TrimSpace(os.Getenv("GCS_BUCKET_NAME")),
			AppEnv:        appEnv,
		},
		APISecret:         strings.TrimSpace(os.Getenv("API_SECRET")),
		JWTSigningSecret:  strings.TrimSpace(os.Getenv("JWT_SIGNING_SECRET")),
		GeminiAPIKey:      strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
		Port:              strings.TrimSpace(os.Getenv("PORT")),
		DevAllowedOrigins: parseListEnv("DEV_ALLOWED_ORIGINS", defaultDevAllowedOrigins),
		WebOrigin:         strings.TrimSpace(os.Getenv("WEB_ORIGIN")),
		CookieDomain:      strings.TrimSpace(os.Getenv("COOKIE_DOMAIN")),
		CookieSecure:      !strings.EqualFold(strings.TrimSpace(os.Getenv("COOKIE_SECURE")), "false"),
	}
	// Merge web origin into the CORS allowlist so the web SPA is always permitted.
	if cfg.WebOrigin != "" {
		cfg.DevAllowedOrigins = append(cfg.DevAllowedOrigins, cfg.WebOrigin)
	}
	if cfg.Port == "" {
		cfg.Port = defaultPort
	}

	if err := validateRequired(
		requiredEnv("DATABASE_URL", cfg.DatabaseURL),
		requiredEnv("REDIS_URL", cfg.RedisURL),
		requiredEnv("GCS_BUCKET_NAME", cfg.GCSBucketName),
		requiredEnv("API_SECRET", cfg.APISecret),
		requiredEnv("JWT_SIGNING_SECRET", cfg.JWTSigningSecret),
	); err != nil {
		return Server{}, err
	}

	if err := validateValues(
		validatePort(cfg.Port),
		validateDatabaseURL(cfg.DatabaseURL),
		validateRedisURL(cfg.RedisURL),
	); err != nil {
		return Server{}, err
	}

	return cfg, nil
}

func InitWorker() (Worker, error) {
	loadEnvFile()

	appEnv := strings.TrimSpace(os.Getenv("APP_ENV"))
	if appEnv == "" {
		appEnv = "production"
	}

	cfg := Worker{
		Common: Common{
			DatabaseURL:   strings.TrimSpace(os.Getenv("DATABASE_URL")),
			RedisURL:      strings.TrimSpace(os.Getenv("REDIS_URL")),
			GCSBucketName: strings.TrimSpace(os.Getenv("GCS_BUCKET_NAME")),
			AppEnv:        appEnv,
		},
		GeminiAPIKey: strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
		GeminiModel:  strings.TrimSpace(os.Getenv("GEMINI_MODEL")),
		UseCache:     strings.EqualFold(strings.TrimSpace(os.Getenv("GEMINI_USE_CACHE")), "true"),
	}
	if cfg.GeminiModel == "" {
		cfg.GeminiModel = gemini.ModelPro31Preview
	}

	if err := validateRequired(
		requiredEnv("DATABASE_URL", cfg.DatabaseURL),
		requiredEnv("REDIS_URL", cfg.RedisURL),
		requiredEnv("GCS_BUCKET_NAME", cfg.GCSBucketName),
		requiredEnv("GEMINI_API_KEY", cfg.GeminiAPIKey),
	); err != nil {
		return Worker{}, err
	}

	if err := validateValues(
		validateDatabaseURL(cfg.DatabaseURL),
		validateRedisURL(cfg.RedisURL),
	); err != nil {
		return Worker{}, err
	}

	return cfg, nil
}

// --- presence validation ---

type required struct {
	name  string
	value string
}

func requiredEnv(name, value string) required {
	return required{name: name, value: value}
}

func validateRequired(requiredEnvs ...required) error {
	missing := make([]string, 0, len(requiredEnvs))
	for _, env := range requiredEnvs {
		if strings.TrimSpace(env.value) == "" {
			missing = append(missing, env.name)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
}

// --- value validation ---

func validateValues(errs ...error) error {
	var problems []string
	for _, err := range errs {
		if err != nil {
			problems = append(problems, err.Error())
		}
	}

	if len(problems) == 0 {
		return nil
	}

	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
}

func validatePort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("PORT must be a number, got %q", port)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535, got %d", n)
	}
	return nil
}

func validateDatabaseURL(url string) error {
	if !strings.HasPrefix(url, "postgres://") && !strings.HasPrefix(url, "postgresql://") {
		return fmt.Errorf("DATABASE_URL must start with postgres:// or postgresql://, got %q", url)
	}
	return nil
}

func validateRedisURL(url string) error {
	host, port, err := net.SplitHostPort(url)
	if err != nil {
		return fmt.Errorf("REDIS_URL must be in host:port format, got %q", url)
	}
	if host == "" {
		return fmt.Errorf("REDIS_URL host must not be empty")
	}
	if port == "" {
		return fmt.Errorf("REDIS_URL port must not be empty")
	}
	return nil
}

// --- env file loading ---

func loadEnvFile() {
	loadEnvOnce.Do(func() {
		_ = godotenv.Load()
	})
}

func parseListEnv(name string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return append([]string(nil), fallback...)
	}

	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}

	if len(values) == 0 {
		return append([]string(nil), fallback...)
	}

	return values
}
