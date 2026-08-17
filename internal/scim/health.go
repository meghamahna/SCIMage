package scim

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Pinger is the readiness probe's one dependency: something that can confirm the
// backing store is reachable. *store.Store satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// readyTimeout bounds the readiness check so a hung database can't hang the
// probe. An orchestrator polling this needs a verdict, not a stall.
const readyTimeout = 2 * time.Second

// LivenessHandler reports that the process is up and serving. It takes no
// dependencies on purpose: a liveness probe decides whether to restart the
// process, so coupling it to the database would turn a transient DB blip into a
// restart loop that can't help. Readiness is where the database belongs.
//
// Mounted outside the auth and tenant path so an orchestrator can probe it
// without a token — see cmd/server.
func LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeHealth(w, http.StatusOK, "ok")
	})
}

// ReadinessHandler reports whether the server can actually serve traffic, which
// for this service means the database is reachable. A load balancer uses this
// to decide whether to route requests here; a failing check pulls the instance
// out of rotation rather than restarting it.
func ReadinessHandler(db Pinger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			slog.Warn("readiness check failed", "error", err)
			writeHealth(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		writeHealth(w, http.StatusOK, "ok")
	})
}

// Health responses are plain application/json, not scim+json: they aren't SCIM
// resources, and the probes reading them aren't SCIM clients.
func writeHealth(w http.ResponseWriter, status int, state string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": state}); err != nil {
		slog.Error("write health response", "error", err)
	}
}
