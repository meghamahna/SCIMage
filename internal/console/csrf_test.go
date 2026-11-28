package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	operatorA = "ck_operator_a"
	operatorB = "ck_operator_b"
)

func TestCSRFTokenRoundTrips(t *testing.T) {
	g, err := newCSRFGuard()
	if err != nil {
		t.Fatalf("newCSRFGuard: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)

	tok := g.token(operatorA, now)
	if !g.valid(tok, operatorA, now) {
		t.Error("a freshly issued token is rejected in the same bucket")
	}
}

func TestCSRFAcceptsPreviousBucket(t *testing.T) {
	g, _ := newCSRFGuard()
	now := time.Unix(1_700_000_000, 0)

	tok := g.token(operatorA, now)
	// One window later: the token's bucket is now the previous one, still valid.
	if !g.valid(tok, operatorA, now.Add(csrfWindow)) {
		t.Error("a token from the immediately previous bucket should still be accepted")
	}
}

func TestCSRFRejectsStaleToken(t *testing.T) {
	g, _ := newCSRFGuard()
	now := time.Unix(1_700_000_000, 0)

	tok := g.token(operatorA, now)
	if g.valid(tok, operatorA, now.Add(3*csrfWindow)) {
		t.Error("a token three windows old should be rejected")
	}
}

func TestCSRFRejectsTampered(t *testing.T) {
	g, _ := newCSRFGuard()
	now := time.Unix(1_700_000_000, 0)
	good := g.token(operatorA, now)

	for _, tc := range []struct {
		name  string
		tok   string
		keyID string
	}{
		{"empty", "", operatorA},
		{"no separator", "deadbeef", operatorA},
		{"non-numeric bucket", "abc.deadbeef", operatorA},
		{"forged mac", good[:len(good)-4] + "0000", operatorA},
		{"wrong key", func() string { other, _ := newCSRFGuard(); return other.token(operatorA, now) }(), operatorA},
		{"other operator", good, operatorB},
		{"no operator", good, ""},
		{"empty operator token", g.token("", now), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if g.valid(tc.tok, tc.keyID, now) {
				t.Errorf("valid(%q, %q) = true, want false", tc.tok, tc.keyID)
			}
		})
	}
}

// checkCSRF applies the origin check and the per-operator token to every
// console POST; both failures answer 403.
func TestCheckCSRF(t *testing.T) {
	g, _ := newCSRFGuard()
	srv := &Server{csrf: g}
	const host = "console.example.com:8443"
	validTok := g.token(operatorA, time.Now())

	for _, tc := range []struct {
		name      string
		keyID     string
		tok       string
		fetchSite []string
		origin    []string
		want      bool
	}{
		{"no browser headers, valid token", operatorA, validTok, nil, nil, true},
		{"same-origin fetch", operatorA, validTok, []string{"same-origin"}, []string{"https://" + host}, true},
		{"same-site fetch", operatorA, validTok, []string{"same-site"}, nil, false},
		{"cross-site fetch", operatorA, validTok, []string{"cross-site"}, nil, false},
		{"user-initiated fetch", operatorA, validTok, []string{"none"}, nil, false},
		{"empty fetch site", operatorA, validTok, []string{""}, nil, false},
		{"origin same host, scheme ignored", operatorA, validTok, nil, []string{"http://" + host}, true},
		{"origin other host", operatorA, validTok, nil, []string{"https://evil.example.com"}, false},
		{"origin other port", operatorA, validTok, nil, []string{"https://console.example.com:9443"}, false},
		{"origin null", operatorA, validTok, nil, []string{"null"}, false},
		{"origin unparseable", operatorA, validTok, nil, []string{"https://%zz"}, false},
		{"origin with path", operatorA, validTok, nil, []string{"https://" + host + "/x"}, false},
		{"origin host in upper case", operatorA, validTok, nil, []string{"https://CONSOLE.EXAMPLE.COM:8443"}, true},
		{"two origin values", operatorA, validTok, nil, []string{"https://" + host, "https://" + host}, false},
		{"cross-site fetch wins over matching origin", operatorA, validTok, []string{"cross-site"}, []string{"https://" + host}, false},
		{"same-origin fetch wins over foreign origin", operatorA, validTok, []string{"same-origin"}, []string{"https://evil.example.com"}, true},
		{"other operator's token", operatorB, validTok, nil, nil, false},
		{"no identity in context", "", validTok, nil, nil, false},
		{"bad token, same origin", operatorA, "bogus", []string{"same-origin"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"csrf": {tc.tok}}
			req := httptest.NewRequest("POST", "/console/tenants", strings.NewReader(form.Encode()))
			req.Host = host
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.fetchSite != nil {
				req.Header["Sec-Fetch-Site"] = tc.fetchSite
			}
			if tc.origin != nil {
				req.Header["Origin"] = tc.origin
			}
			if tc.keyID != "" {
				ctx := context.WithValue(req.Context(), consoleIdentityKey{}, consoleIdentity{KeyID: tc.keyID})
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()

			got := srv.checkCSRF(rec, req)
			if got != tc.want {
				t.Fatalf("checkCSRF = %v, want %v", got, tc.want)
			}
			if !got && rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestSameOriginIPv6Host(t *testing.T) {
	req := httptest.NewRequest("POST", "/console/tenants", nil)
	req.Host = "[::1]:8090"
	req.Header.Set("Origin", "http://[::1]:8090")
	if !sameOrigin(req) {
		t.Error("an Origin naming the same IPv6 literal host is refused")
	}
}

// A cross-site POST is refused on its headers, so a large body is never parsed.
func TestCheckCSRFRefusesOriginBeforeParsingBody(t *testing.T) {
	g, _ := newCSRFGuard()
	srv := &Server{csrf: g}
	body := "csrf=" + g.token(operatorA, time.Now()) + "&name=" + strings.Repeat("a", 1<<20)
	req := httptest.NewRequest("POST", "/console/tenants", strings.NewReader(body))
	req.Host = "console.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example.com")
	req = req.WithContext(context.WithValue(req.Context(), consoleIdentityKey{}, consoleIdentity{KeyID: operatorA}))
	rec := httptest.NewRecorder()

	if srv.checkCSRF(rec, req) {
		t.Fatal("checkCSRF accepted a cross-site POST")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if req.PostForm != nil {
		t.Error("the body was parsed before the origin check refused the request")
	}
}

// A body over the form cap answers 413, not the 403 a failed token check gives;
// a body right at the cap still goes through.
func TestCheckCSRFBodyLimit(t *testing.T) {
	g, _ := newCSRFGuard()
	srv := &Server{csrf: g}
	prefix := "csrf=" + g.token(operatorA, time.Now()) + "&name="
	for _, tc := range []struct {
		name     string
		size     int
		wantOK   bool
		wantCode int
	}{
		{"at the limit", maxFormBytes, true, http.StatusOK},
		{"one byte over", maxFormBytes + 1, false, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := prefix + strings.Repeat("a", tc.size-len(prefix))
			req := httptest.NewRequest("POST", "/console/tenants", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req = req.WithContext(context.WithValue(req.Context(), consoleIdentityKey{}, consoleIdentity{KeyID: operatorA}))
			rec := httptest.NewRecorder()

			if got := srv.checkCSRF(rec, req); got != tc.wantOK {
				t.Fatalf("checkCSRF = %v, want %v", got, tc.wantOK)
			}
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}
