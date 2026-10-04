package main

import (
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
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           app.NewLive().WithMiddleware(cfg.CORSOrigin),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
