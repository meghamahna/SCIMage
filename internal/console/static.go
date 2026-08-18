package console

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"io/fs"
	"net/http"
	"time"
)

//go:embed assets/console.css assets/scimage_logo.png
var assetsFS embed.FS

//go:embed templates/*.html
var templatesFS embed.FS

// cssVersion is a short content hash of console.css, appended to its <link> as
// ?v=… so a new build serves a fresh URL. Without it the browser holds the
// cached stylesheet for the full max-age and misses CSS changes.
var cssVersion = func() string {
	b, err := assetsFS.ReadFile("assets/console.css")
	if err != nil {
		return "0"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}()

// parseTemplates loads every page template into one set, sharing the funcs
// below. Parsed once at startup so a broken template fails fast rather than on
// first request.
func parseTemplates() (*template.Template, error) {
	return template.New("console").Funcs(templateFuncs).ParseFS(templatesFS, "templates/*.html")
}

var templateFuncs = template.FuncMap{
	// rfc3339 renders a timestamp the way the CLI's tabwriter output does, so
	// the console and scimage-admin describe the same instant identically.
	"rfc3339": func(t time.Time) string { return t.Format(time.RFC3339) },
	"rfc3339p": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.Format(time.RFC3339)
	},
	"dash": func(s string) string {
		if s == "" {
			return "—"
		}
		return s
	},
	// hms is a compact wall-clock time for the "Last refreshed at" caption,
	// where the date is implied by the operator watching the page live.
	"hms": func(t time.Time) string { return t.Format("15:04:05 MST") },
	// assetver is the cache-busting suffix for the stylesheet link.
	"assetver": func() string { return cssVersion },
}

// staticHandler serves the embedded CSS and logo under /static/. These carry
// no secrets and are cheap and cacheable, so they mount before the auth
// middleware — the browser's login dialog page needs the stylesheet to render.
func staticHandler() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// Only reachable if the embed directive and this path disagree, which
		// is a build-time mistake, not a runtime condition.
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fileServer.ServeHTTP(w, r)
	})
}
