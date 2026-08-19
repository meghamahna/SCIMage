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

	"github.com/meghamahna/SCIMage/internal/apidocs"
	"github.com/meghamahna/SCIMage/internal/console"
	"github.com/meghamahna/SCIMage/internal/logging"
	"github.com/meghamahna/SCIMage/internal/scim"
	"github.com/meghamahna/SCIMage/internal/store"
	"github.com/meghamahna/SCIMage/internal/webhook"
)

// How long in-flight requests get to finish once a signal arrives.
const shutdownGrace = 15 * time.Second

// Bounds on both listeners against a slow or stalled client tying up a
// connection indefinitely. WriteTimeout is SCIM-only (see consoleServer): the
// console's ARIA page can legitimately take up to a minute to answer, since
// narration waits on an LLM call (internal/aria's own client timeout is 60s).
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
)

// consoleServer builds the admin-console listener, or nil when CONSOLE_ADDR is
// unset. The console is opt-in: a full-mutation admin surface stays off unless
// an operator turns it on, and 127.0.0.1:8090 is the recommended value so it
// binds loopback only.
func consoleServer(s *store.Store) (*http.Server, error) {
	addr := os.Getenv("CONSOLE_ADDR")
	if addr == "" {
		return nil, nil
	}
	c, err := console.NewServer(s, os.Getenv("SCIMAGE_ENV"))
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              addr,
		Handler:           c.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
	}, nil
}

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

	// Health probes mount outside the SCIM handler's auth and tenant path: an
	// orchestrator has no token and no tenant, and liveness must not depend on
	// either. /healthz is process-up; /readyz reflects database reachability.
	root := http.NewServeMux()
	root.Handle("GET /healthz", scim.LivenessHandler())
	root.Handle("GET /readyz", scim.ReadinessHandler(s))

	// Interactive API docs: unauthenticated on purpose — the spec describes a
	// public protocol and carries no tenant data, so an integrator reads it
	// before they have a token. Mounted on the root mux, outside the SCIM
	// handler's auth, like the health probes.
	root.Handle("/docs/", http.StripPrefix("/docs", apidocs.Handler()))
	root.Handle("/docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))

	root.Handle("/", scim.NewHandler(s, s, s, s).Routes())

	srv := &http.Server{
		Addr:              addr,
		Handler:           root,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// The console is a second listener, separate from the internet-facing SCIM
	// port so the privileged admin surface never shares a socket with tenant
	// traffic. It is opt-in: only started when CONSOLE_ADDR is set, and the
	// recommended value binds loopback (127.0.0.1:8090) so it isn't reachable
	// off-host without an explicit tunnel.
	consoleSrv, err := consoleServer(s)
	if err != nil {
		return err
	}

	servers := []*http.Server{srv}
	if consoleSrv != nil {
		servers = append(servers, consoleSrv)
	}

	// One error channel per listener, so a failed bind on either is observed.
	errs := make(chan error, len(servers))
	for _, hs := range servers {
		go func(hs *http.Server) {
			slog.Info("listening", "addr", hs.Addr)
			errs <- hs.ListenAndServe()
		}(hs)
	}

	var serveErr error
	select {
	case serveErr = <-errs:
		// A listener never came up — a taken port, say. There is nothing to
		// drain, but the dispatcher is already running and still has to be
		// stopped below.
	case <-ctx.Done():
		stop() // a second signal kills the process rather than waiting
		slog.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()

		for _, hs := range servers {
			if err := hs.Shutdown(shutdownCtx); err != nil {
				slog.Error("graceful shutdown", "addr", hs.Addr, "error", err)
			}
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
