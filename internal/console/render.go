package console

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/meghamahna/SCIMage/internal/aria"
)

// pageView is the envelope every page renders inside. Data carries the
// page-specific payload; the rest is chrome shared by the layout (nav
// highlight, who's signed in, the CSRF token, an optional error banner).
type pageView struct {
	Title       string
	Active      string
	Identity    consoleIdentity
	EnvLabel    string
	CSRF        string
	Error       string
	RenderedAt  time.Time // when this page was served, shown in the refresh bar
	ARIAEnabled bool      // whether ARIA_LLM_* is configured; hides the nav entry and gates the briefing UI
	Body        template.HTML
	Data        any
}

// render executes the named page template into the shared layout. It's a
// two-pass render — page into a buffer, then the buffer into the layout — so
// each page can define its own top-level template without the name collisions
// a single shared "content" block would cause.
//
// The page is rendered to a buffer first so a template error produces a clean
// 500 instead of a half-written 200 with a broken body.
func (srv *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, view pageView) {
	view.Identity = identityFrom(r.Context())
	view.EnvLabel = srv.env
	view.CSRF = srv.csrf.token(time.Now())
	view.RenderedAt = time.Now()
	view.ARIAEnabled = aria.Enabled()

	var body bytes.Buffer
	if err := srv.tmpl.ExecuteTemplate(&body, page, view); err != nil {
		srv.serverError(w, r, fmt.Errorf("render page %q: %w", page, err))
		return
	}
	view.Body = template.HTML(body.String())

	var full bytes.Buffer
	if err := srv.tmpl.ExecuteTemplate(&full, "layout", view); err != nil {
		srv.serverError(w, r, fmt.Errorf("render layout: %w", err))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := full.WriteTo(w); err != nil {
		slog.Error("write console response", "error", err)
	}
}

func (srv *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("console handler", "path", r.URL.Path, "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// actor is who an admin-audited mutation is attributed to: the console
// credential it came through. The key id is non-secret and stable, so it names
// the acting credential without leaking anything, and the "console:" prefix
// distinguishes a console action from the same store call run at the CLI.
func actor(r *http.Request) string {
	return "console:" + identityFrom(r.Context()).KeyID
}

// writeJSON encodes v as the response body. It's used by the small JSON
// endpoints the Webhooks page's Replay button talks to (replay and
// delivery-status), which return data rather than a rendered page.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write console json", "error", err)
	}
}

// jsonServerError logs like serverError but answers the JSON callers with a
// JSON body, so a fetch sees a shape it can parse rather than an HTML error page.
func (srv *Server) jsonServerError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("console handler", "path", r.URL.Path, "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal server error"})
}
