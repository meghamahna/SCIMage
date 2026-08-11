// Package webhook delivers SCIM change events to the application's own
// endpoint, so a provisioned user reaches the system that actually needs them.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// SignatureHeader carries the scheme version, the timestamp and the MAC.
	SignatureHeader = "X-SCIMage-Signature"

	// DeliveryHeader is stable across retries of the same event, so a receiver
	// can deduplicate on it. Delivery is at-least-once.
	DeliveryHeader = "X-SCIMage-Delivery-Id"

	// EventHeader lets a receiver route without parsing the body.
	EventHeader = "X-SCIMage-Event"
)

// minSecretLen matches the bearer token's floor. A secret short enough to
// brute-force makes the signature decorative.
const minSecretLen = 16

var (
	ErrMalformedSignature = errors.New("malformed signature header")
	ErrStaleSignature     = errors.New("signature timestamp outside tolerance")
	ErrBadSignature       = errors.New("signature does not match")
)

// Sign returns the SignatureHeader value for a delivery.
//
// Three things beyond the body are covered, because a receiver is told to act
// on all of them. The timestamp is inside the signed material rather than
// merely alongside it, which is what makes replay protection possible: a
// receiver can reject old timestamps knowing a captured request cannot be
// re-stamped without the secret. The delivery id is covered because
// deduplicating on an unauthenticated key would let a replay through under a
// fresh id, and the event type because routing on a header an attacker can
// rewrite is no better than not checking it.
func Sign(secret string, ts time.Time, deliveryID int64, event string, body []byte) string {
	stamp := strconv.FormatInt(ts.Unix(), 10)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(mac(secret, stamp, deliveryID, event, body))
}

// mac is the signed material. The fields are newline-separated and the body
// comes last: the timestamp and delivery id are decimal, and event types are a
// fixed set of lowercase identifiers, so none of them can contain the separator
// and no field boundary is ambiguous. Only the body is arbitrary, and nothing
// follows it to be confused with.
func mac(secret, stamp string, deliveryID int64, event string, body []byte) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	for _, field := range []string{stamp, strconv.FormatInt(deliveryID, 10), event} {
		h.Write([]byte(field))
		h.Write([]byte("\n"))
	}
	h.Write(body)
	return h.Sum(nil)
}

// Verify is the receiving half, exported so a Go application consuming these
// webhooks doesn't have to reimplement the scheme — and so the round trip is
// covered by this package's own tests rather than only asserted.
//
// deliveryID and event are the values from DeliveryHeader and EventHeader; a
// receiver passes what it read, and a mismatch fails the signature.
//
// The comparison is hmac.Equal, which is constant-time: a byte-wise compare
// would let a caller discover a valid MAC one byte at a time.
func Verify(secret, header string, deliveryID int64, event string, body []byte, now time.Time, tolerance time.Duration) error {
	stamp, ts, got, err := parseSignature(header)
	if err != nil {
		return err
	}

	// Both directions: a timestamp far in the future is as suspect as a stale
	// one, and would otherwise mint a signature valid indefinitely.
	if skew := now.Sub(ts); skew > tolerance || skew < -tolerance {
		return fmt.Errorf("%w: %s off", ErrStaleSignature, skew.Truncate(time.Second))
	}

	// The raw timestamp is what gets re-hashed, not a reformatted one, so a
	// non-canonical stamp fails rather than being silently normalised.
	if !hmac.Equal(got, mac(secret, stamp, deliveryID, event, body)) {
		return ErrBadSignature
	}
	return nil
}

func parseSignature(header string) (stamp string, ts time.Time, mac []byte, err error) {
	var rawMAC string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			stamp = v
		case "v1":
			rawMAC = v
		}
	}

	if stamp == "" || rawMAC == "" {
		return "", time.Time{}, nil, fmt.Errorf("%w: want t=<unix>,v1=<hex>", ErrMalformedSignature)
	}

	secs, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return "", time.Time{}, nil, fmt.Errorf("%w: timestamp: %v", ErrMalformedSignature, err)
	}

	mac, err = hex.DecodeString(rawMAC)
	if err != nil {
		return "", time.Time{}, nil, fmt.Errorf("%w: mac: %v", ErrMalformedSignature, err)
	}

	return stamp, time.Unix(secs, 0), mac, nil
}
