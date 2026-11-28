---
id: module-console
kind: module
title: Admin console (internal/console)
summary: Opt-in loopback web UI for the SCIMage operator on its own listener; reuses store calls, Host-allowlisted and token-authenticated, with security headers, a strict CSP and capped POST bodies
tags: [console, admin console, admin ui, web ui, dashboard, operator, CONSOLE_ADDR, CONSOLE_ALLOWED_HOSTS, host allowlist, 421, 413, body limit, templates, ARIA, webhooks, CSP, security headers]
links: [depends_on:module-store, depends_on:module-scim, related:module-console-auth, related:api-console-routes, decided_by:decision-console-separate-listener, decided_by:decision-console-token-csrf, decided_by:decision-console-host-allowlist, related:module-apidocs, documented_in:doc-architecture]
sources: [internal/console/server.go:1-16, internal/console/server.go:35-63, internal/console/server.go:64-112, internal/console/server.go:121-176, internal/console/host.go:164-172, internal/console/render.go:38-43, internal/console/render.go:70-76, internal/console/static.go:12-32, internal/console/static.go:67-86, internal/console/handlers.go:29-32, internal/console/handlers.go:798-833, cmd/server/main.go:36-62, cmd/server/main.go:145-160, docs/ARCHITECTURE.md:442-498]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Facts
- Purpose: single-operator admin UI (tenants, tokens, attributes, audit, admin audit, webhooks, ARIA). Not a tenant self-service portal → internal/console/server.go:1-16, docs/ARCHITECTURE.md:442-446
- Started only when CONSOLE_ADDR is set; consoleServer returns nil otherwise → cmd/server/main.go:41-50
- Runs as a second http.Server beside the SCIM listener; both are started from run() → cmd/server/main.go:145-160
- Server struct holds store, templates, CSRF guard, env label, ARIA timezone, SCIM_BASE_URL, narrative cache and the allowed Host set → internal/console/server.go:64-75
- NewServer reads the Host allowlist, parses templates and mints a per-process CSRF key; errors are returned, not panicked → internal/console/server.go:81-112
- Store is an interface (ConsoleTokenStore plus data methods) satisfied by *store.Store; handlers reuse store.* so audit-in-transaction is inherited → internal/console/server.go:35-63, docs/ARCHITECTURE.md:496-498
- Mutations call store functions with actor "console:<key_id>" → internal/console/render.go:74-76
- Templates are embedded and parsed once at startup → internal/console/static.go:16-17, internal/console/static.go:37-39
- Each page renders into a buffer, then into the layout, so a template error yields a clean 500 → internal/console/render.go:31-63
- Shared chrome (identity, CSRF token, ARIA-enabled flag, env label) is set in render() → internal/console/render.go:38-43
- Middleware order: security headers, Host allowlist (421), static assets, console auth, router, handler → docs/ARCHITECTURE.md:449-468
- Host check wraps the whole root handler, static assets included → internal/console/server.go:153, internal/console/host.go:164-172
- Handler(): /console/static/ mounts before auth; all other routes go through requireConsoleToken → internal/console/server.go:148-151
- Security headers wrap the Host check, so 421s, 401s and mux 404/405s all carry them → internal/console/server.go:153, internal/console/server.go:166-176
- Headers on every response: X-Frame-Options DENY, strict CSP, nosniff, Referrer-Policy same-origin, Cache-Control no-store → internal/console/server.go:169-175
- CSP (consoleCSP): default-src, form-action, base-uri, object-src and frame-src limited to 'self' or none; no unsafe-inline → internal/console/server.go:159-160
- Static assets (CSS, JS, logo) are embedded and served without auth; cache is public, max-age 3600, only for real files → internal/console/static.go:13-14, internal/console/static.go:67-86
- Missing files and directories under /console/static/ get a plain 404 and no listing → internal/console/static.go:76-80
- One ?v= hash covers both console.css and console.js → internal/console/static.go:19-32, internal/console/templates/layout.html:10
- POST bodies are capped at 64 KiB (maxFormBytes); an oversize body gets 413 → internal/console/handlers.go:29-32, internal/console/handlers.go:880-886
- Both listeners set MaxHeaderBytes to 64 KiB, not the 1 MiB default → cmd/server/main.go:36-39, cmd/server/main.go:60, cmd/server/main.go:149
- Both listeners set ReadHeaderTimeout, ReadTimeout and IdleTimeout; only the SCIM listener sets WriteTimeout, since the ARIA page can wait up to a minute on an LLM call → docs/ARCHITECTURE.md:491-494
- Narrative (LLM briefing) cache is in memory, keyed by tenant and window → internal/console/handlers.go:798-833
## Gotchas
- Startup fails if template parse, CSRF key or CONSOLE_ALLOWED_HOSTS fails; the console never starts broken → internal/console/server.go:81-112
- Mutating routes call checkCSRF before touching the store (7 call sites) → internal/console/handlers.go:111, 202, 230, 281, 310, 430, 728
- Templates and pages must not contain inline script, style attributes or on* handlers; the CSP would block them, and tests enforce it → internal/console/headers_test.go:114-150
