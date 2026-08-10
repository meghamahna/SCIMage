package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/meghamahna/SCIMage/internal/scim"
	"github.com/meghamahna/SCIMage/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func run() error {
	token, err := scim.TokenFromEnv()
	if err != nil {
		return err
	}

	dsn, err := store.DSNFromEnv()
	if err != nil {
		return err
	}

	s, err := store.New(context.Background(), dsn)
	if err != nil {
		return err
	}
	defer s.Close()

	addr := os.Getenv("SCIM_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           scim.NewHandler(s, token).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("listening on %s", addr)
	return srv.ListenAndServe()
}
