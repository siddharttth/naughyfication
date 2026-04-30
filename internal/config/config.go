package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddress        string
	DatabaseURL        string
	RedisAddress       string
	RedisPassword      string
	RedisDB            int
	RateLimitRPS       int
	RateLimitBurst     int
	WorkerCount        int
	MaxRetries         int
	BaseBackoff        time.Duration
	MaxBackoff         time.Duration
	ProviderTimeout    time.Duration
	WebhookTimeout     time.Duration
	WebhookMaxRetries  int
	DocsBaseURL        string
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPassword       string
	SMTPFrom           string
	AllowMockDelivery  bool
	BootstrapUserEmail string
	BootstrapAPIKey    string
	LogLevel           string
}

func Load() Config {
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbHost := getEnv("DB_HOST", "127.0.0.1")
	dbPort := getEnv("DB_PORT", "5432")
	dbName := getEnv("DB_NAME", "postgres")

	return Config{
		HTTPAddress:        getEnv("HTTP_ADDRESS", ":8080"),
		DatabaseURL:        getEnv("DATABASE_URL", fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, dbHost, dbPort, dbName)),
		RedisAddress:       getEnv("REDIS_ADDRESS", "127.0.0.1:6379"),
		RedisPassword:      os.Getenv("REDIS_PASSWORD"),
		RedisDB:            getEnvInt("REDIS_DB", 0),
		RateLimitRPS:       getEnvInt("RATE_LIMIT_RPS", 10),
		RateLimitBurst:     getEnvInt("RATE_LIMIT_BURST", 20),
		WorkerCount:        getEnvInt("WORKER_COUNT", 4),
		MaxRetries:         getEnvInt("MAX_RETRIES", 3),
		BaseBackoff:        getEnvDuration("BASE_BACKOFF", 2*time.Second),
		MaxBackoff:         getEnvDuration("MAX_BACKOFF", 30*time.Second),
		ProviderTimeout:    getEnvDuration("PROVIDER_TIMEOUT", 10*time.Second),
		WebhookTimeout:     getEnvDuration("WEBHOOK_TIMEOUT", 5*time.Second),
		WebhookMaxRetries:  getEnvInt("WEBHOOK_MAX_RETRIES", 3),
		DocsBaseURL:        getEnv("DOCS_BASE_URL", "http://localhost:8080"),
		SMTPHost:           os.Getenv("SMTP_HOST"),
		SMTPPort:           getEnvInt("SMTP_PORT", 587),
		SMTPUsername:       os.Getenv("SMTP_USERNAME"),
		SMTPPassword:       os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:           getEnv("SMTP_FROM", "no-reply@naughtyfication.local"),
		AllowMockDelivery:  getEnvBool("ALLOW_MOCK_DELIVERY", true),
		BootstrapUserEmail: getEnv("BOOTSTRAP_USER_EMAIL", "dev@naughtyfication.local"),
		BootstrapAPIKey:    getEnv("API_KEY", "dev-secret-key"),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
