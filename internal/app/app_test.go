package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"cloudcall/internal/app"
	"cloudcall/internal/platform/config"
	"cloudcall/internal/platform/db"
)

func TestHealthLive(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(app.NewLive().Handler())
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/health/live")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "live" {
		t.Fatalf("status %q", body.Status)
	}
}

func TestWithMiddlewareAddsRequestIDAndCORS(t *testing.T) {
	t.Parallel()
	const origin = "http://localhost:4200"
	srv := httptest.NewServer(app.NewLive().WithMiddleware(origin))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/health/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", origin)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("missing X-Request-Id")
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("allow-origin %q", got)
	}
}

func TestHealthReadyOK(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	a, err := app.New(context.Background(), config.Config{DatabaseURL: url, DemoMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown() })
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ready" {
		t.Fatalf("status %q", body.Status)
	}
}

func TestNewMigratesAndSeeds(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	a, err := app.New(ctx, config.Config{DatabaseURL: url, DemoMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown() })

	sqlDB, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var n int
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE organization_id = $1", db.OrganizationID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("users %d", n)
	}
}

func TestNewFailsWhenDatabaseUnreachable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, err := app.New(ctx, config.Config{DatabaseURL: "postgres://x:y@127.0.0.1:1/z?sslmode=disable&connect_timeout=1", DemoMode: true})
	if err == nil {
		_ = a.Shutdown()
		t.Fatal("expected error")
	}
}

func TestNewMountsUserRoutes(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	a, err := app.New(context.Background(), config.Config{DatabaseURL: url, DemoMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown() })
	srv := httptest.NewServer(a.WithMiddleware("http://localhost:4200"))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/api/v1/users")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
}
