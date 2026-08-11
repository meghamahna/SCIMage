package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/meghamahna/SCIMage/internal/logging"
	"github.com/meghamahna/SCIMage/internal/scim"
	"github.com/meghamahna/SCIMage/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	closeLogs, err := logging.Setup()
	if err != nil {
		return err
	}
	defer closeLogs.Close()

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

	slog.Info("listening", "addr", addr)
	return srv.ListenAndServe()
}
