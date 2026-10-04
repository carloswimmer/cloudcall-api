package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"cloudcall/internal/app"
	"cloudcall/internal/platform/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	a, err := app.New(ctx, cfg)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	defer a.Shutdown()
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           a.WithMiddleware(cfg.CORSOrigin),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
