package console

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// csrfWindow is how long a rendered form stays submittable. A stale token is
// rejected, so an operator who leaves a tab open past this reloads before the
// form works again — an acceptable trade for keeping the scheme sessionless.
const csrfWindow = time.Hour

// csrfGuard issues and verifies stateless CSRF tokens. Browsers re-send HTTP
// Basic credentials on any request to the console, including a form another
// site submits, so authentication alone does not show the operator meant the
// POST. Two checks do: the request's origin (sameOrigin), and a token that is
// an HMAC over a time bucket and the operator's console key id, so a token
// only works for the operator it was rendered for. The key is random per
// process, so tokens don't survive a restart, which is fine for short-lived
// form tokens.
type csrfGuard struct {
	key []byte
}

func newCSRFGuard() (*csrfGuard, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate csrf key: %w", err)
	}
	return &csrfGuard{key: key}, nil
}

// token returns operator keyID's CSRF token for the bucket `now` falls in.
func (g *csrfGuard) token(keyID string, now time.Time) string {
	return g.tokenForBucket(bucketOf(now), keyID)
}

func (g *csrfGuard) tokenForBucket(bucket int64, keyID string) string {
	mac := hmac.New(sha256.New, g.key)
	// The bucket is a fixed 8 bytes and the key id is the only variable-length
	// field after it, so no two (bucket, keyID) pairs produce the same input.
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(bucket))
	mac.Write(buf[:])
	mac.Write([]byte(keyID))
	sum := mac.Sum(nil)
	return strconv.FormatInt(bucket, 10) + "." + hex.EncodeToString(sum)
}

// valid reports whether tok is a token this guard issued to operator keyID for
// the current or immediately previous bucket. Accepting the previous bucket
// keeps a form that was rendered just before a boundary from failing on
// submit. An empty keyID means no authenticated operator and always fails.
func (g *csrfGuard) valid(tok, keyID string, now time.Time) bool {
	if keyID == "" {
		return false
	}
	bucketStr, _, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	bucket, err := strconv.ParseInt(bucketStr, 10, 64)
	if err != nil {
		return false
	}

	current := bucketOf(now)
	if bucket != current && bucket != current-1 {
		return false
	}
	// Recompute the expected token for the claimed bucket and compare in
	// constant time, so a forged MAC can't be probed byte by byte.
	want := g.tokenForBucket(bucket, keyID)
	return subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1
}

// sameOrigin reports whether a POST came from a console page rather than
// another site. Sec-Fetch-Site is preferred when present and must be
// "same-origin". Otherwise Origin must name this host; the scheme is not
// compared because the console may serve plain HTTP behind a TLS proxy.
//
// A request with neither header is accepted: browsers send Origin on every
// POST, so its absence means a non-browser client (curl with a Bearer token),
// which no other site can make an operator's browser send. The per-operator
// token still applies.
func sameOrigin(r *http.Request) bool {
	if site, ok := r.Header["Sec-Fetch-Site"]; ok {
		return len(site) == 1 && site[0] == "same-origin"
	}
	origin, ok := r.Header["Origin"]
	if !ok {
		return true
	}
	if len(origin) != 1 {
		return false
	}
	u, err := url.Parse(origin[0])
	// An Origin is exactly scheme://host[:port]; "null" and anything with a
	// path, query or userinfo fails the round-trip check.
	if err != nil || u.Host == "" || u.Scheme+"://"+u.Host != origin[0] {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// clipHeader cuts a client-supplied header value to at most 256 bytes of valid
// UTF-8 before it is logged, so a refused request can't flood the log.
func clipHeader(s string) string {
	const limit = 256
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "")
}

func bucketOf(t time.Time) int64 {
	return t.Unix() / int64(csrfWindow.Seconds())
}
