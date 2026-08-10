package scim

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authTestToken is deliberately a different value from testToken, so a handler
// built with one can't be authenticated by the other.
const authTestToken = "auth-test-token-9876543210"

// authed sends a request through the middleware and reports whether it got past.
func authed(t *testing.T, authorization string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	return authedWith(t, authTestToken, authorization)
}

func authedWith(t *testing.T, configured, authorization string) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest(http.MethodGet, "/Users", nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}

	rr := httptest.NewRecorder()
	requireBearer(configured)(next).ServeHTTP(rr, r)
	return rr, reached
}

func TestRequireBearer(t *testing.T) {
	t.Run("accepts the configured token", func(t *testing.T) {
		rr, reached := authed(t, "Bearer "+authTestToken)

		if !reached {
			t.Fatalf("request was rejected: %d %s", rr.Code, rr.Body)
		}
	})

	// RFC 7235 §2.1 makes the scheme case-insensitive.
	t.Run("scheme is case-insensitive", func(t *testing.T) {
		if _, reached := authed(t, "bEaReR "+authTestToken); !reached {
			t.Error("lowercase scheme was rejected")
		}
	})

	for _, tc := range []struct{ name, authorization string }{
		{"no header", ""},
		{"empty header", " "},
		{"wrong token", "Bearer " + strings.ToUpper(authTestToken)},
		{"empty token", "Bearer "},
		{"prefix of the token", "Bearer " + authTestToken[:len(authTestToken)-1]},
		{"token plus a suffix", "Bearer " + authTestToken + "x"},
		{"no scheme", authTestToken},
		{"wrong scheme", "Basic " + authTestToken},
		{"scheme without a space", "Bearer" + authTestToken},
	} {
		t.Run(tc.name+" is rejected", func(t *testing.T) {
			rr, reached := authed(t, tc.authorization)

			if reached {
				t.Fatal("request reached the handler")
			}
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rr.Code)
			}
		})
	}

	// A misconfigured token must reject everything. Hashing both sides means an
	// empty configured token would otherwise match a request with no header at
	// all — SHA-256("") on both sides — and serve the whole API anonymously.
	for _, tc := range []struct{ name, configured string }{
		{"empty", ""},
		{"one short of the minimum", strings.Repeat("a", minTokenLen-1)},
	} {
		t.Run("a "+tc.name+" configured token rejects everything", func(t *testing.T) {
			for _, authorization := range []string{
				"",
				"Bearer ",
				"Bearer " + tc.configured,
				"Bearer " + authTestToken,
			} {
				rr, reached := authedWith(t, tc.configured, authorization)
				if reached {
					t.Errorf("Authorization %q reached the handler", authorization)
				}
				if rr.Code != http.StatusUnauthorized {
					t.Errorf("Authorization %q gave %d, want 401", authorization, rr.Code)
				}
			}
		})
	}

	t.Run("401 is a SCIM error with a challenge", func(t *testing.T) {
		rr, _ := authed(t, "")

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
		if strings.Contains(rr.Body.String(), authTestToken) {
			t.Error("the response body echoes the configured token")
		}
	})
}

// Auth is applied by Routes itself, so no wiring mistake can serve the API
// unauthenticated. Unknown paths are rejected before routing, so a 404 can't
// tell an unauthenticated caller which resources exist.
func TestRoutesRequireAuth(t *testing.T) {
	// A nil store is fine: the 401 path never reaches a handler, so this stays
	// meaningful without a database.
	routes := NewHandler(nil, authTestToken, nil).Routes()

	for _, tc := range []struct{ method, target string }{
		{http.MethodPost, "/Users"},
		{http.MethodGet, "/Users"},
		{http.MethodGet, "/Users/" + nonexistentID},
		{http.MethodPut, "/Users/" + nonexistentID},
		{http.MethodDelete, "/Users/" + nonexistentID},
		{http.MethodPatch, "/Users/" + nonexistentID},
		{http.MethodGet, "/Groups"},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			rr := httptest.NewRecorder()
			routes.ServeHTTP(rr, r)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 without a token", rr.Code)
			}
		})
	}
}

func TestTokenFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"too short", strings.Repeat("a", minTokenLen-1), true},
		{"minimum length", strings.Repeat("a", minTokenLen), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SCIM_TOKEN", tc.value)

			got, err := TokenFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("TokenFromEnv() = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("TokenFromEnv(): %v", err)
			}
			if got != tc.value {
				t.Errorf("TokenFromEnv() = %q, want %q", got, tc.value)
			}
		})
	}
}
