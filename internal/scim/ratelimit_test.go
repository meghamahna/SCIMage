package scim

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

// send runs one request through the throttle and reports the status.
func send(t *testing.T, l *limiter, key string) int {
	t.Helper()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rr := httptest.NewRecorder()
	l.throttle(key, next).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/Users", nil))
	return rr.Code
}

func TestLimiter(t *testing.T) {
	// rate.Limit(0) with a burst refills nothing, so exactly burst requests get
	// through and the next is refused — deterministic, no sleeping.
	t.Run("allows the burst then refuses", func(t *testing.T) {
		l := newLimiter(0, 3)

		for i := range 3 {
			if got := send(t, l, "tok_a"); got != http.StatusOK {
				t.Fatalf("request %d = %d, want 200 within the burst", i+1, got)
			}
		}
		if got := send(t, l, "tok_a"); got != http.StatusTooManyRequests {
			t.Errorf("request past the burst = %d, want 429", got)
		}
	})

	t.Run("buckets are per key", func(t *testing.T) {
		l := newLimiter(0, 1)

		if got := send(t, l, "tok_a"); got != http.StatusOK {
			t.Fatalf("first caller = %d, want 200", got)
		}
		if got := send(t, l, "tok_a"); got != http.StatusTooManyRequests {
			t.Fatalf("first caller again = %d, want 429", got)
		}
		if got := send(t, l, "tok_b"); got != http.StatusOK {
			t.Errorf("second caller = %d, want 200 — one caller drained another's bucket", got)
		}
	})

	t.Run("429 is a SCIM error with Retry-After", func(t *testing.T) {
		l := newLimiter(0, 0)

		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("request reached the handler")
		})
		rr := httptest.NewRecorder()
		l.throttle("tok_a", next).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/Users", nil))

		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", rr.Code)
		}
		if got := rr.Header().Get("Retry-After"); got == "" {
			t.Error("no Retry-After header")
		}
		if got := rr.Header().Get("Content-Type"); got != contentType {
			t.Errorf("Content-Type = %q, want %q", got, contentType)
		}

		scimErr := decodeBody[Error](t, rr)
		if scimErr.Status != "429" {
			t.Errorf("status = %q, want \"429\"", scimErr.Status)
		}
		if scimErr.ScimType != "tooMany" {
			t.Errorf("scimType = %q, want tooMany", scimErr.ScimType)
		}
	})

	// A nil limiter is the disabled case, not a crash.
	t.Run("a disabled limiter passes everything through", func(t *testing.T) {
		var l *limiter

		for range 50 {
			if got := send(t, l, "tok_a"); got != http.StatusOK {
				t.Fatalf("status = %d, want 200 when limiting is off", got)
			}
		}
	})

	t.Run("refills over time", func(t *testing.T) {
		l := newLimiter(rate.Every(0), 1) // rate.Every(0) is an infinite refill

		for i := range 5 {
			if got := send(t, l, "tok_a"); got != http.StatusOK {
				t.Fatalf("request %d = %d, want 200 with an unlimited refill", i+1, got)
			}
		}
	})
}

func TestLimiterFromEnv(t *testing.T) {
	t.Run("zero disables limiting", func(t *testing.T) {
		t.Setenv("SCIM_RATE_LIMIT", "0")

		if l := limiterFromEnv(); l != nil {
			t.Error("expected a nil limiter when SCIM_RATE_LIMIT=0")
		}
	})

	t.Run("unset falls back to the default", func(t *testing.T) {
		t.Setenv("SCIM_RATE_LIMIT", "")

		l := limiterFromEnv()
		if l == nil {
			t.Fatal("expected limiting to be on by default")
		}
		if l.limit != defaultRateLimit || l.burst != defaultRateBurst {
			t.Errorf("limit/burst = %v/%d, want %d/%d", l.limit, l.burst, defaultRateLimit, defaultRateBurst)
		}
	})

	t.Run("reads the configured values", func(t *testing.T) {
		t.Setenv("SCIM_RATE_LIMIT", "7")
		t.Setenv("SCIM_RATE_BURST", "9")

		l := limiterFromEnv()
		if l == nil {
			t.Fatal("expected a limiter")
		}
		if l.limit != 7 || l.burst != 9 {
			t.Errorf("limit/burst = %v/%d, want 7/9", l.limit, l.burst)
		}
	})
}
