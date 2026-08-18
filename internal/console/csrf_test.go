package console

import (
	"testing"
	"time"
)

func TestCSRFTokenRoundTrips(t *testing.T) {
	g, err := newCSRFGuard()
	if err != nil {
		t.Fatalf("newCSRFGuard: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)

	tok := g.token(now)
	if !g.valid(tok, now) {
		t.Error("a freshly issued token is rejected in the same bucket")
	}
}

func TestCSRFAcceptsPreviousBucket(t *testing.T) {
	g, _ := newCSRFGuard()
	now := time.Unix(1_700_000_000, 0)

	tok := g.token(now)
	// One window later: the token's bucket is now the previous one, still valid.
	if !g.valid(tok, now.Add(csrfWindow)) {
		t.Error("a token from the immediately previous bucket should still be accepted")
	}
}

func TestCSRFRejectsStaleToken(t *testing.T) {
	g, _ := newCSRFGuard()
	now := time.Unix(1_700_000_000, 0)

	tok := g.token(now)
	if g.valid(tok, now.Add(3*csrfWindow)) {
		t.Error("a token three windows old should be rejected")
	}
}

func TestCSRFRejectsTampered(t *testing.T) {
	g, _ := newCSRFGuard()
	now := time.Unix(1_700_000_000, 0)

	for _, tc := range []struct {
		name string
		tok  string
	}{
		{"empty", ""},
		{"no separator", "deadbeef"},
		{"non-numeric bucket", "abc.deadbeef"},
		{"forged mac", g.token(now)[:len(g.token(now))-4] + "0000"},
		{"wrong key", func() string { other, _ := newCSRFGuard(); return other.token(now) }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if g.valid(tc.tok, now) {
				t.Errorf("valid(%q) = true, want false", tc.tok)
			}
		})
	}
}
