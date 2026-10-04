package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"cloudcall/internal/platform/config"
)

func disableDotEnvFile(t *testing.T) {
	t.Helper()
	t.Setenv("ENV_FILE", filepath.Join(t.TempDir(), ".env"))
}

func TestLoadDefaults(t *testing.T) {
	disableDotEnvFile(t)
	t.Setenv("DATABASE_URL", "postgres://cloudcall:cloudcall@localhost:5432/cloudcall?sslmode=disable")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("CORS_ORIGIN", "")
	t.Setenv("DEMO_MODE", "")
	t.Setenv("LOG_LEVEL", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.CORSOrigin != "http://localhost:4200" || !cfg.DemoMode || cfg.LogLevel != "info" {
		t.Fatalf("%+v", cfg)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	disableDotEnvFile(t)
	t.Setenv("DATABASE_URL", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsDemoModeFalse(t *testing.T) {
	disableDotEnvFile(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("DEMO_MODE", "false")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsInvalidDemoMode(t *testing.T) {
	disableDotEnvFile(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("DEMO_MODE", "yes")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadAcceptsDemoModeTrue(t *testing.T) {
	disableDotEnvFile(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("DEMO_MODE", "true")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DemoMode {
		t.Fatalf("%+v", cfg)
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	disableDotEnvFile(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("LOG_LEVEL", "verbose")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadFromDotEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte(
		"DATABASE_URL=postgres://from-dotenv@localhost:5432/cloudcall?sslmode=disable\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENV_FILE", envPath)
	t.Setenv("DATABASE_URL", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://from-dotenv@localhost:5432/cloudcall?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadDotEnvDoesNotOverrideExistingEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte(
		"DATABASE_URL=postgres://from-dotenv@localhost:5432/cloudcall?sslmode=disable\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENV_FILE", envPath)
	t.Setenv("DATABASE_URL", "postgres://from-shell@localhost:5432/cloudcall?sslmode=disable")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://from-shell@localhost:5432/cloudcall?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadAcceptsLogLevels(t *testing.T) {
	want := map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "ERROR": slog.LevelError,
	}
	for in, lvl := range want {
		disableDotEnvFile(t)
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("LOG_LEVEL", in)
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		got, err := config.ParseLogLevel(cfg.LogLevel)
		if err != nil || got != lvl {
			t.Fatalf("%s: got %v err %v, want %v", in, got, err, lvl)
		}
	}
}
