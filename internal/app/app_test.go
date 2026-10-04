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
