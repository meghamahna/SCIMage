package scim

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

// minTokenLen is the shortest token the server will serve with. Short enough to
// brute-force is the same as no token at all.
const minTokenLen = 16

// TokenFromEnv reads SCIM_TOKEN. It is an error for it to be missing: starting
// without a token would leave provisioning open to anyone who finds the port.
// Surrounding whitespace is trimmed, since a stray space in .env would
// otherwise produce a server that starts and then rejects every request.
func TokenFromEnv() (string, error) {
	token := strings.TrimSpace(os.Getenv("SCIM_TOKEN"))
	if token == "" {
		return "", fmt.Errorf("SCIM_TOKEN is not set (generate one with: openssl rand -hex 32)")
	}
	if len(token) < minTokenLen {
		return "", fmt.Errorf("SCIM_TOKEN must be at least %d characters", minTokenLen)
	}
	return token, nil
}

// requireBearer rejects every request that doesn't carry the token, including
// ones for paths that don't exist — a 404 before authentication would tell an
// unauthenticated caller which resources are real.
//
// The comparison is over SHA-256 digests rather than the raw tokens:
// subtle.ConstantTimeCompare returns early when lengths differ, so comparing
// raw would leak the token's length. Hashing makes both sides fixed-width, and
// the compare itself stays constant-time.
// An unusable token rejects everything rather than being compared: with an
// empty token both sides hash to SHA-256("") and a request carrying no
// Authorization header at all would authenticate. Config errors have to fail
// closed, not open.
func requireBearer(token string) func(http.Handler) http.Handler {
	usable := len(token) >= minTokenLen
	if !usable {
		log.Print("scim: no usable SCIM_TOKEN — every request will be rejected")
	}
	want := sha256.Sum256([]byte(token))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := sha256.Sum256([]byte(bearerToken(r)))
			if !usable || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeError(w, http.StatusUnauthorized, "", "invalid or missing bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
