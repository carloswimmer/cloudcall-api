package httpx_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cloudcall/internal/platform/httpx"
)

const origin = "http://localhost:4200"

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "request body too large", nil)
				return
			}
			httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "unexpected error", nil)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"requestId": httpx.RequestID(r), "body": string(body)})
	})
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"requestId": httpx.RequestID(r)})
	})
	return httpx.Middleware(origin, mux)
}

func TestGeneratesRequestID(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	id := rec.Header().Get("X-Request-Id")
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("response X-Request-Id %q is not a UUID: %v", id, err)
	}
	var body struct {
		RequestID string `json:"requestId"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RequestID != id {
		t.Fatalf("RequestID(r)=%q, header=%q", body.RequestID, id)
	}
}

func TestReusesValidRequestID(t *testing.T) {
	t.Parallel()
	want := uuid.NewString()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Request-Id", want)
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReplacesInvalidRequestID(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Request-Id", "not-a-uuid")
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-Id")
	if got == "not-a-uuid" {
		t.Fatal("invalid request id was reused")
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Fatalf("response X-Request-Id %q is not a UUID: %v", got, err)
	}
}

func TestPreflightAllowedOrigin(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodOptions, "/echo", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("allow-origin %q", got)
	}
	headers := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(headers, "Content-Type") || !strings.Contains(headers, "X-Request-Id") {
		t.Fatalf("allow-headers %q", headers)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET,POST,PATCH,PUT,DELETE,OPTIONS" {
		t.Fatalf("allow-methods %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("allow-credentials %q", got)
	}
}

func TestCORSHeadersOnSimpleRequest(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("allow-origin %q", got)
	}
}

func TestDisallowedOriginGetsNoAllowOrigin(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodOptions, http.MethodGet} {
		req := httptest.NewRequest(method, "/ping", nil)
		req.Header.Set("Origin", "http://evil.example")
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s: allow-origin %q", method, got)
		}
	}
}

func TestBodyOverLimitIsRejected(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a", int(httpx.MaxBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(big))
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	var body httpx.ErrorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "validation_error" {
		t.Fatalf("%+v", body)
	}
}

func TestBodyAtLimitIsAccepted(t *testing.T) {
	t.Parallel()
	ok := strings.Repeat("a", int(httpx.MaxBodyBytes))
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(ok))
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestWriteErrorBody(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /bad", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "validation_error", "invalid input", map[string]string{"phone": "required"})
	})
	req := httptest.NewRequest(http.MethodGet, "/bad", nil)
	req.Header.Set("X-Request-Id", id)
	rec := httptest.NewRecorder()
	httpx.Middleware(origin, mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	var body httpx.ErrorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := httpx.ErrorBody{
		Code:        "validation_error",
		Message:     "invalid input",
		FieldErrors: map[string]string{"phone": "required"},
		RequestID:   id,
	}
	if body.Code != want.Code || body.Message != want.Message || body.RequestID != want.RequestID || body.FieldErrors["phone"] != "required" {
		t.Fatalf("%+v", body)
	}
}
