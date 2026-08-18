package console

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// ConsoleTokenStore is the slice of the store the auth middleware needs. Kept
// narrow so a test can supply a fake without standing up Postgres, the same
// shape internal/scim uses for its own token lookups.
type ConsoleTokenStore interface {
	GetConsoleTokenByKeyID(ctx context.Context, keyID string) (*store.ConsoleToken, error)
	TouchConsoleToken(ctx context.Context, keyID string) error
}

type consoleIdentityKey struct{}

// consoleIdentity is who a request authenticated as. KeyID is non-secret (the
// role a username plays) and safe to show in the UI and logs.
type consoleIdentity struct {
	KeyID string
	Label string
}

func identityFrom(ctx context.Context) consoleIdentity {
	id, _ := ctx.Value(consoleIdentityKey{}).(consoleIdentity)
	return id
}

// requireConsoleToken authenticates every console request against an issued
// console_tokens credential. It accepts the token as an HTTP Basic password
// (so a browser's own login dialog works — the operator pastes the token
// there) or as a Bearer header (so curl/scripts work), and compares the
// secret in constant time, exactly as the SCIM auth path does.
//
// Every rejection reason answers with the same 401 and the same Basic
// challenge; distinguishing them would let a caller probe which key ids exist.
func requireConsoleToken(tokens ConsoleTokenStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := authenticateConsole(r, tokens)
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="SCIMage Console"`)
				http.Error(w, "invalid or missing console credential", http.StatusUnauthorized)
				return
			}

			// Best-effort, like the SCIM path: last_used_at describes the
			// token, not the request, so a failure here is a log line and not
			// a reason to reject a caller already let in.
			if err := tokens.TouchConsoleToken(r.Context(), id.KeyID); err != nil {
				slog.Error("touch console token", "key_id", id.KeyID, "error", err)
			}

			ctx := context.WithValue(r.Context(), consoleIdentityKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authenticateConsole(r *http.Request, tokens ConsoleTokenStore) (consoleIdentity, bool) {
	raw := consoleCredential(r)
	keyID, secret, ok := store.ParseConsoleToken(raw)
	if !ok {
		return consoleIdentity{}, false
	}

	tok, err := tokens.GetConsoleTokenByKeyID(r.Context(), keyID)
	if err != nil {
		// A missing key is ordinary and attacker-triggerable; anything else —
		// the store unreachable, say — is operational and must not vanish
		// just because the caller still sees a flat 401.
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("look up console token", "key_id", keyID, "error", err)
		}
		return consoleIdentity{}, false
	}

	if tok.RevokedAt != nil {
		return consoleIdentity{}, false
	}
	if tok.ExpiresAt != nil && !tok.ExpiresAt.After(time.Now()) {
		return consoleIdentity{}, false
	}

	got := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(got[:], tok.SecretHash) != 1 {
		return consoleIdentity{}, false
	}

	return consoleIdentity{KeyID: tok.KeyID, Label: tok.Label}, true
}

// consoleCredential pulls the token from either an Authorization: Bearer
// header or HTTP Basic auth. For Basic, the token can arrive as the password
// (username ignored — what a browser dialog submits when the operator leaves
// the username blank) or as the username (what some clients send when only
// one field is filled).
func consoleCredential(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if bearer, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(bearer)
	}
	if user, pass, ok := r.BasicAuth(); ok {
		if strings.HasPrefix(pass, consolePrefix) {
			return pass
		}
		if strings.HasPrefix(user, consolePrefix) {
			return user
		}
	}
	return ""
}

// consolePrefix mirrors store.consoleTokenPrefix; the store keeps that
// constant unexported, so the check that a Basic field looks like a console
// token lives here rather than reaching across the package boundary.
const consolePrefix = "scimage_console_"
