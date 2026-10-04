package main

import (
	"log"
	"net/http"

	"cloudcall/internal/app"
)

func main() {
	addr := ":8080"
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, app.NewLive().Handler()); err != nil {
		log.Fatal(err)
	}
}
