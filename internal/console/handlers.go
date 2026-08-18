package console

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/meghamahna/SCIMage/internal/aria"
	"github.com/meghamahna/SCIMage/internal/store"
)

// auditListLimit caps how many rows the audit views pull. The store clamps to
// its own MaxPageSize regardless; this keeps a page from rendering an
// unmanageable table when that cap is large.
const auditListLimit = 200

func (srv *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/console/tenants", http.StatusFound)
}

// ---- Tenants ----

func (srv *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	srv.showTenants(w, r, http.StatusOK, "")
}

func (srv *Server) showTenants(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	tenants, err := srv.store.ListTenants(r.Context())
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	srv.render(w, r, status, "tenants", pageView{
		Title:  "Tenants",
		Active: "tenants",
		Error:  errMsg,
		Data:   struct{ Tenants []store.Tenant }{tenants},
	})
}

func (srv *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	if !srv.checkCSRF(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		srv.showTenants(w, r, http.StatusBadRequest, "A display name is required.")
		return
	}

	if _, err := srv.store.CreateTenant(r.Context(), name, actor(r)); err != nil {
		if errors.Is(err, store.ErrDuplicateTenantName) {
			srv.showTenants(w, r, http.StatusConflict, "A tenant with that name already exists.")
			return
		}
		srv.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/console/tenants", http.StatusSeeOther)
}

// ---- Tokens ----

type tokenRow struct {
	KeyID       string
	Label       string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	Status      string
	StatusClass string
	Revoked     bool
}

type tokensData struct {
	Tenants        []store.Tenant
	SelectedTenant string
	Tokens         []tokenRow
	Reveal         string
}

func (srv *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	srv.showTokens(w, r, http.StatusOK, "", "")
}

// showTokens renders the tokens page for the tenant in ?tenant=. reveal is the
// one-time plaintext to display after an issue, empty otherwise.
func (srv *Server) showTokens(w http.ResponseWriter, r *http.Request, status int, reveal, errMsg string) {
	tenants, err := srv.store.ListTenants(r.Context())
	if err != nil {
		srv.serverError(w, r, err)
		return
	}

	data := tokensData{Tenants: tenants, SelectedTenant: r.FormValue("tenant"), Reveal: reveal}
	if data.SelectedTenant != "" {
		tokens, err := srv.store.ListTokens(r.Context(), data.SelectedTenant)
		if err != nil {
			srv.serverError(w, r, err)
			return
		}
		now := time.Now()
		for _, t := range tokens {
			data.Tokens = append(data.Tokens, toTokenRow(t, now))
		}
	}

	srv.render(w, r, status, "tokens", pageView{
		Title:  "Tokens",
		Active: "tokens",
		Error:  errMsg,
		Data:   data,
	})
}

func toTokenRow(t store.Token, now time.Time) tokenRow {
	row := tokenRow{
		KeyID: t.KeyID, Label: t.Label, CreatedAt: t.CreatedAt,
		LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt,
	}
	switch {
	case t.RevokedAt != nil:
		row.Status, row.StatusClass, row.Revoked = "revoked", "bad", true
	case t.ExpiresAt != nil && !t.ExpiresAt.After(now):
		row.Status, row.StatusClass = "expired", "warn"
	default:
		row.Status, row.StatusClass = "active", "good"
	}
	return row
}

func (srv *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	if !srv.checkCSRF(w, r) {
		return
	}
	tenantID := strings.TrimSpace(r.FormValue("tenant"))
	label := strings.TrimSpace(r.FormValue("label"))
	if tenantID == "" || label == "" {
		srv.showTokens(w, r, http.StatusBadRequest, "", "A tenant and a label are both required.")
		return
	}

	expiresAt, err := parseExpiry(r.FormValue("expires"))
	if err != nil {
		srv.showTokens(w, r, http.StatusBadRequest, "", "Invalid expiry.")
		return
	}

	plaintext, _, err := srv.store.IssueToken(r.Context(), tenantID, label, actor(r), expiresAt)
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	// Can't redirect: the plaintext is shown once and would be lost. Re-render
	// the page directly with the reveal block, exactly what the CLI's
	// shown-once output does in a terminal.
	srv.showTokens(w, r, http.StatusOK, plaintext, "")
}

func (srv *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if !srv.checkCSRF(w, r) {
		return
	}
	keyID := strings.TrimSpace(r.FormValue("key_id"))
	if keyID == "" {
		srv.showTokens(w, r, http.StatusBadRequest, "", "No token specified to revoke.")
		return
	}
	if err := srv.store.RevokeToken(r.Context(), keyID, actor(r)); err != nil {
		srv.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/console/tokens?tenant="+url.QueryEscape(r.FormValue("tenant")), http.StatusSeeOther)
}

// ---- Attributes ----

type attributesData struct {
	Tenants        []store.Tenant
	SelectedTenant string
	Attributes     []store.TenantAttribute
}

func (srv *Server) handleAttributes(w http.ResponseWriter, r *http.Request) {
	srv.showAttributes(w, r, http.StatusOK, "")
}

func (srv *Server) showAttributes(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	tenants, err := srv.store.ListTenants(r.Context())
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	data := attributesData{Tenants: tenants, SelectedTenant: r.FormValue("tenant")}
	if data.SelectedTenant != "" {
		attrs, err := srv.store.ListAttributes(r.Context(), data.SelectedTenant)
		if err != nil {
			srv.serverError(w, r, err)
			return
		}
		data.Attributes = attrs
	}
	srv.render(w, r, status, "attributes", pageView{
		Title:  "Attributes",
		Active: "attributes",
		Error:  errMsg,
		Data:   data,
	})
}

func (srv *Server) handleRegisterAttribute(w http.ResponseWriter, r *http.Request) {
	if !srv.checkCSRF(w, r) {
		return
	}
	tenantID := strings.TrimSpace(r.FormValue("tenant"))
	name := strings.TrimSpace(r.FormValue("name"))
	typ := strings.TrimSpace(r.FormValue("type"))
	if typ == "" {
		typ = "string"
	}
	if tenantID == "" || name == "" {
		srv.showAttributes(w, r, http.StatusBadRequest, "A tenant and an attribute name are both required.")
		return
	}

	if _, err := srv.store.RegisterAttribute(r.Context(), tenantID, name, typ, actor(r)); err != nil {
		switch {
		case errors.Is(err, store.ErrDuplicateAttribute):
			srv.showAttributes(w, r, http.StatusConflict, "That attribute is already registered.")
		case errors.Is(err, store.ErrReservedAttribute):
			srv.showAttributes(w, r, http.StatusBadRequest, "That name is reserved by a core attribute and can't be registered.")
		default:
			srv.serverError(w, r, err)
		}
		return
	}
	http.Redirect(w, r, "/console/attributes?tenant="+url.QueryEscape(tenantID), http.StatusSeeOther)
}

func (srv *Server) handleUnregisterAttribute(w http.ResponseWriter, r *http.Request) {
	if !srv.checkCSRF(w, r) {
		return
	}
	tenantID := strings.TrimSpace(r.FormValue("tenant"))
	name := strings.TrimSpace(r.FormValue("name"))
	if tenantID == "" || name == "" {
		srv.showAttributes(w, r, http.StatusBadRequest, "A tenant and an attribute name are both required.")
		return
	}
	if err := srv.store.UnregisterAttribute(r.Context(), tenantID, name, actor(r)); err != nil {
		srv.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/console/attributes?tenant="+url.QueryEscape(tenantID), http.StatusSeeOther)
}

// ---- Audit ----

func (srv *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	tenants, err := srv.store.ListTenants(r.Context())
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	data := struct {
		Tenants        []store.Tenant
		SelectedTenant string
		Entries        []store.AuditEntry
	}{Tenants: tenants, SelectedTenant: r.FormValue("tenant")}

	if data.SelectedTenant != "" {
		entries, err := srv.store.ListAuditEntries(r.Context(), data.SelectedTenant, auditListLimit)
		if err != nil {
			srv.serverError(w, r, err)
			return
		}
		data.Entries = entries
	}
	srv.render(w, r, http.StatusOK, "audit", pageView{Title: "Audit log", Active: "audit", Data: data})
}

func (srv *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	// Empty tenant lists across every tenant and the system-scope
	// (NULL-tenant) console actions too — the operator's whole-deployment view.
	entries, err := srv.store.ListAdminAuditEntries(r.Context(), "", auditListLimit)
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	srv.render(w, r, http.StatusOK, "admin-audit", pageView{
		Title:  "Admin audit",
		Active: "admin-audit",
		Data:   struct{ Entries []store.AdminAuditEntry }{entries},
	})
}

// ---- ARIA ----

type ariaWindow struct {
	Value string
	Label string
}

var ariaWindows = []ariaWindow{
	{"1h", "Last hour"},
	{"24h", "Last 24 hours"},
	{"7d", "Last 7 days"},
	{"30d", "Last 30 days"},
}

type ariaFlagView struct {
	Kind   string
	Title  string
	Detail string
}

type ariaData struct {
	Tenants        []store.Tenant
	SelectedTenant string
	Windows        []ariaWindow
	SelectedSince  string
	Total          int
	Callers        int
	Truncated      bool
	Flags          []ariaFlagView
}

func (srv *Server) handleARIA(w http.ResponseWriter, r *http.Request) {
	tenants, err := srv.store.ListTenants(r.Context())
	if err != nil {
		srv.serverError(w, r, err)
		return
	}

	since := r.FormValue("since")
	window := parseWindow(since)
	if window == 0 {
		since, window = "24h", 24*time.Hour
	}
	tenantID := r.FormValue("tenant")

	now := time.Now()
	entries, err := srv.store.ListAuditEntriesSince(r.Context(), tenantID, now.Add(-window))
	if err != nil {
		srv.serverError(w, r, err)
		return
	}

	report := aria.Detect(entries, tenantID, now.Add(-window), now, srv.loc)

	data := ariaData{
		Tenants:        tenants,
		SelectedTenant: tenantID,
		Windows:        ariaWindows,
		SelectedSince:  since,
		Total:          report.Total,
		Callers:        report.Callers,
		Truncated:      report.Truncated,
	}
	for _, f := range report.Flags {
		data.Flags = append(data.Flags, toFlagView(f, srv.loc))
	}

	srv.render(w, r, http.StatusOK, "aria", pageView{Title: "ARIA", Active: "aria", Data: data})
}

// toFlagView renders one deterministic flag into title/detail text. It leans
// on ARIA's own Header/summary vocabulary so the console and the CLI describe
// the same finding the same way.
func toFlagView(f aria.Flag, loc *time.Location) ariaFlagView {
	v := ariaFlagView{Kind: string(f.Kind)}
	switch f.Kind {
	case aria.FlagBulkDeactivation:
		v.Title = "Bulk deactivation"
		v.Detail = strconv.Itoa(f.Count) + " deactivations clustered between " +
			f.WindowStart.In(loc).Format(time.RFC3339) + " and " + f.WindowEnd.In(loc).Format(time.RFC3339) +
			byActor(f.Actor)
	case aria.FlagDenialBurst:
		v.Title = "Burst of refusals"
		v.Detail = strconv.Itoa(f.Count) + " denied calls" + byActor(f.Actor)
	case aria.FlagOffHours:
		v.Title = "Off-hours activity"
		v.Detail = strconv.Itoa(f.Count) + " changes outside business hours" + byActor(f.Actor)
	case aria.FlagHighVolume:
		v.Title = "High call volume"
		v.Detail = strconv.Itoa(f.Count) + " calls" + byActor(f.Actor)
	default:
		v.Title = string(f.Kind)
		v.Detail = strconv.Itoa(f.Count) + " occurrences" + byActor(f.Actor)
	}
	return v
}

func byActor(actor string) string {
	if actor == "" {
		return "."
	}
	return " from caller " + actor + "."
}

// ---- helpers ----

func (srv *Server) checkCSRF(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not parse form", http.StatusBadRequest)
		return false
	}
	if !srv.csrf.valid(r.PostFormValue("csrf"), time.Now()) {
		http.Error(w, "invalid or expired form token — reload the page and try again", http.StatusForbidden)
		return false
	}
	return true
}

// parseExpiry mirrors the admin CLI: a bare day count ("90d") or anything
// time.ParseDuration understands; empty means no expiry.
func parseExpiry(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return nil, err
		}
		t := time.Now().Add(time.Duration(n) * 24 * time.Hour)
		return &t, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, err
	}
	t := time.Now().Add(d)
	return &t, nil
}

// parseWindow accepts the same "Nd"/duration forms as parseExpiry but returns
// a bare duration; 0 means unrecognised, which the caller defaults.
func parseWindow(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0
		}
		return time.Duration(n) * 24 * time.Hour
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// envTimezone loads ARIA_TIMEZONE if set and valid, else nil.
func envTimezone() *time.Location {
	tz := strings.TrimSpace(os.Getenv("ARIA_TIMEZONE"))
	if tz == "" {
		return nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil
	}
	return loc
}
