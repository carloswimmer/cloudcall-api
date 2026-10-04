package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"cloudcall/internal/call"
	"cloudcall/internal/contact"
	"cloudcall/internal/dashboard"
	"cloudcall/internal/event"
	"cloudcall/internal/platform/config"
	"cloudcall/internal/platform/db"
	"cloudcall/internal/platform/httpx"
	"cloudcall/internal/user"
)

type App struct {
	mux     *http.ServeMux
	ping    func(context.Context) error
	closeFn func() error
	hub     *event.Hub
}

// heartbeatInterval is how often an idle SSE stream receives a heartbeat.
const heartbeatInterval = 15 * time.Second

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
	hub := event.NewHub(snapshotLoader{
		calls: calls, users: users, orgID: db.OrganizationID, ownerID: db.DemoUserID, now: time.Now,
	}, heartbeatInterval, time.Now)
	users.Publisher, calls.Publisher = hub, hub
	// Calls left over from a previous process cannot continue; fail them before listening.
	if err := calls.SweepInterrupted(ctx, db.OrganizationID); err != nil {
		hub.Close()
		_ = sqlDB.Close()
		return nil, fmt.Errorf("sweep interrupted calls: %w", err)
	}
	a := newApp(sqlDB.PingContext, sqlDB.Close)
	a.hub = hub
	event.NewHandler(hub).Register(a.mux)
	user.NewHandler(users, db.OrganizationID, db.DemoUserID).WithCallLock(calls).Register(a.mux)
	contacts := &contact.Store{DB: sqlDB}
	contact.NewHandler(contacts, db.OrganizationID).Register(a.mux)
	callHandler := call.NewHandler(calls, contacts, db.OrganizationID, db.DemoUserID)
	callHandler.Register(a.mux)
	dashboard.NewHandler(sqlDB, db.OrganizationID, db.DemoUserID).Register(a.mux)
	if cfg.DemoMode {
		callHandler.RegisterDemo(a.mux)
	}
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

// Shutdown closes the SSE hub (cancelling every stream) and then the database pool.
// Both steps always run in that order; ctx cancellation does not skip db.Close().
func (a *App) Shutdown(ctx context.Context) error {
	var errs []error
	ctxErr := ctx.Err()
	if ctxErr != nil {
		errs = append(errs, ctxErr)
	}

	if a.hub != nil {
		done := make(chan struct{})
		go func() {
			a.hub.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			<-done
		}
	}

	if a.closeFn != nil {
		done := make(chan error, 1)
		go func() { done <- a.closeFn() }()
		select {
		case err := <-done:
			if err != nil {
				errs = append(errs, err)
			}
		case <-ctx.Done():
			if err := <-done; err != nil {
				errs = append(errs, err)
			}
		}
	}

	if err := ctx.Err(); err != nil && !errors.Is(ctxErr, err) {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (a *App) Handler() http.Handler { return a.mux }

func (a *App) WithMiddleware(origin string) http.Handler { return httpx.Middleware(origin, a.mux) }

// NewServer builds the HTTP server for this app. There is no global WriteTimeout
// (it would kill SSE streams). http.Server.Shutdown waits for active handlers, and
// an SSE handler only returns when its stream ends, so the hub is closed as soon
// as Shutdown starts; otherwise every open stream would hold Shutdown until its
// deadline.
func (a *App) NewServer(addr, corsOrigin string) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           a.WithMiddleware(corsOrigin),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if a.hub != nil {
		srv.RegisterOnShutdown(a.hub.Close)
	}
	return srv
}
