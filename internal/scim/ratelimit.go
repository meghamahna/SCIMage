package scim

import (
	"net/http"
	"os"
	"strconv"
	"sync"

	"golang.org/x/time/rate"
)

// Defaults sized for provisioning traffic: an IdP pushing a bulk import sends
// bursts, then goes quiet for hours.
const (
	defaultRateLimit = 20 // requests per second, sustained
	defaultRateBurst = 40
)

// limiter is a token bucket per caller, keyed by token fingerprint. With a
// single configured token that is one bucket for the whole server; the keying
// is what makes it per-caller once there is more than one token.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	limit   rate.Limit
	burst   int
}

func newLimiter(limit rate.Limit, burst int) *limiter {
	return &limiter{
		buckets: make(map[string]*rate.Limiter),
		limit:   limit,
		burst:   burst,
	}
}

// limiterFromEnv reads SCIM_RATE_LIMIT and SCIM_RATE_BURST. A limit of 0
// disables limiting entirely, which is an explicit opt-out rather than a
// silently unlimited default.
func limiterFromEnv() *limiter {
	limit := envInt("SCIM_RATE_LIMIT", defaultRateLimit)
	if limit <= 0 {
		return nil
	}
	return newLimiter(rate.Limit(limit), envInt("SCIM_RATE_BURST", defaultRateBurst))
}

func envInt(name string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return v
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = rate.NewLimiter(l.limit, l.burst)
		l.buckets[key] = b
	}
	l.mu.Unlock()

	return b.Allow()
}

// throttle sits behind auth, so an unauthenticated flood never consumes a real
// caller's budget. The trade-off is that it also doesn't limit unauthenticated
// requests — those are bounded by the cost of one SHA-256 compare.
func (l *limiter) throttle(key string, next http.Handler) http.Handler {
	if l == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(key) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "tooMany", "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
