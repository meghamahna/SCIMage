// Package console is the admin console: a small, loopback-by-default web UI for
// whoever runs SCIMage. It covers the day-to-day of scimage-admin — viewing and
// mutating tenants, tokens, and attributes, reading the audit trails, ARIA's
// read, and webhook delivery health — but only for that one operator. It is
// deliberately not a customer-facing self-service portal: a tenant's own IT
// staff never log in here.
//
// One capability stays CLI-only on purpose: issuing and revoking the console's
// own login tokens (scimage-admin console-token). A console session can't be
// allowed to mint or revoke the credential that guards the console, and the
// first token has to come from somewhere before anyone can log in at all, so
// that lifecycle lives out-of-band in the CLI.
//
// Every mutating route reuses the exact store.* functions scimage-admin calls,
// so the audit-log-in-transaction guarantee is inherited, never
// re-implemented here.
package console

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// Store is everything the console reads and mutates. The concrete *store.Store
// satisfies it; the interface keeps the handlers testable and documents the
// exact surface the console touches. It embeds ConsoleTokenStore so one value
// covers both auth and data.
type Store interface {
	ConsoleTokenStore

	ListTenants(ctx context.Context) ([]store.Tenant, error)
	CreateTenant(ctx context.Context, name, createdBy string) (*store.Tenant, error)

	ListTokens(ctx context.Context, tenantID string) ([]store.Token, error)
	IssueToken(ctx context.Context, tenantID, label, createdBy string, expiresAt *time.Time) (string, *store.Token, error)
	RevokeToken(ctx context.Context, keyID, actor string) error

	ListAttributes(ctx context.Context, tenantID string) ([]store.TenantAttribute, error)
	RegisterAttribute(ctx context.Context, tenantID, name, typ, actor string) (*store.TenantAttribute, error)
	UnregisterAttribute(ctx context.Context, tenantID, name, actor string) error

	ListAuditEntries(ctx context.Context, tenantID string, limit int) ([]store.AuditEntry, error)
	ListAuditEntriesSince(ctx context.Context, tenantID string, since time.Time) ([]store.AuditEntry, error)
	AuditWindowStats(ctx context.Context, tenantID string, start, end time.Time) (total, callers int, err error)
	ListAdminAuditEntries(ctx context.Context, tenantID string, limit int) ([]store.AdminAuditEntry, error)

	WebhookDeliverySummary(ctx context.Context) (store.WebhookSummary, error)
	DeadLetters(ctx context.Context, limit int) ([]store.Delivery, error)
	PendingDeliveries(ctx context.Context, limit int) ([]store.Delivery, error)
	ReplayDeadLetter(ctx context.Context, id int64, actor string) error
	WebhookDeliveryStatus(ctx context.Context, id int64) (string, error)
}

// Server holds the console's dependencies. env is the label shown in the
// sidebar badge — purely cosmetic, so an operator can tell prod from staging
// at a glance.
type Server struct {
	store      Store
	tmpl       *template.Template
	csrf       *csrfGuard
	env        string
	loc        *time.Location  // timezone ARIA's off-hours check evaluates against
	scimBase   string          // SCIM_BASE_URL, trimmed; "" falls back to a placeholder
	narratives *narrativeCache // last LLM briefing per tenant+window
}

// NewServer parses the templates and mints a fresh CSRF key. A template or key
// failure is returned rather than panicked, so cmd/server can decide whether a
// broken console should stop the whole process.
func NewServer(s Store, env string) (*Server, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, fmt.Errorf("parse console templates: %w", err)
	}
	guard, err := newCSRFGuard()
	if err != nil {
		return nil, fmt.Errorf("console csrf: %w", err)
	}
	if env == "" {
		env = "local"
	}
	return &Server{
		store:      s,
		tmpl:       tmpl,
		csrf:       guard,
		env:        env,
		loc:        ariaLocation(),
		scimBase:   strings.TrimSuffix(strings.TrimSpace(os.Getenv("SCIM_BASE_URL")), "/"),
		narratives: newNarrativeCache(),
	}, nil
}

// ariaLocation resolves the timezone ARIA's business-hours check uses, from
// ARIA_TIMEZONE, falling back to the host's local zone — the same precedence
// the aria CLI applies, so the console and the CLI agree on what "off-hours"
// means.
func ariaLocation() *time.Location {
	if tz := envTimezone(); tz != nil {
		return tz
	}
	return time.Local
}

// Handler builds the console's routes under /console. Static assets mount
// before auth so the login dialog's page can style itself; every other route
// is wrapped in requireConsoleToken.
func (srv *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Read pages.
	mux.HandleFunc("GET /console", srv.handleHome)
	mux.HandleFunc("GET /console/tenants", srv.handleTenants)
	mux.HandleFunc("GET /console/tokens", srv.handleTokens)
	mux.HandleFunc("GET /console/attributes", srv.handleAttributes)
	mux.HandleFunc("GET /console/webhooks", srv.handleWebhooks)
	mux.HandleFunc("GET /console/webhooks/delivery-status", srv.handleDeliveryStatus)
	mux.HandleFunc("GET /console/audit", srv.handleAudit)
	mux.HandleFunc("GET /console/admin-audit", srv.handleAdminAudit)
	mux.HandleFunc("GET /console/aria", srv.handleARIA)

	// Mutations. Each verifies the CSRF token before touching the store.
	mux.HandleFunc("POST /console/tenants", srv.handleCreateTenant)
	mux.HandleFunc("POST /console/tokens/issue", srv.handleIssueToken)
	mux.HandleFunc("POST /console/tokens/revoke", srv.handleRevokeToken)
	mux.HandleFunc("POST /console/attributes/register", srv.handleRegisterAttribute)
	mux.HandleFunc("POST /console/attributes/unregister", srv.handleUnregisterAttribute)
	mux.HandleFunc("POST /console/webhooks/replay", srv.handleReplayDeadLetter)
	mux.HandleFunc("POST /console/aria/narrate", srv.handleARIANarrate)

	authed := requireConsoleToken(srv.store)(mux)

	root := http.NewServeMux()
	root.Handle("GET /console/static/", http.StripPrefix("/console/static/", staticHandler()))
	root.Handle("/", authed)
	return root
}
