package scim

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

func revokedFakeToken() *store.Token {
	tok := validFakeToken()
	at := time.Now().Add(-time.Minute)
	tok.RevokedAt = &at
	return tok
}

func expiredFakeToken() *store.Token {
	tok := validFakeToken()
	past := time.Now().Add(-time.Minute)
	tok.ExpiresAt = &past
	return tok
}

// authed sends a request through requireToken and reports whether it got past.
func authed(t *testing.T, tokens TokenStore, target, authorization string) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest(http.MethodGet, target, nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}

	rr := httptest.NewRecorder()
	requireToken(tokens)(next).ServeHTTP(rr, r)
	return rr, reached
}

func TestRequireToken(t *testing.T) {
	target := "/scim/v2/" + fakeTenantID + "/Users"
	valid := fakeTokenStore{tok: validFakeToken()}

	t.Run("accepts a valid token for its own tenant", func(t *testing.T) {
		rr, reached := authed(t, valid, target, "Bearer "+fakeToken)

		if !reached {
			t.Fatalf("request was rejected: %d %s", rr.Code, rr.Body)
		}
	})

	// RFC 7235 §2.1 makes the scheme case-insensitive.
	t.Run("scheme is case-insensitive", func(t *testing.T) {
		if _, reached := authed(t, valid, target, "bEaReR "+fakeToken); !reached {
			t.Error("lowercase scheme was rejected")
		}
	})

	const wrongPart = "wrongsecretwrongsecretwrongsecret"
	const otherTenantID = "tenant_07e4000000000000"
	// Far past any real key id or tenant id, so an unbounded log line shows.
	huge := strings.Repeat("a", 64<<10)
	for _, tc := range []struct {
		name          string
		tokens        fakeTokenStore
		target        string
		authorization string
		wantReason    string
		wantKeyID     string
		wantTenantID  string
	}{
		{"no header", valid, target, "", authMalformed, "", fakeTenantID},
		{"empty header", valid, target, " ", authMalformed, "", fakeTenantID},
		{"no scheme", valid, target, fakeToken, authMalformed, "", fakeTenantID},
		{"wrong scheme", valid, target, "Basic " + fakeToken, authMalformed, "", fakeTenantID},
		{"scheme without a space", valid, target, "Bearer" + fakeToken, authMalformed, "", fakeTenantID},
		{"malformed token", fakeTokenStore{}, target, "Bearer not-a-scim-token", authMalformed, "", fakeTenantID},
		{"lookup error", fakeTokenStore{err: errors.New("db unavailable")}, target, "Bearer " + fakeToken, authLookupError, fakeKeyID, fakeTenantID},
		{"lookup miss", fakeTokenStore{err: store.ErrNotFound}, target, "Bearer " + fakeToken, authUnknownKey, fakeKeyID, fakeTenantID},
		{"wrong secret", valid, target, "Bearer scimage_" + fakeKeyID + "_" + wrongPart, authHashMismatch, fakeKeyID, fakeTenantID},
		{"right token, wrong tenant in path", valid, "/scim/v2/" + otherTenantID + "/Users", "Bearer " + fakeToken, authTenantMismatch, fakeKeyID, otherTenantID},
		{"oversized key id", valid, target, "Bearer scimage_" + huge + "_" + fakePlaintext, authMalformed, "", fakeTenantID},
		{"oversized tenant in path", valid, "/scim/v2/tenant_" + huge + "/Users", "Bearer " + fakeToken, authTenantMismatch, fakeKeyID, ""},
		{"revoked", fakeTokenStore{tok: revokedFakeToken()}, target, "Bearer " + fakeToken, authRevoked, fakeKeyID, fakeTenantID},
		{"expired", fakeTokenStore{tok: expiredFakeToken()}, target, "Bearer " + fakeToken, authExpired, fakeKeyID, fakeTenantID},
	} {
		t.Run(tc.name+" is rejected and logged", func(t *testing.T) {
			logs := captureLogs(t)

			rr, reached := authed(t, tc.tokens, tc.target, tc.authorization)

			if reached {
				t.Fatal("request reached the handler")
			}
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rr.Code)
			}

			got := logRecords(t, logs, "authentication failed")
			if len(got) != 1 {
				t.Fatalf("got %d authentication-failed lines, want 1", len(got))
			}
			want := map[string]any{"level": "WARN", "reason": tc.wantReason, "key_id": tc.wantKeyID,
				"tenant_id": tc.wantTenantID, "remote_addr": "192.0.2.1"}
			for k, v := range want {
				if got[0][k] != v {
					t.Errorf("%s = %v, want %v", k, got[0][k], v)
				}
			}
			if out := logs.String(); strings.Contains(out, fakePlaintext) || strings.Contains(out, wrongPart) {
				t.Errorf("the log carries the token secret: %s", out)
			}
			if n := logs.Len(); n > 1024 {
				t.Errorf("the log line is %d bytes, want it bounded whatever the caller sends", n)
			}
		})
	}

	t.Run("401 is a SCIM error with a challenge", func(t *testing.T) {
		rr, _ := authed(t, valid, target, "")

		if got := rr.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("WWW-Authenticate = %q, want Bearer", got)
		}
		if got := rr.Header().Get("Content-Type"); got != contentType {
			t.Errorf("Content-Type = %q, want %q", got, contentType)
		}

		scimErr := decodeBody[Error](t, rr)
		if scimErr.Status != "401" {
			t.Errorf("status = %q, want \"401\"", scimErr.Status)
		}
		if len(scimErr.Schemas) != 1 || scimErr.Schemas[0] != errorSchema {
			t.Errorf("schemas = %v, want the Error schema", scimErr.Schemas)
		}
		if strings.Contains(rr.Body.String(), fakePlaintext) {
			t.Error("the response body echoes the token secret")
		}
	})
}

// Auth is applied by Routes itself, so no wiring mistake can serve the API
// unauthenticated. Unknown paths are rejected before routing, so a 404 can't
// tell an unauthenticated caller which resources exist.
func TestRoutesRequireAuth(t *testing.T) {
	// A nil store is fine: the 401 path never reaches a handler, so this stays
	// meaningful without a database.
	routes := NewHandler(nil, nil, fakeTokenStore{tok: validFakeToken()}, nil).Routes()
	prefix := "/scim/v2/" + fakeTenantID

	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, prefix + "/ServiceProviderConfig"},
		{http.MethodPost, prefix + "/Users"},
		{http.MethodGet, prefix + "/Users"},
		{http.MethodGet, prefix + "/Users/" + nonexistentID},
		{http.MethodPut, prefix + "/Users/" + nonexistentID},
		{http.MethodDelete, prefix + "/Users/" + nonexistentID},
		{http.MethodPatch, prefix + "/Users/" + nonexistentID},
		{http.MethodGet, prefix + "/Groups"},
		{http.MethodGet, "/unknown/path/entirely"},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			rr := sendUnauthenticated(t, routes, tc.method, tc.target)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 without a token", rr.Code)
			}
		})
	}
}

// sendUnauthenticated drives a handler with no Authorization header.
func sendUnauthenticated(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(method, target, nil))
	return rr
}

func TestTenantIDFromPath(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/scim/v2/tenant_abc/Users", "tenant_abc"},
		{"/scim/v2/tenant_abc/Users/123", "tenant_abc"},
		{"/scim/v2/tenant_abc/ServiceProviderConfig", "tenant_abc"},
		{"/scim/v2/tenant_abc", "tenant_abc"},
		{"/scim/v2/", ""},
		{"/nonsense", ""},
		{"/", ""},
		// A %2f inside the tenant segment is not a path separator to the mux
		// (net/http unescapes one segment at a time), so it has to come out
		// unescaped here too — matching what r.PathValue("tenantID") would
		// return for the same request, not truncating early.
		{"/scim/v2/tenant_abc%2Fx/Users", "tenant_abc/x"},
		{"/scim/v2/tenant_abc%2Fx", "tenant_abc/x"},
	} {
		if got := tenantIDFromPath(tc.path); got != tc.want {
			t.Errorf("tenantIDFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// captureLogs sends slog's default logger to a buffer, as JSON lines, for one
// test. Nothing in this package runs in parallel, so swapping it is safe.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// logRecords returns the captured records with message msg.
func logRecords(t *testing.T, buf *bytes.Buffer, msg string) []map[string]any {
	t.Helper()

	var out []map[string]any
	for line := range strings.Lines(buf.String()) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}
