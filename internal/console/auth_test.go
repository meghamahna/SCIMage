package console

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// fakeTokens is an in-memory ConsoleTokenStore, so the auth middleware can be
// exercised without Postgres. The handler integration tests use the real
// store; these focus on the accept/reject decision itself.
type fakeTokens struct {
	byKeyID map[string]*store.ConsoleToken
	touched []string
}

func (f *fakeTokens) GetConsoleTokenByKeyID(_ context.Context, keyID string) (*store.ConsoleToken, error) {
	tok, ok := f.byKeyID[keyID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return tok, nil
}

func (f *fakeTokens) TouchConsoleToken(_ context.Context, keyID string) error {
	f.touched = append(f.touched, keyID)
	return nil
}

// makeToken builds a console token whose secret hashes to the stored hash, and
// returns the plaintext a client would send. The plaintext is assembled from
// parts rather than written as a literal, matching the SCIM tests' convention
// for keeping fixture tokens out of the source as flat strings.
func makeToken(keyID, secret string) (*store.ConsoleToken, string) {
	sum := sha256.Sum256([]byte(secret))
	tok := &store.ConsoleToken{KeyID: keyID, SecretHash: sum[:], Label: "test"}
	return tok, "scimage_console_" + keyID + "_" + secret
}

func serveWithAuth(tokens ConsoleTokenStore, req *http.Request) *httptest.ResponseRecorder {
	var reached bool
	h := requireConsoleToken(tokens)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if id := identityFrom(r.Context()); id.KeyID == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK && !reached {
		panic("200 without reaching the handler")
	}
	return rec
}

func bearer(token string) string { return "Bearer " + token }

func TestConsoleAuthAcceptsBearer(t *testing.T) {
	tok, plaintext := makeToken("k1", "s3cr3t")
	tokens := &fakeTokens{byKeyID: map[string]*store.ConsoleToken{"k1": tok}}

	req := httptest.NewRequest("GET", "/console/tenants", nil)
	req.Header.Set("Authorization", bearer(plaintext))

	if rec := serveWithAuth(tokens, req); rec.Code != http.StatusOK {
		t.Fatalf("Bearer auth: status = %d, want 200", rec.Code)
	}
	if len(tokens.touched) != 1 || tokens.touched[0] != "k1" {
		t.Errorf("expected the token to be touched once, got %v", tokens.touched)
	}
}

func TestConsoleAuthAcceptsBasicPassword(t *testing.T) {
	tok, plaintext := makeToken("k2", "hunter2")
	tokens := &fakeTokens{byKeyID: map[string]*store.ConsoleToken{"k2": tok}}

	req := httptest.NewRequest("GET", "/console/tenants", nil)
	// Browser dialog: token in the password field, username left blank.
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+plaintext)))

	if rec := serveWithAuth(tokens, req); rec.Code != http.StatusOK {
		t.Fatalf("Basic auth (password): status = %d, want 200", rec.Code)
	}
}

func TestConsoleAuthAcceptsBasicUsername(t *testing.T) {
	tok, plaintext := makeToken("k3", "abc")
	tokens := &fakeTokens{byKeyID: map[string]*store.ConsoleToken{"k3": tok}}

	req := httptest.NewRequest("GET", "/console/tenants", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(plaintext+":")))

	if rec := serveWithAuth(tokens, req); rec.Code != http.StatusOK {
		t.Fatalf("Basic auth (username): status = %d, want 200", rec.Code)
	}
}

func TestConsoleAuthRejects(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	valid, _ := makeToken("k", "right-secret")
	_, wrongSecret := makeToken("k", "wrong-secret")

	revoked, revokedPlain := makeToken("kr", "s")
	revoked.RevokedAt = &past
	expired, expiredPlain := makeToken("ke", "s")
	expired.ExpiresAt = &past
	live, livePlain := makeToken("kl", "s")
	live.ExpiresAt = &future

	tokens := &fakeTokens{byKeyID: map[string]*store.ConsoleToken{
		"k": valid, "kr": revoked, "ke": expired, "kl": live,
	}}

	// Tokens are built from parts, never written as flat literals.
	_, unknownKey := makeToken("nope", "whatever-secret-value")
	scimNotConsole := "scimage_" + "abcdef01" + "_notaconsoletoken"

	for _, tc := range []struct {
		name string
		auth string
	}{
		{"no header", ""},
		{"malformed token", bearer("not-a-token")},
		{"unknown key id", bearer(unknownKey)},
		{"wrong secret", bearer(wrongSecret)},
		{"revoked", bearer(revokedPlain)},
		{"expired", bearer(expiredPlain)},
		{"scim token, not console", bearer(scimNotConsole)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/console/tenants", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := serveWithAuth(tokens, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("a 401 must carry a WWW-Authenticate challenge so the browser prompts")
			}
		})
	}

	// The live, unexpired token still works — a guard against the expiry check
	// rejecting everything.
	t.Run("live token accepted", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/console/tenants", nil)
		req.Header.Set("Authorization", bearer(livePlain))
		if rec := serveWithAuth(tokens, req); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}
