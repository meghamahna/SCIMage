---
id: module-console
kind: module
title: Admin console (internal/console)
summary: Opt-in loopback web UI for the SCIMage operator on its own listener; reuses store calls, token-authenticated, with security headers and a strict CSP
tags: [console, admin console, admin ui, web ui, dashboard, operator, CONSOLE_ADDR, templates, ARIA, webhooks, CSP, security headers]
links: [depends_on:module-store, depends_on:module-scim, related:module-console-auth, related:api-console-routes, decided_by:decision-console-separate-listener, decided_by:decision-console-token-csrf, related:module-apidocs]
sources: [internal/console/server.go:1-16, internal/console/server.go:35-62, internal/console/server.go:64-98, internal/console/server.go:114-165, internal/console/render.go:38-43, internal/console/render.go:70-76, internal/console/static.go:12-32, internal/console/static.go:67-86, internal/console/handlers.go:792-816, cmd/server/main.go:36-56, cmd/server/main.go:145-167]
updated: 2026-11-26
verified_commit: 794ebee
---
## Facts
- Purpose: single-operator admin UI (tenants, tokens, attributes, audit, admin audit, webhooks, ARIA). Not a tenant self-service portal → internal/console/server.go:1-16
- Started only when CONSOLE_ADDR is set; otherwise consoleServer returns nil → cmd/server/main.go:36-44
- Runs as a second http.Server beside the SCIM listener, both started in run() → cmd/server/main.go:150-167
- Server struct holds store, parsed templates, csrf guard, env label (SCIMAGE_ENV), ARIA timezone, SCIM_BASE_URL, narrative cache → internal/console/server.go:64-72
- NewServer parses templates and mints a per-process CSRF key; errors are returned, not panicked → internal/console/server.go:77-98
- Store is an interface (ConsoleTokenStore plus data methods) satisfied by *store.Store; handlers reuse store.* so audit-in-transaction is inherited → internal/console/server.go:35-62
- Mutations call store functions with actor "console:<key_id>" → internal/console/render.go:74-76
- Templates: embedded and parsed once at startup, one layout plus page templates → internal/console/static.go:16-17, internal/console/static.go:37-39
- Each page renders into a buffer, then into the layout, so a template error yields a clean 500 → internal/console/render.go:31-63
- Shared chrome (identity, CSRF token, ARIA-enabled flag, env label) is set in render() → internal/console/render.go:38-43
- Handler(): /console/static/ mounts before auth; all other routes go through requireConsoleToken → internal/console/server.go:114-143
- Security headers wrap the root handler, so 401s, mux 404/405s and errors carry them → internal/console/server.go:151-155, internal/console/server.go:114-143
- Headers set on every response: X-Frame-Options DENY, a strict CSP, nosniff, Referrer-Policy same-origin, Cache-Control no-store → internal/console/server.go:156-164
- CSP (consoleCSP): default-src, form-action, base-uri, object-src and frame-src limited to 'self' or none; no unsafe-inline → internal/console/server.go:148-149
- Referrer-Policy is same-origin, not no-referrer, so same-origin form POSTs keep a real Origin header → internal/console/server.go:161-163
- Static assets (CSS, JS, logo) are embedded and served without auth; cache is public, max-age 3600, only for real files → internal/console/static.go:13-14, internal/console/static.go:67-86
- Missing files and directories under /console/static/ get a plain 404 and no listing → internal/console/static.go:76-80
- One ?v= hash covers both console.css and console.js → internal/console/static.go:19-32, internal/console/templates/layout.html:10
- Narrative (LLM briefing) cache is in memory, keyed by tenant and window → internal/console/handlers.go:792-816
## Gotchas
- Console startup fails if template parse or CSRF key fails; it does not start a broken console → internal/console/server.go:77-98
- Mutating routes call checkCSRF before touching the store (7 sites) → internal/console/handlers.go:851-870
- Templates and pages must not contain inline script, style attributes or on* handlers; the CSP would block them, and tests enforce it → internal/console/headers_test.go:114-150
