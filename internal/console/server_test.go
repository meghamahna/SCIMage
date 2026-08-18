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
	if !strings.Contains(body, "SCIMage Console") {
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
