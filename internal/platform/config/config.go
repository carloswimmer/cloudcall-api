package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	CORSOrigin  string
	DemoMode    bool
	LogLevel    string
}

func Load() (Config, error) {
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
	if v, ok := os.LookupEnv("DEMO_MODE"); ok && strings.TrimSpace(v) != "" {
		if strings.EqualFold(strings.TrimSpace(v), "true") {
			cfg.DemoMode = true
		} else {
			return Config{}, fmt.Errorf("DEMO_MODE must be true in this MVP")
		}
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
