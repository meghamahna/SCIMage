package console

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"
)

// pageView is the envelope every page renders inside. Data carries the
// page-specific payload; the rest is chrome shared by the layout (nav
// highlight, who's signed in, the CSRF token, an optional error banner).
type pageView struct {
	Title    string
	Active   string
	Identity consoleIdentity
	EnvLabel string
	CSRF     string
	Error    string
	Body     template.HTML
	Data     any
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
