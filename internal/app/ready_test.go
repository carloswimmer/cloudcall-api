package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthReadyFailsWithoutDB(t *testing.T) {
	t.Parallel()
	const secret = "postgres://user:secret@db.internal:5432/x"
	a := newApp(func(context.Context) error { return errors.New("dial " + secret) }, nil)
	srv := httptest.NewServer(a.WithMiddleware("http://localhost:4200"))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "internal_error" || body.Message != "database unavailable" {
		t.Fatalf("body %+v", body)
	}
	if strings.Contains(body.Message, "secret") {
		t.Fatal("leaked DSN")
	}
}

func TestShutdownWithCanceledContextStillClosesDB(t *testing.T) {
	t.Parallel()
	closed := false
	a := newApp(func(context.Context) error { return nil }, func() error { closed = true; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Shutdown(ctx); err == nil {
		t.Fatal("expected context.Canceled")
	}
	if !closed {
		t.Fatal("close not called with canceled context")
	}
}

func TestShutdownReturnsContextErrorIfExpiredDuringClose(t *testing.T) {
	t.Parallel()
	closed := false
	a := newApp(
		func(context.Context) error { return nil },
		func() error {
			time.Sleep(50 * time.Millisecond)
			closed = true
			return nil
		},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := a.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if !closed {
		t.Fatal("close not called when context expired during closeFn")
	}
}

func TestReadyAndShutdownDelegate(t *testing.T) {
	t.Parallel()
	closed := false
	a := newApp(func(context.Context) error { return nil }, func() error { closed = true; return nil })
	if err := a.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("close not called")
	}
}
