package console

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	wantCSP = "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; " +
		"form-action 'self'; object-src 'none'; frame-src 'none'"
	noStore     = "no-store"
	staticCache = "public, max-age=3600"
)

type headerCase struct {
	name      string
	req       *http.Request
	wantCode  int
	wantCache string
}

// checkHeaders asserts every response carries the framing, sniffing and
// referrer headers, whichever handler wrote it, and the expected caching.
func checkHeaders(t *testing.T, srv *Server, tests []headerCase) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(srv, tc.req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			h := rec.Header()
			for k, want := range map[string]string{
				"X-Frame-Options":         "DENY",
				"Content-Security-Policy": wantCSP,
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         "same-origin",
				"Cache-Control":           tc.wantCache,
			} {
				if got := h.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

// Responses written before any store lookup, so bare `go test` covers the
// header guarantee. The nil store is never reached on these paths.
func TestConsoleSecurityHeadersWithoutStore(t *testing.T) {
	srv, err := NewServer(nil, "test")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	get := func(target string) *http.Request { return httptest.NewRequest("GET", target, nil) }
	checkHeaders(t, srv, []headerCase{
		{"login challenge", get("/console/tokens"), http.StatusUnauthorized, noStore},
		{"static asset", get("/console/static/console.js"), http.StatusOK, staticCache},
		{"static miss", get("/console/static/nope.js"), http.StatusNotFound, noStore},
		{"static directory", get("/console/static/"), http.StatusNotFound, noStore},
	})
}

// Responses past the auth middleware, which needs a real console token.
func TestConsoleSecurityHeadersAuthenticated(t *testing.T) {
	it := newConsoleIT(t)
	authed := func(method, target string, body io.Reader) *http.Request {
		req := httptest.NewRequest(method, target, body)
		req.Header.Set("Authorization", it.auth)
		return req
	}
	post := authed("POST", "/console/tokens/revoke",
		strings.NewReader(url.Values{"csrf": {"bogus"}}.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	checkHeaders(t, it.srv, []headerCase{
		{"authenticated page", authed("GET", "/console/tenants", nil), http.StatusOK, noStore},
		{"unknown page", authed("GET", "/console/nope", nil), http.StatusNotFound, noStore},
		{"wrong method", authed("PUT", "/console/tenants", nil), http.StatusMethodNotAllowed, noStore},
		{"refused post", post, http.StatusForbidden, noStore},
	})
}

// The CSP refuses inline script, inline event handlers and style attributes.
var (
	scriptTag     = regexp.MustCompile(`<script\b[^>]*>`)
	inlineHandler = regexp.MustCompile(`\son[a-z]+\s*=`)
	inlineStyle   = regexp.MustCompile(`\sstyle\s*=|<style\b`)
)

// inlineMarkup returns the first piece of markup the CSP would refuse, or "".
func inlineMarkup(s string) string {
	for _, tag := range scriptTag.FindAllString(s, -1) {
		if !strings.Contains(tag, " src=") {
			return tag
		}
	}
	if m := inlineHandler.FindString(s); m != "" {
		return m
	}
	return inlineStyle.FindString(s)
}

// Scanning the sources covers branches a rendered test page wouldn't reach.
func TestConsoleTemplatesHaveNoInlineScriptOrStyle(t *testing.T) {
	names, err := fs.Glob(templatesFS, "templates/*.html")
	if err != nil || len(names) == 0 {
		t.Fatalf("glob templates: %v (%d files)", err, len(names))
	}
	for _, name := range names {
		b, err := templatesFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if m := inlineMarkup(string(b)); m != "" {
			t.Errorf("%s has inline markup the CSP refuses: %q", name, m)
		}
	}
}

// Every GET page, with a tenant selected so the confirm-on-submit forms and
// auto-submitting selects render, loads console.js and nothing inline.
func TestConsolePagesHaveNoInlineScriptOrStyle(t *testing.T) {
	it := newConsoleIT(t)
	ctx := context.Background()

	tn, err := it.store.CreateTenant(ctx, fmt.Sprintf("console-csp-%d", time.Now().UnixNano()), "test-suite")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	it.cleanupTenant(t, tn.ID)
	if _, _, err := it.store.IssueToken(ctx, tn.ID, "okta", "test-suite", nil); err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if _, err := it.store.RegisterAttribute(ctx, tn.ID, "costCenter", "string", "test-suite"); err != nil {
		t.Fatalf("RegisterAttribute: %v", err)
	}

	tenant := url.QueryEscape(tn.ID)
	pages := []struct{ target, want string }{
		{"/console", ""},
		{"/console/tenants", ""},
		{"/console/tokens?tenant=" + tenant, `data-confirm="Revoke `},
		{"/console/attributes?tenant=" + tenant, `data-confirm="Unregister costCenter?"`},
		{"/console/webhooks", ""},
		{"/console/audit?tenant=" + tenant, "data-autosubmit"},
		{"/console/admin-audit", ""},
		{"/console/aria?since=24h&tenant=" + tenant, "data-autosubmit"},
	}
	for _, p := range pages {
		rec := authedGet(it.srv, it.auth, p.target)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", p.target, rec.Code)
			continue
		}
		body := rec.Body.String()
		if m := inlineMarkup(body); m != "" {
			t.Errorf("GET %s has inline markup the CSP refuses: %q", p.target, m)
		}
		if !strings.Contains(body, `<script src="/console/static/console.js?v=`) {
			t.Errorf("GET %s does not load console.js", p.target)
		}
		if p.want != "" && !strings.Contains(body, p.want) {
			t.Errorf("GET %s is missing %q", p.target, p.want)
		}
	}
}
