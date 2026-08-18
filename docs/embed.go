// Package docs serves the hand-written OpenAPI spec and a vendored Swagger UI
// at /docs. Both are embedded in the binary, so the interactive API reference
// works on a deployment with no outbound network — no CDN, no runtime fetch.
//
// It is served unauthenticated, unlike the console: the spec describes a
// public protocol (SCIM 2.0) and carries no tenant data or secrets, so an
// integrator can read it before they have a token. The endpoints it documents
// still require one.
package docs

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed openapi.yaml
var specFS embed.FS

//go:embed swaggerui/index.html swaggerui/swagger-ui.css swaggerui/swagger-ui-bundle.js
var uiFS embed.FS

// Handler serves the Swagger UI and the spec under /docs/. Mount it with the
// "/docs" prefix stripped — no trailing slash, so a stripped path keeps its
// leading slash (".../docs/openapi.yaml" -> "/openapi.yaml") and the inner
// mux matches it instead of issuing a clean-path redirect:
//
//	root.Handle("/docs/", http.StripPrefix("/docs", docs.Handler()))
//	root.Handle("/docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
//
// The UI page fetches ./openapi.yaml relative to itself, so the spec is
// exposed at /docs/openapi.yaml alongside the assets.
func Handler() http.Handler {
	ui, err := fs.Sub(uiFS, "swaggerui")
	if err != nil {
		// Only reachable if the embed path and this string disagree — a
		// build-time mistake, not a runtime condition.
		panic(err)
	}

	mux := http.NewServeMux()
	// The spec is embedded at its repo path (docs/openapi.yaml); serve it at
	// the flat name the UI expects.
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		b, err := specFS.ReadFile("openapi.yaml")
		if err != nil {
			http.Error(w, "spec unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(b)
	})

	// Everything else (index.html at the root, plus the two assets) comes from
	// the embedded UI tree.
	fileServer := http.FileServer(http.FS(ui))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fileServer.ServeHTTP(w, r)
	}))
	return mux
}
