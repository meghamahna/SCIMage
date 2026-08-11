package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/meghamahna/SCIMage/internal/logging"
	"github.com/meghamahna/SCIMage/internal/scim"
	"github.com/meghamahna/SCIMage/internal/store"
	"github.com/meghamahna/SCIMage/internal/webhook"
)

// How long in-flight requests get to finish once a signal arrives.
const shutdownGrace = 15 * time.Second

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

	// Reading the webhook config before opening the pool means a bad setting
	// fails at startup rather than on the first mutation.
	hookCfg, hooks, err := webhook.ConfigFromEnv()
	if err != nil {
		return err
	}

	// The store only queues change events when something is going to drain
	// them, so a deployment without webhooks doesn't accumulate a backlog.
	var opts []store.Option
	if hooks {
		opts = append(opts, store.WithChangeEvents())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := store.New(ctx, dsn, opts...)
	if err != nil {
		return err
	}
	defer s.Close()

	// The dispatcher outlives the signal context on purpose: it is stopped after
	// the listener drains, so a request still in flight when the signal arrives
	// can enqueue its event and have a chance of sending it.
	dispatchCtx, stopDispatch := context.WithCancel(context.Background())
	defer stopDispatch()

	var wg sync.WaitGroup
	if hooks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			webhook.New(s, hookCfg).Run(dispatchCtx)
		}()
	}

	addr := os.Getenv("SCIM_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           scim.NewHandler(s, token).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", addr)
		errs <- srv.ListenAndServe()
	}()

	var serveErr error
	select {
	case serveErr = <-errs:
		// The listener never came up — a taken port, say. There is nothing to
		// drain, but the dispatcher is already running and still has to be
		// stopped below.
	case <-ctx.Done():
		stop() // a second signal kills the process rather than waiting
		slog.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("graceful shutdown", "error", err)
		}
		serveErr = <-errs
	}

	// Both exits stop the dispatcher and wait for it, so it can't log into a
	// closed file or query a closed pool on the way out.
	//
	// Anything still queued, or abandoned mid-flight here, keeps its row and
	// goes out when the server comes back — its lease simply expires. That is
	// what the outbox is for, so shutdown doesn't have to wait on a receiver.
	stopDispatch()
	wg.Wait()

	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}
