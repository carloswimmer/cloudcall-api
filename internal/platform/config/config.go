package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	CORSOrigin  string
	DemoMode    bool
	LogLevel    string
}

func Load() (Config, error) {
	loadDotEnv()
	cfg := Config{
		HTTPAddr:    envOr("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		CORSOrigin:  envOr("CORS_ORIGIN", "http://localhost:4200"),
		LogLevel:    envOr("LOG_LEVEL", "info"),
		DemoMode:    true,
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	cfg.LogLevel = strings.ToLower(cfg.LogLevel)
	if _, err := ParseLogLevel(cfg.LogLevel); err != nil {
		return Config{}, err
	}
	if v, ok := os.LookupEnv("DEMO_MODE"); ok && strings.TrimSpace(v) != "" {
		if strings.EqualFold(strings.TrimSpace(v), "true") {
			cfg.DemoMode = true
		} else {
			return Config{}, fmt.Errorf("DEMO_MODE must be true in this MVP")
		}
	}
	return cfg, nil
}

// ParseLogLevel maps debug, info, warn and error (case-insensitive) to a slog level.
func ParseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error")
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv loads optional KEY=value pairs from a .env file into the process
// environment. Non-empty variables already set in the process are left unchanged
// so production and shell exports keep precedence. Missing files are ignored.
func loadDotEnv() {
	path := strings.TrimSpace(os.Getenv("ENV_FILE"))
	if path == "" {
		path = ".env"
	}
	envMap, err := godotenv.Read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		return
	}
	for key, value := range envMap {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			_ = os.Setenv(key, value)
		}
	}
}
