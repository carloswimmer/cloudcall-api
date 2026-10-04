package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

type ErrorBody struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
	RequestID   string            `json:"requestId"`
}

const MaxBodyBytes int64 = 16 * 1024

const (
	requestIDHeader = "X-Request-Id"
	allowedMethods  = "GET,POST,PATCH,PUT,DELETE,OPTIONS"
	allowedHeaders  = "Content-Type, X-Request-Id"
)

type requestIDKey struct{}

var logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

// Middleware assigns a request ID, applies CORS for corsOrigin, limits request
// bodies to MaxBodyBytes and logs one JSON line per request.
func Middleware(corsOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		id := r.Header.Get(requestIDHeader)
		if _, err := uuid.Parse(id); err != nil {
			id = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		if origin := r.Header.Get("Origin"); origin != "" && origin == corsOrigin {
			h := sw.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", allowedMethods)
			h.Set("Access-Control-Allow-Headers", allowedHeaders)
			h.Add("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			sw.WriteHeader(http.StatusNoContent)
		} else {
			if r.Method != http.MethodGet {
				r.Body = http.MaxBytesReader(sw, r.Body, MaxBodyBytes)
			}
			next.ServeHTTP(sw, r)
		}

		route := r.Pattern
		if route == "" {
			route = r.URL.Path
		}
		logger.Info("request",
			"requestId", id,
			"method", r.Method,
			"route", route,
			"status", sw.status,
			"durationMs", float64(time.Since(start).Microseconds())/1000,
		)
	})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string, fields map[string]string) {
	WriteJSON(w, status, ErrorBody{
		Code:        code,
		Message:     message,
		FieldErrors: fields,
		RequestID:   RequestID(r),
	})
}

// RequestID returns the ID assigned by Middleware, or "" outside of it.
func RequestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return id
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush keeps streaming responses working through the wrapper.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
