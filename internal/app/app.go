package app

import (
	"encoding/json"
	"net/http"

	"cloudcall/internal/platform/httpx"
)

type App struct {
	mux *http.ServeMux
}

func NewLive() *App {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "live"})
	})
	return &App{mux: mux}
}

func (a *App) Handler() http.Handler { return a.mux }

func (a *App) WithMiddleware(origin string) http.Handler { return httpx.Middleware(origin, a.mux) }
