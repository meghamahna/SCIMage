package console

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meghamahna/SCIMage/internal/store"
)

// consoleIT bundles a live console server, its store, an authenticating token,
// and a dedicated cleanup pool. The cleanup pool is opened from the same DSN
// rather than reaching into the store's unexported pool: the store owns all
// its SQL, so a test tidies up through its own connection instead of widening
// the store's API.
type consoleIT struct {
	srv     *Server
	store   *store.Store
	auth    string
	cleanup *pgxpool.Pool
}

// newConsoleIT drives the real store against the compose Postgres, never a
// mock — the same discipline the rest of the suite uses. It skips when no
// database is configured.
func newConsoleIT(t *testing.T) *consoleIT {
	t.Helper()
	ctx := context.Background()

	dsn, err := store.DSNFromEnv()
	if err != nil {
		t.Skipf("no database configured — run `make test` (%v)", err)
	}
	s, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(s.Close)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open cleanup pool: %v", err)
	}
	t.Cleanup(pool.Close)

	srv, err := NewServer(s, "test")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	plaintext, tok, err := s.IssueConsoleToken(ctx, "console-it", "test-suite", nil)
	if err != nil {
		t.Fatalf("IssueConsoleToken: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM admin_audit_log WHERE tenant_id IS NULL AND target_id = $1`, tok.KeyID)
		pool.Exec(ctx, `DELETE FROM console_tokens WHERE key_id = $1`, tok.KeyID)
	})

	return &consoleIT{srv: srv, store: s, auth: bearer(plaintext), cleanup: pool}
}

// cleanupTenant removes a tenant created during a test and everything under it.
func (it *consoleIT) cleanupTenant(t *testing.T, id string) {
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM audit_log WHERE tenant_id = $1`,
			`DELETE FROM admin_audit_log WHERE tenant_id = $1`,
			`DELETE FROM scim_tokens WHERE tenant_id = $1`,
			`DELETE FROM tenant_attributes WHERE tenant_id = $1`,
			`DELETE FROM users WHERE tenant_id = $1`,
			`DELETE FROM tenants WHERE id = $1`,
		} {
			it.cleanup.Exec(ctx, q, id)
		}
	})
}

func do(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func authedGet(srv *Server, auth, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	req.Header.Set("Authorization", auth)
	return do(srv, req)
}

func authedPost(srv *Server, auth, target string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(srv, req)
}

func TestConsoleRequiresAuth(t *testing.T) {
	it := newConsoleIT(t)

	rec := do(it.srv, httptest.NewRequest("GET", "/console/tenants", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET status = %d, want 401", rec.Code)
	}
}

func TestConsoleStaticServedWithoutAuth(t *testing.T) {
	it := newConsoleIT(t)

	rec := do(it.srv, httptest.NewRequest("GET", "/console/static/console.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("static CSS status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("Content-Type = %q, want a css type", ct)
	}
}

func TestConsoleTenantsPageRenders(t *testing.T) {
	it := newConsoleIT(t)
	name := fmt.Sprintf("console-it-%d", time.Now().UnixNano())
	tn, err := it.store.CreateTenant(context.Background(), name, "test-suite")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	it.cleanupTenant(t, tn.ID)

	rec := authedGet(it.srv, it.auth, "/console/tenants")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, name) {
		t.Error("tenants page does not list the created tenant")
	}
	if !strings.Contains(body, "SCIMage Admin Console") {
		t.Error("layout did not render (missing title)")
	}
}

func TestConsoleCreateTenantRequiresCSRF(t *testing.T) {
	it := newConsoleIT(t)

	form := url.Values{"name": {"should-not-be-created"}, "csrf": {"bogus"}}
	rec := authedPost(it.srv, it.auth, "/console/tenants", form)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST with a bad CSRF token status = %d, want 403", rec.Code)
	}
}

func TestConsoleCreateTenantSucceeds(t *testing.T) {
	it := newConsoleIT(t)
	name := fmt.Sprintf("console-created-%d", time.Now().UnixNano())

	form := url.Values{"name": {name}, "csrf": {it.srv.csrf.token(time.Now())}}
	rec := authedPost(it.srv, it.auth, "/console/tenants", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST create tenant status = %d, want 303", rec.Code)
	}

	tenants, err := it.store.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	var created *store.Tenant
	for i := range tenants {
		if tenants[i].Name == name {
			created = &tenants[i]
		}
	}
	if created == nil {
		t.Fatal("tenant was not created")
	}
	it.cleanupTenant(t, created.ID)

	// The console attributes the action to its own credential, so the admin
	// audit trail shows it came through the console, not the CLI.
	entries, err := it.store.ListAdminAuditEntries(context.Background(), created.ID, 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}
	if len(entries) == 0 || !strings.HasPrefix(entries[0].Actor, "console:") {
		t.Errorf("admin audit actor = %+v, want a console:-prefixed actor", entries)
	}
}

func TestConsoleIssueAndRevokeToken(t *testing.T) {
	it := newConsoleIT(t)
	ctx := context.Background()

	name := fmt.Sprintf("console-tok-%d", time.Now().UnixNano())
	tn, err := it.store.CreateTenant(ctx, name, "test-suite")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	it.cleanupTenant(t, tn.ID)

	// Issue: the plaintext is revealed once in the response body.
	form := url.Values{"tenant": {tn.ID}, "label": {"okta"}, "csrf": {it.srv.csrf.token(time.Now())}}
	rec := authedPost(it.srv, it.auth, "/console/tokens/issue", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue token status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "scimage_") {
		t.Error("issue response does not reveal the plaintext token once")
	}

	tokens, err := it.store.ListTokens(ctx, tn.ID)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("got %d tokens, want 1", len(tokens))
	}
	keyID := tokens[0].KeyID

	// Revoke.
	revForm := url.Values{"tenant": {tn.ID}, "key_id": {keyID}, "csrf": {it.srv.csrf.token(time.Now())}}
	rec = authedPost(it.srv, it.auth, "/console/tokens/revoke", revForm)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke token status = %d, want 303", rec.Code)
	}

	got, err := it.store.GetTokenByKeyID(ctx, keyID)
	if err != nil {
		t.Fatalf("GetTokenByKeyID: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("token is not revoked after the revoke POST")
	}
}

func TestConsoleARIAPageRenders(t *testing.T) {
	it := newConsoleIT(t)

	rec := authedGet(it.srv, it.auth, "/console/aria?since=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("ARIA page status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Entries reviewed") {
		t.Error("ARIA page did not render its stat row")
	}
}

func TestConsoleAdminAuditRenders(t *testing.T) {
	it := newConsoleIT(t)

	rec := authedGet(it.srv, it.auth, "/console/admin-audit")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin audit status = %d, want 200", rec.Code)
	}
}

func TestConsoleHomePageRenders(t *testing.T) {
	it := newConsoleIT(t)

	rec := authedGet(it.srv, it.auth, "/console")
	if rec.Code != http.StatusOK {
		t.Fatalf("home status = %d, want 200 (should render, not redirect)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "SCIM base URL") {
		t.Error("home page did not render its SCIM base URL section")
	}
}

func TestConsoleWebhooksPageRenders(t *testing.T) {
	it := newConsoleIT(t)

	rec := authedGet(it.srv, it.auth, "/console/webhooks")
	if rec.Code != http.StatusOK {
		t.Fatalf("webhooks status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Parked and queued deliveries") {
		t.Error("webhooks page did not render the parked-deliveries table")
	}
}

func TestConsoleReplayRequiresCSRF(t *testing.T) {
	it := newConsoleIT(t)

	form := url.Values{"id": {"1"}, "csrf": {"bogus"}}
	rec := authedPost(it.srv, it.auth, "/console/webhooks/replay", form)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("replay with a bad CSRF token status = %d, want 403", rec.Code)
	}
}

// A replay for a delivery that isn't parked (here, one that doesn't exist)
// reports it cleanly rather than 500-ing — exercising the route, CSRF, handler,
// and the store's ErrNotFound guard end to end.
func TestConsoleReplayUnknownDelivery(t *testing.T) {
	it := newConsoleIT(t)

	form := url.Values{"id": {"999999999"}, "csrf": {it.srv.csrf.token(time.Now())}}
	rec := authedPost(it.srv, it.auth, "/console/webhooks/replay", form)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("replay of an unknown delivery status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no longer parked") {
		t.Error("replay of an unknown delivery did not explain itself")
	}
}

// The Generate/Refresh button fetches with X-Requested-With and expects just the
// briefing block back, so it can swap it in place without leaving /console/aria.
// Scoped to a fresh, empty tenant so there are no findings — which keeps the test
// off the real LLM (a window with findings would try to narrate).
func TestConsoleARIANarrateReturnsFragment(t *testing.T) {
	it := newConsoleIT(t)
	tn, err := it.store.CreateTenant(context.Background(), fmt.Sprintf("aria-frag-%d", time.Now().UnixNano()), "test-suite")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	it.cleanupTenant(t, tn.ID)

	form := url.Values{"tenant": {tn.ID}, "since": {"24h"}, "csrf": {it.srv.csrf.token(time.Now())}}
	req := httptest.NewRequest("POST", "/console/aria/narrate", strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", it.auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "fetch")
	rec := do(it.srv, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("narrate fragment status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ARIA briefing") {
		t.Error("fragment response should contain the briefing block")
	}
	if strings.Contains(body, "Entries reviewed") {
		t.Error("fragment response leaked the full page chrome")
	}
}

func TestConsoleDeliveryStatusUnknown(t *testing.T) {
	it := newConsoleIT(t)

	rec := authedGet(it.srv, it.auth, "/console/webhooks/delivery-status?id=999999999")
	if rec.Code != http.StatusOK {
		t.Fatalf("delivery-status of an unknown id = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"unknown"`) {
		t.Errorf("body = %q, want an unknown status", rec.Body.String())
	}
}

// The Replay button posts with Accept: application/json and expects a JSON
// verdict it can act on in place, rather than a redirect.
func TestConsoleReplayJSONUnknown(t *testing.T) {
	it := newConsoleIT(t)

	form := url.Values{"id": {"999999999"}, "csrf": {it.srv.csrf.token(time.Now())}}
	req := httptest.NewRequest("POST", "/console/webhooks/replay", strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", it.auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := do(it.srv, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("json replay of an unknown id = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Errorf("body = %q, want ok:false", rec.Body.String())
	}
}

// A tenant created in the console must show the SCIM base URL an operator hands
// an identity provider — the path parity the CLI already had.
func TestConsoleTenantsPageShowsBaseURL(t *testing.T) {
	it := newConsoleIT(t)
	name := fmt.Sprintf("console-baseurl-%d", time.Now().UnixNano())

	tn, err := it.store.CreateTenant(context.Background(), name, "test-suite")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	it.cleanupTenant(t, tn.ID)

	rec := authedGet(it.srv, it.auth, "/console/tenants")
	if rec.Code != http.StatusOK {
		t.Fatalf("tenants status = %d, want 200", rec.Code)
	}
	if want := "/scim/v2/" + tn.ID; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("tenants page did not render the SCIM base URL %q", want)
	}
}
