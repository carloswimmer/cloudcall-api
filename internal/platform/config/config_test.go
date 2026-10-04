package config_test

import (
	"testing"

	"cloudcall/internal/platform/config"
)

func TestLoadDefaults(t *testing.T) {
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
	t.Setenv("DATABASE_URL", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsDemoModeFalse(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("DEMO_MODE", "false")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsInvalidDemoMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("DEMO_MODE", "yes")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadAcceptsDemoModeTrue(t *testing.T) {
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
