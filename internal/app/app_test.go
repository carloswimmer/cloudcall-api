package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloudcall/internal/app"
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
