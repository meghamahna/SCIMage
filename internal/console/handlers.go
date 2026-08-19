package console

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/meghamahna/SCIMage/internal/aria"
	"github.com/meghamahna/SCIMage/internal/store"
	"github.com/meghamahna/SCIMage/internal/webhook"
)

// auditListLimit caps how many rows the audit views pull. The store clamps to
// its own MaxPageSize regardless; this keeps a page from rendering an
// unmanageable table when that cap is large.
const auditListLimit = 200

// ---- Home ----

type homeData struct {
	TenantCount     int
	BaseURL         string // scim base URL pattern, with a placeholder tenant id
	BasePlaceholder bool   // SCIM_BASE_URL is unset, so the host is a placeholder
	Webhook         webhook.Status
}

// handleHome is the console landing page: what SCIMage is, the SCIM base URL an
// operator hands an IdP, and a few live signals (tenant count, webhook status)
// with quick links into each section. It replaces the old bare redirect to the
// tenants page.
func (srv *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	tenants, err := srv.store.ListTenants(r.Context())
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	srv.render(w, r, http.StatusOK, "home", pageView{
		Title:  "Home",
		Active: "home",
		Data: homeData{
			TenantCount:     len(tenants),
			BaseURL:         srv.tenantBaseURL("{tenantId}"),
			BasePlaceholder: srv.scimBase == "",
			Webhook:         webhook.StatusFromEnv(),
		},
	})
}

// ---- Tenants ----

type tenantRow struct {
	store.Tenant
	BaseURL string // the SCIM base URL an IdP points at, derived not stored
}

func (srv *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	srv.showTenants(w, r, http.StatusOK, "")
}

func (srv *Server) showTenants(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	tenants, err := srv.store.ListTenants(r.Context())
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	rows := make([]tenantRow, len(tenants))
	for i, t := range tenants {
		rows[i] = tenantRow{Tenant: t, BaseURL: srv.tenantBaseURL(t.ID)}
	}
	srv.render(w, r, status, "tenants", pageView{
		Title:  "Tenants",
		Active: "tenants",
		Error:  errMsg,
		Data: struct {
			Tenants         []tenantRow
			BasePlaceholder bool
		}{rows, srv.scimBase == ""},
	})
}

// tenantBaseURL mirrors internal/scim.Handler.baseURL and the admin CLI: the
// URL is never stored, only derived from the deployment's SCIM_BASE_URL plus the
// tenant id, so it can't go stale if the operator later changes domains. When
// SCIM_BASE_URL is unset it shows a placeholder, the same as the CLI, so the
// path shape is still clear.
func (srv *Server) tenantBaseURL(tenantID string) string {
	root := srv.scimBase
	if root == "" {
		root = "<SCIM_BASE_URL>"
	}
	return root + "/scim/v2/" + tenantID
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

// ---- Webhooks ----

// deadLetterLimit caps the parked-delivery list on the webhooks page. These are
// the ones needing a human, so a small recent window is what's useful; the full
// set is a store query away if it's ever needed.
const deadLetterLimit = 50

// pendingLimit caps the queued-delivery list the same way: enough to show what's
// currently in flight (including anything just replayed) without the page
// growing unbounded during a real backlog.
const pendingLimit = 50

type webhookData struct {
	Status      webhook.Status
	Summary     store.WebhookSummary
	DeadLetters []store.Delivery
	Pending     []store.Delivery
}

// handleWebhooks shows change-event delivery: the configured endpoint
// (secret-free), queue health, and any parked deliveries. The endpoint itself
// is configured through SCIM_WEBHOOK_* at startup, not here — the only mutation
// on this page is replaying a parked delivery (handleReplayDeadLetter).
func (srv *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	srv.showWebhooks(w, r, http.StatusOK, "")
}

func (srv *Server) showWebhooks(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	summary, err := srv.store.WebhookDeliverySummary(r.Context())
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	deadLetters, err := srv.store.DeadLetters(r.Context(), deadLetterLimit)
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	pending, err := srv.store.PendingDeliveries(r.Context(), pendingLimit)
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	srv.render(w, r, status, "webhooks", pageView{
		Title:  "Webhooks",
		Active: "webhooks",
		Error:  errMsg,
		Data: webhookData{
			Status:      webhook.StatusFromEnv(),
			Summary:     summary,
			DeadLetters: deadLetters,
			Pending:     pending,
		},
	})
}

// handleReplayDeadLetter returns one parked delivery to the queue. The store
// flips it back to pending and records the replay in the admin audit log, in one
// transaction, attributed to this console credential.
//
// It answers JSON when the caller asks for it (the Webhooks page's Replay
// button, which then polls delivery-status), and otherwise redirects — so the
// same route works with JavaScript off.
func (srv *Server) handleReplayDeadLetter(w http.ResponseWriter, r *http.Request) {
	if !srv.checkCSRF(w, r) {
		return
	}
	wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")

	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil {
		if wantsJSON {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid delivery id"})
			return
		}
		srv.showWebhooks(w, r, http.StatusBadRequest, "That isn't a valid delivery id.")
		return
	}

	// Refused rather than requeued: with no dispatcher running, a replayed
	// delivery would sit as pending with no dispatcher to pick it up. Checked
	// against the delivery's real status first (not just short-circuited on
	// "webhooks are off") so an unknown or already-handled id still answers 404,
	// the same as it would with webhooks on.
	if !webhook.StatusFromEnv().Enabled {
		status, err := srv.store.WebhookDeliveryStatus(r.Context(), id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			if wantsJSON {
				srv.jsonServerError(w, r, err)
				return
			}
			srv.serverError(w, r, err)
			return
		}
		if status == store.DeliveryDeadLetter {
			const msg = "Webhooks are turned off, so a replayed delivery would have nothing to send it. Turn them on, then replay."
			if wantsJSON {
				writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": msg})
				return
			}
			srv.showWebhooks(w, r, http.StatusConflict, msg)
			return
		}
	}

	if err := srv.store.ReplayDeadLetter(r.Context(), id, actor(r)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if wantsJSON {
				writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "no longer parked"})
				return
			}
			srv.showWebhooks(w, r, http.StatusNotFound, "That delivery is no longer parked — it may already have been replayed.")
			return
		}
		if wantsJSON {
			srv.jsonServerError(w, r, err)
			return
		}
		srv.serverError(w, r, err)
		return
	}

	if wantsJSON {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	http.Redirect(w, r, "/console/webhooks", http.StatusSeeOther)
}

// handleDeliveryStatus reports one delivery's current status as JSON, so the
// Replay button can poll a requeued delivery until it lands (delivered) or parks
// again (dead_letter). Read-only, so no CSRF. A missing row reports "unknown".
func (srv *Server) handleDeliveryStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid delivery id"})
		return
	}
	status, err := srv.store.WebhookDeliveryStatus(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"status": "unknown"})
			return
		}
		srv.jsonServerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status})
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
	Narrative      template.HTML // cached LLM briefing, empty until generated
	NarrativeAt    time.Time
	NarrativeError string
	TotalDelta     *deltaView // entries this window vs the previous one; nil when not comparable
	CallersDelta   *deltaView
}

// deltaView is a window-over-window change badge: a color class and a short
// label like "+12% ↑". It is only ever built from real counts of two adjacent
// windows — never a fabricated trend.
type deltaView struct {
	Class string
	Label string
}

// computeDelta compares this window's count against the previous window's.
// It returns nil when there's nothing honest to show (both windows empty).
func computeDelta(cur, prev int) *deltaView {
	switch {
	case prev == 0 && cur == 0:
		return nil
	case prev == 0:
		return &deltaView{Class: "up", Label: "new"}
	default:
		pct := float64(cur-prev) / float64(prev) * 100
		switch {
		case pct > 0:
			return &deltaView{Class: "up", Label: fmt.Sprintf("+%.0f%% ↑", pct)}
		case pct < 0:
			return &deltaView{Class: "down", Label: fmt.Sprintf("%.0f%% ↓", pct)}
		default:
			return &deltaView{Class: "flat", Label: "0%"}
		}
	}
}

// ariaInputs is the recomputed ARIA read that the GET page and the POST narrate
// action both start from, so the two agree on the same window and report.
type ariaInputs struct {
	tenants  []store.Tenant
	tenantID string
	since    string
	report   aria.Report
}

func (srv *Server) ariaInputs(r *http.Request) (ariaInputs, error) {
	tenants, err := srv.store.ListTenants(r.Context())
	if err != nil {
		return ariaInputs{}, err
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
		return ariaInputs{}, err
	}

	report := aria.Detect(entries, tenantID, now.Add(-window), now, srv.loc)
	return ariaInputs{tenants: tenants, tenantID: tenantID, since: since, report: report}, nil
}

func (srv *Server) handleARIA(w http.ResponseWriter, r *http.Request) {
	in, err := srv.ariaInputs(r)
	if err != nil {
		srv.serverError(w, r, err)
		return
	}
	srv.renderARIA(w, r, http.StatusOK, in, "")
}

// assembleARIAData builds the view for the ARIA page from a recomputed report,
// attaching the last cached narrative (if any) and an optional narration error.
// The stats are always live; a cached briefing carries its own generated-at
// timestamp so an operator can see how fresh it is.
func (srv *Server) assembleARIAData(in ariaInputs, narrativeErr string) ariaData {
	data := ariaData{
		Tenants:        in.tenants,
		SelectedTenant: in.tenantID,
		Windows:        ariaWindows,
		SelectedSince:  in.since,
		Total:          in.report.Total,
		Callers:        in.report.Callers,
		Truncated:      in.report.Truncated,
		NarrativeError: narrativeErr,
	}
	for _, f := range in.report.Flags {
		data.Flags = append(data.Flags, toFlagView(f, srv.loc))
	}
	if e, ok := srv.narratives.get(narrativeKey(in.tenantID, in.since)); ok {
		data.Narrative = renderNarrative(e.text)
		data.NarrativeAt = e.generatedAt
	}
	return data
}

func (srv *Server) renderARIA(w http.ResponseWriter, r *http.Request, status int, in ariaInputs, narrativeErr string) {
	data := srv.assembleARIAData(in, narrativeErr)
	srv.attachARIADeltas(r.Context(), in, &data)
	srv.render(w, r, status, "aria", pageView{Title: "ARIA", Active: "aria", Data: data})
}

// attachARIADeltas sets the entries/callers change badges by counting this
// window against the one immediately before it. The counts are real SQL counts;
// deltas are cosmetic, so any error just leaves the badges off rather than
// failing the page. Skipped when the window was truncated, since the displayed
// count is capped and a comparison would mislead.
func (srv *Server) attachARIADeltas(ctx context.Context, in ariaInputs, data *ariaData) {
	if in.report.Truncated {
		return
	}
	curStart, curEnd := in.report.Since, in.report.Now
	window := curEnd.Sub(curStart)
	if window <= 0 {
		return
	}
	prevStart := curStart.Add(-window)

	curTotal, curCallers, err := srv.store.AuditWindowStats(ctx, in.tenantID, curStart, curEnd)
	if err != nil {
		slog.Error("aria delta: current window", "error", err)
		return
	}
	prevTotal, prevCallers, err := srv.store.AuditWindowStats(ctx, in.tenantID, prevStart, curStart)
	if err != nil {
		slog.Error("aria delta: previous window", "error", err)
		return
	}
	data.TotalDelta = computeDelta(curTotal, prevTotal)
	data.CallersDelta = computeDelta(curCallers, prevCallers)
}

// renderARIABrief renders just the AI-briefing block, for the fetch that the
// Generate/Refresh button makes — so the briefing updates in place and the URL
// stays on /console/aria instead of navigating to /aria/narrate. It's buffered
// so a template error is a clean 500, not a half-written fragment.
func (srv *Server) renderARIABrief(w http.ResponseWriter, r *http.Request, in ariaInputs, narrativeErr string) {
	view := pageView{CSRF: srv.csrf.token(time.Now()), Data: srv.assembleARIAData(in, narrativeErr)}
	var buf bytes.Buffer
	if err := srv.tmpl.ExecuteTemplate(&buf, "aria-brief", view); err != nil {
		srv.serverError(w, r, fmt.Errorf("render aria-brief: %w", err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := buf.WriteTo(w); err != nil {
		slog.Error("write aria brief", "error", err)
	}
}

// respondARIA answers the narrate action either as the briefing fragment (the
// button's fetch) or a full page re-render (the no-JavaScript form fallback).
func (srv *Server) respondARIA(w http.ResponseWriter, r *http.Request, fragment bool, in ariaInputs, narrativeErr string) {
	if fragment {
		srv.renderARIABrief(w, r, in, narrativeErr)
		return
	}
	srv.renderARIA(w, r, http.StatusOK, in, narrativeErr)
}

// handleARIANarrate produces the plain-English briefing over the flagged
// signals. It's advisory only: the narrative is displayed to the operator and
// never re-enters the store or the auth path, so this endpoint can't influence
// a provisioning or authorization decision. A hit on the per-tenant+window
// cache short-circuits the paid LLM call unless force=1 asks for a refresh.
func (srv *Server) handleARIANarrate(w http.ResponseWriter, r *http.Request) {
	if !srv.checkCSRF(w, r) {
		return
	}
	// The button fetches with X-Requested-With and gets just the briefing block
	// back; a plain form POST (no JavaScript) gets the whole page re-rendered.
	fragment := r.Header.Get("X-Requested-With") != ""

	in, err := srv.ariaInputs(r)
	if err != nil {
		srv.serverError(w, r, err)
		return
	}

	// A quiet window has nothing to narrate; don't spend an LLM call on it.
	if !in.report.HasFindings() {
		srv.respondARIA(w, r, fragment, in, "")
		return
	}

	key := narrativeKey(in.tenantID, in.since)
	if r.PostFormValue("force") != "1" {
		if _, ok := srv.narratives.get(key); ok {
			srv.respondARIA(w, r, fragment, in, "")
			return
		}
	}

	text, err := srv.narrate(r.Context(), in.report)
	if err != nil {
		// An LLM outage shouldn't 500 the console, and its detail (upstream
		// error text, missing config names) shouldn't reach the operator's
		// screen either. Log it and show a generic status instead.
		slog.Error("aria narrate", "error", err)
		srv.respondARIA(w, r, fragment, in, "Couldn't generate a briefing. The LLM is currently unavailable.")
		return
	}
	srv.narratives.put(key, text)
	srv.respondARIA(w, r, fragment, in, "")
}

// narrate runs ARIA's LLM pass over an already-computed report: it reads the
// LLM config from the environment and asks the model to narrate the findings.
// The prompt carries only the deterministic facts; the model adds phrasing, not
// signals.
func (srv *Server) narrate(ctx context.Context, report aria.Report) (string, error) {
	cfg, err := aria.ConfigFromEnv()
	if err != nil {
		return "", err
	}
	system, user := aria.BuildPrompt(report)
	return aria.NewClient(cfg).Summarize(ctx, system, user)
}

// boldRE matches the model's **label:** headers. It runs on already-escaped
// text (see renderNarrative), so its capture can never carry live markup.
var boldRE = regexp.MustCompile(`\*\*(.+?)\*\*`)

// renderNarrative turns ARIA's Markdown-ish briefing into safe HTML. The text
// is HTML-escaped first, so the only markup emitted is the <strong> added for
// the model's bold labels; newlines are kept by white-space:pre-wrap in the
// stylesheet. The LLM's output is never trusted as raw HTML.
func renderNarrative(s string) template.HTML {
	escaped := template.HTMLEscapeString(s)
	escaped = boldRE.ReplaceAllString(escaped, "<strong>$1</strong>")
	return template.HTML(escaped)
}

// narrativeCache holds the last briefing per tenant+window so re-opening the
// ARIA page or re-clicking Generate doesn't spend a paid LLM call every time.
// This is a single-operator loopback console, so a process-lifetime in-memory
// map is enough; a refresh (force=1) bypasses it.
type narrativeCache struct {
	mu    sync.Mutex
	items map[string]narrativeEntry
}

type narrativeEntry struct {
	text        string
	generatedAt time.Time
}

func newNarrativeCache() *narrativeCache {
	return &narrativeCache{items: make(map[string]narrativeEntry)}
}

func (c *narrativeCache) get(key string) (narrativeEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	return e, ok
}

func (c *narrativeCache) put(key, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = narrativeEntry{text: text, generatedAt: time.Now()}
}

// narrativeKey pairs a tenant filter with a window. The NUL separator can't
// appear in either value, so distinct pairs can't collide.
func narrativeKey(tenantID, since string) string {
	return tenantID + "\x00" + since
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
