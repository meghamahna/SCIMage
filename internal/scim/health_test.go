package scim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakePinger stands in for the store: the health handlers only need a Ping, so
// these tests carry no database dependency and run everywhere.
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestLivenessAlwaysOK(t *testing.T) {
	rec := httptest.NewRecorder()
	LivenessHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	assertStatusBody(t, rec.Body.Bytes(), "ok")
}

func TestReadinessReflectsPing(t *testing.T) {
	tests := []struct {
		name     string
		pingErr  error
		wantCode int
		wantBody string
	}{
		{"reachable", nil, http.StatusOK, "ok"},
		{"unreachable", errors.New("connection refused"), http.StatusServiceUnavailable, "unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ReadinessHandler(fakePinger{err: tt.pingErr}).
				ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			assertStatusBody(t, rec.Body.Bytes(), tt.wantBody)
		})
	}
}

func assertStatusBody(t *testing.T, body []byte, want string) {
	t.Helper()
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	if got["status"] != want {
		t.Errorf("status = %q, want %q", got["status"], want)
	}
}
