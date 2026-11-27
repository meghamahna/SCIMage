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

//go:embed assets/console.css assets/console.js assets/scimage_logo.png
var assetsFS embed.FS

//go:embed templates/*.html
var templatesFS embed.FS

// assetVersion is a short content hash of console.css and console.js, appended
// to their URLs as ?v=… so a new build serves fresh URLs. Without it the
// browser holds the cached files for the full max-age and misses changes.
var assetVersion = func() string {
	h := sha256.New()
	for _, name := range []string{"assets/console.css", "assets/console.js"} {
		b, err := assetsFS.ReadFile(name)
		if err != nil {
			return "0"
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
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
	// assetver is the cache-busting suffix for the stylesheet and script URLs.
	"assetver": func() string { return assetVersion },
}

// staticHandler serves the embedded CSS, script and logo under /static/.
// These carry no secrets and are cheap and cacheable, so they mount before
// the auth middleware; the browser's login dialog page needs the stylesheet.
func staticHandler() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// Only reachable if the embed directive and this path disagree, which
		// is a build-time mistake, not a runtime condition.
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only real files are cacheable. Misses and the directory itself get a
		// plain 404 that keeps the no-store default, and no listing.
		info, err := fs.Stat(sub, r.URL.Path)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fileServer.ServeHTTP(w, r)
	})
}
