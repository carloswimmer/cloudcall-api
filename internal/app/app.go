package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"cloudcall/internal/call"
	"cloudcall/internal/contact"
	"cloudcall/internal/platform/config"
	"cloudcall/internal/platform/db"
	"cloudcall/internal/platform/httpx"
	"cloudcall/internal/user"
)

type App struct {
	mux     *http.ServeMux
	ping    func(context.Context) error
	closeFn func() error
}

// NewLive builds an app with only the liveness route (no database).
func NewLive() *App {
	a := &App{mux: http.NewServeMux()}
	a.registerLive()
	return a
}

// New opens the PostgreSQL pool, runs migrations and the demo seed, and registers
// live and ready routes. Any failure is returned so the process never listens.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	sqlDB, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := db.Seed(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("seed: %w", err)
	}
	users := &user.Store{DB: sqlDB}
	calls := &call.Store{DB: sqlDB, Users: users}
	// Calls left over from a previous process cannot continue; fail them before listening.
	if err := calls.SweepInterrupted(ctx, db.OrganizationID); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("sweep interrupted calls: %w", err)
	}
	a := newApp(sqlDB.PingContext, sqlDB.Close)
	user.NewHandler(users, db.OrganizationID, db.DemoUserID).WithCallLock(calls).Register(a.mux)
	contact.NewHandler(&contact.Store{DB: sqlDB}, db.OrganizationID).Register(a.mux)
	return a, nil
}

func newApp(ping func(context.Context) error, closeFn func() error) *App {
	a := &App{mux: http.NewServeMux(), ping: ping, closeFn: closeFn}
	a.registerLive()
	a.mux.HandleFunc("GET /health/ready", a.handleReady)
	return a
}

func (a *App) registerLive() {
	a.mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "live"})
	})
}

func (a *App) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := a.Ready(r.Context()); err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "internal_error", "database unavailable", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// Ready pings the database pool.
func (a *App) Ready(ctx context.Context) error {
	if a.ping == nil {
		return nil
	}
	return a.ping(ctx)
}

// Shutdown closes the database pool.
func (a *App) Shutdown() error {
	if a.closeFn == nil {
		return nil
	}
	return a.closeFn()
}

func (a *App) Handler() http.Handler { return a.mux }

func (a *App) WithMiddleware(origin string) http.Handler { return httpx.Middleware(origin, a.mux) }
