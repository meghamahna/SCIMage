package scim

import (
	"context"
	"net/http"
	"os"
	"time"
)

// defaultRequestTimeout bounds how long one request may hold a database
// connection. A stuck query fails the request instead of holding a pool slot,
// and the goroutine serving it, indefinitely. Applied outside auth, so it
// covers the token lookup requireToken performs before a handler ever runs,
// not just the handler's own queries.
const defaultRequestTimeout = 10 * time.Second

// requestTimeoutFromEnv reads SCIM_REQUEST_TIMEOUT as a Go duration string
// (e.g. "15s"). An unset or invalid value falls back to defaultRequestTimeout;
// a non-positive value turns the deadline off, an explicit opt-out rather than
// a silently unlimited default.
func requestTimeoutFromEnv() time.Duration {
	if v := os.Getenv("SCIM_REQUEST_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultRequestTimeout
}

// withTimeout bounds the request's context so every downstream database call
// fails fast instead of holding a connection open indefinitely.
func withTimeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if timeout <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
