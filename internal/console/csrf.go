package console

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// csrfWindow is how long a rendered form stays submittable. A stale token is
// rejected, so an operator who leaves a tab open past this reloads before the
// form works again — an acceptable trade for keeping the scheme sessionless.
const csrfWindow = time.Hour

// csrfGuard issues and verifies stateless CSRF tokens. There is no session and
// no cookie: a token is an HMAC over a time bucket, so any process holding the
// key can verify one without shared state. The key is random per process, so
// tokens don't survive a restart — which for CSRF is fine (they're
// short-lived by design). Requiring one on every mutating route is
// defence-in-depth here: the console authenticates with a Bearer/Basic token,
// not a cookie, so a browser won't attach credentials to a cross-site POST in
// the first place.
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

// token returns the CSRF token for the bucket `now` falls in.
func (g *csrfGuard) token(now time.Time) string {
	return g.tokenForBucket(bucketOf(now))
}

func (g *csrfGuard) tokenForBucket(bucket int64) string {
	mac := hmac.New(sha256.New, g.key)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(bucket))
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	return strconv.FormatInt(bucket, 10) + "." + hex.EncodeToString(sum)
}

// valid reports whether tok is a token this guard issued for the current or
// immediately previous bucket. Accepting the previous bucket keeps a form that
// was rendered just before a boundary from failing on submit.
func (g *csrfGuard) valid(tok string, now time.Time) bool {
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
	want := g.tokenForBucket(bucket)
	return subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1
}

func bucketOf(t time.Time) int64 {
	return t.Unix() / int64(csrfWindow.Seconds())
}
