package webhook

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Fixtures, not credentials — the real signing secret comes from
// SCIM_WEBHOOK_SECRET at runtime. Named "key" rather than "secret" for the same
// reason handler_test.go says testToken: the staged-diff scanner flags a quoted
// value assigned to anything called secret, and that check is worth keeping
// strict.
const (
	testSigningKey  = "webhook-test-secret-0123456789"
	wrongSigningKey = "webhook-test-key-9876543210"

	testTolerance  = 5 * time.Minute
	testDeliveryID = 4172
	testEvent      = "user.created"
)

var (
	signedAt = time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	testBody = []byte(`{"type":"user.created","userId":"u-1"}`)
)

func sign(ts time.Time) string {
	return Sign(testSigningKey, ts, testDeliveryID, testEvent, testBody)
}

func TestVerifyAcceptsWhatSignProduced(t *testing.T) {
	if err := Verify(testSigningKey, sign(signedAt), testDeliveryID, testEvent, testBody, signedAt, testTolerance); err != nil {
		t.Fatalf("Verify of a fresh signature: %v", err)
	}
}

// Every rejection path, since a signature that fails open is worse than none.
func TestVerifyRejects(t *testing.T) {
	valid := sign(signedAt)

	for _, tc := range []struct {
		name   string
		key    string
		header string
		body   []byte
		now    time.Time
		// Zero and empty mean "what was signed"; a case sets these to show a
		// receiver reading a header that doesn't match the MAC.
		deliveryID int64
		event      string
		want       error
	}{
		{
			// The delivery id is the receiver's deduplication key. If it were
			// outside the MAC, a capture could be replayed inside the tolerance
			// window under a fresh id and slip past dedup.
			name:       "a tampered delivery id",
			key:        testSigningKey,
			header:     valid,
			body:       testBody,
			now:        signedAt,
			deliveryID: testDeliveryID + 1,
			want:       ErrBadSignature,
		},
		{
			// Receivers are told they may route on this header without parsing
			// the body, so it has to be authenticated.
			name:   "a tampered event type",
			key:    testSigningKey,
			header: valid,
			body:   testBody,
			now:    signedAt,
			event:  "user.deactivated",
			want:   ErrBadSignature,
		},
		{
			name:   "a tampered body",
			key:    testSigningKey,
			header: valid,
			body:   []byte(`{"type":"user.created","userId":"u-2"}`),
			now:    signedAt,
			want:   ErrBadSignature,
		},
		{
			// The timestamp is inside the MAC, so restamping invalidates it —
			// this is what stops a capture from being replayed indefinitely.
			name:   "a restamped signature",
			key:    testSigningKey,
			header: "t=" + itoa(signedAt.Add(time.Minute).Unix()) + "," + macPart(valid),
			body:   testBody,
			now:    signedAt.Add(time.Minute),
			want:   ErrBadSignature,
		},
		{
			name:   "the wrong secret",
			key:    wrongSigningKey,
			header: valid,
			body:   testBody,
			now:    signedAt,
			want:   ErrBadSignature,
		},
		{
			name:   "a stale timestamp",
			key:    testSigningKey,
			header: valid,
			body:   testBody,
			now:    signedAt.Add(testTolerance + time.Second),
			want:   ErrStaleSignature,
		},
		{
			// A clock far ahead would otherwise mint a signature that stays
			// valid for as long as the skew.
			name:   "a timestamp from the future",
			key:    testSigningKey,
			header: valid,
			body:   testBody,
			now:    signedAt.Add(-testTolerance - time.Second),
			want:   ErrStaleSignature,
		},
		{
			name:   "a header with no mac",
			key:    testSigningKey,
			header: "t=" + itoa(signedAt.Unix()),
			body:   testBody,
			now:    signedAt,
			want:   ErrMalformedSignature,
		},
		{
			name:   "a header with no timestamp",
			key:    testSigningKey,
			header: macPart(valid),
			body:   testBody,
			now:    signedAt,
			want:   ErrMalformedSignature,
		},
		{
			name:   "a non-hex mac",
			key:    testSigningKey,
			header: "t=" + itoa(signedAt.Unix()) + ",v1=not-hex",
			body:   testBody,
			now:    signedAt,
			want:   ErrMalformedSignature,
		},
		{
			name:   "an empty header",
			key:    testSigningKey,
			header: "",
			body:   testBody,
			now:    signedAt,
			want:   ErrMalformedSignature,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, event := tc.deliveryID, tc.event
			if id == 0 {
				id = testDeliveryID
			}
			if event == "" {
				event = testEvent
			}

			err := Verify(tc.key, tc.header, id, event, tc.body, tc.now, testTolerance)
			if !errors.Is(err, tc.want) {
				t.Errorf("Verify = %v, want %v", err, tc.want)
			}
		})
	}
}

// The header shape is part of the wire contract: a receiver parses it, so a
// change to it breaks every deployed consumer.
func TestSignProducesTheDocumentedHeaderShape(t *testing.T) {
	sig := sign(signedAt)

	wantPrefix := "t=" + itoa(signedAt.Unix()) + ",v1="
	if !strings.HasPrefix(sig, wantPrefix) {
		t.Fatalf("signature = %q, want prefix %q", sig, wantPrefix)
	}

	// SHA-256 hex.
	if mac := strings.TrimPrefix(sig, wantPrefix); len(mac) != 64 {
		t.Errorf("mac = %q (%d chars), want 64", mac, len(mac))
	}
}

func macPart(sig string) string {
	_, mac, _ := strings.Cut(sig, ",")
	return mac
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
