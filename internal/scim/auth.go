package scim

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// identity is who a request authenticated as, once requireToken accepts it.
// KeyID is safe to log — it isn't secret, the same reasoning that makes a
// username safe to log while a password isn't.
type identity struct {
	TenantID string
	KeyID    string
}

type identityContextKey struct{}

func identityFromContext(ctx context.Context) identity {
	id, _ := ctx.Value(identityContextKey{}).(identity)
	return id
}

// requireToken looks up the bearer token against issued, tenant-scoped
// tokens rather than one process-wide secret: there is no longer one token
// to compare against, since every tenant issues and rotates its own.
//
// Every rejection reason — no such key, revoked, expired, wrong secret, right
// secret but wrong tenant — answers with the same generic 401. Distinguishing
// them in the response would let a caller probe which tenants or key ids
// exist, the same fail-closed, uniform-rejection reasoning the single-token
// version already used.
//
// requireToken rejects every request that doesn't carry a usable token,
// including ones for paths that don't exist — a 404 before authentication
// would tell an unauthenticated caller which resources are real.
func requireToken(tokens TokenStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := authenticate(r, tokens)
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeError(w, http.StatusUnauthorized, "", "invalid or missing bearer token")
				return
			}

			// Best-effort: last_used_at describes the token, not the request's
			// data, so a failure here is logged rather than treated as an
			// authentication failure for a caller who has already been let in.
			if err := tokens.TouchToken(r.Context(), id.KeyID); err != nil {
				slog.Error("touch token", "key_id", id.KeyID, "error", err)
			}

			ctx := context.WithValue(r.Context(), identityContextKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// The verification order matters: cheap, non-secret checks (does the key
// exist, is it revoked or expired) run before the constant-time comparison,
// and the comparison runs before the tenant match — a caller can't learn
// which check failed, but the order still keeps the one check that touches
// secret material minimal and last among the lookups.
func authenticate(r *http.Request, tokens TokenStore) (identity, bool) {
	// r.PathValue isn't usable here: requireToken wraps the mux and runs
	// before it matches a pattern, which is the only place {tenantID} gets
	// populated. The path shape is fixed, so it's parsed directly instead.
	tenantID := tenantIDFromPath(r.URL.EscapedPath())

	keyID, secret, ok := store.ParseToken(bearerToken(r))
	if !ok {
		return identity{}, false
	}

	tok, err := tokens.GetTokenByKeyID(r.Context(), keyID)
	if err != nil {
		// A missing key is an ordinary, attacker-triggerable outcome (a wrong
		// or made-up token) and not worth logging. Anything else — the store
		// unreachable, say — is an operational problem that must not vanish
		// silently just because the response to the caller stays a flat 401.
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("look up token", "key_id", keyID, "error", err)
		}
		return identity{}, false
	}

	if tok.RevokedAt != nil {
		return identity{}, false
	}
	if tok.ExpiresAt != nil && !tok.ExpiresAt.After(time.Now()) {
		return identity{}, false
	}

	got := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(got[:], tok.SecretHash) != 1 {
		return identity{}, false
	}

	if tok.TenantID != tenantID {
		return identity{}, false
	}

	return identity{TenantID: tok.TenantID, KeyID: tok.KeyID}, true
}

// tenantIDFromPath pulls {tenantID} out of /scim/v2/{tenantID}/... directly,
// rather than through the mux's own path-value matching — see the comment in
// authenticate for why. An unmatched or malformed path yields "", which
// can't equal any real tenant, so it fails the same way a wrong one would.
//
// It has to agree with what the mux will later put in r.PathValue("tenantID")
// for the same request, or a caller could get authenticated as one tenant and
// routed as another. net/http's mux matches on the escaped path and unescapes
// one segment at a time — %2f inside a segment is not a separator — so this
// parses EscapedPath and unescapes only the tenant segment, the same order,
// rather than unescaping the whole path first and then splitting it.
func tenantIDFromPath(escapedPath string) string {
	const prefix = "/scim/v2/"

	rest, ok := strings.CutPrefix(escapedPath, prefix)
	if !ok {
		return ""
	}
	escapedSegment, _, _ := strings.Cut(rest, "/")

	tenantID, err := url.PathUnescape(escapedSegment)
	if err != nil {
		return ""
	}
	return tenantID
}

// The scheme is case-insensitive per RFC 7235 §2.1.
func bearerToken(r *http.Request) string {
	const scheme = "bearer "

	h := r.Header.Get("Authorization")
	if len(h) < len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return ""
	}
	return h[len(scheme):]
}
