package scim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestTimeoutFromEnv(t *testing.T) {
	t.Run("unset falls back to the default", func(t *testing.T) {
		t.Setenv("SCIM_REQUEST_TIMEOUT", "")

		if d := requestTimeoutFromEnv(); d != defaultRequestTimeout {
			t.Errorf("timeout = %v, want default %v", d, defaultRequestTimeout)
		}
	})

	t.Run("invalid falls back to the default", func(t *testing.T) {
		t.Setenv("SCIM_REQUEST_TIMEOUT", "not-a-duration")

		if d := requestTimeoutFromEnv(); d != defaultRequestTimeout {
			t.Errorf("timeout = %v, want default %v", d, defaultRequestTimeout)
		}
	})

	t.Run("reads a configured value", func(t *testing.T) {
		t.Setenv("SCIM_REQUEST_TIMEOUT", "15s")

		if d := requestTimeoutFromEnv(); d != 15*time.Second {
			t.Errorf("timeout = %v, want 15s", d)
		}
	})

	t.Run("a non-positive value is honored, not defaulted", func(t *testing.T) {
		t.Setenv("SCIM_REQUEST_TIMEOUT", "0s")

		if d := requestTimeoutFromEnv(); d != 0 {
			t.Errorf("timeout = %v, want 0 (disabled)", d)
		}
	})
}

func TestWithTimeoutDeadlineExceeded(t *testing.T) {
	var sawDeadline bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		sawDeadline = r.Context().Err() == context.DeadlineExceeded
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	withTimeout(time.Millisecond)(next).ServeHTTP(rec, req)

	if !sawDeadline {
		t.Error("handler's context did not carry a deadline that expired")
	}
}

func TestWithTimeoutZeroDisables(t *testing.T) {
	var gotDeadline bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	withTimeout(0)(next).ServeHTTP(rec, req)

	if gotDeadline {
		t.Error("expected no deadline when timeout is 0")
	}
}
