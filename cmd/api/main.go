package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"cloudcall/internal/app"
	"cloudcall/internal/platform/config"
)

// shutdownTimeout bounds the whole graceful shutdown (HTTP drain plus closing the app).
const shutdownTimeout = 10 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	application, err := app.New(startCtx, cfg)
	startCancel()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpServer := application.NewServer(cfg.HTTPAddr, cfg.CORSOrigin)
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", cfg.HTTPAddr)
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		// The server stopped on its own (for example the port is taken).
		_ = application.Shutdown(context.Background())
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
		return
	case <-ctx.Done():
	}

	stop() // a second signal now kills the process the default way
	log.Print("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	if err := application.Shutdown(shutCtx); err != nil {
		log.Printf("app shutdown: %v", err)
	}
}
