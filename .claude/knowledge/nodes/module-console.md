---
id: module-console
kind: module
title: Admin console (internal/console)
summary: Opt-in loopback web UI for the SCIMage operator on its own listener; reuses store.* calls, token-authenticated
tags: [console, admin console, admin ui, web ui, dashboard, operator, CONSOLE_ADDR, templates, ARIA, webhooks]
links: [depends_on:module-store, depends_on:module-scim, related:module-console-auth, related:api-console-routes, decided_by:decision-console-separate-listener, decided_by:decision-console-token-csrf, related:module-apidocs]
sources: [internal/console/server.go:1-143, internal/console/render.go:1-94, internal/console/static.go:1-76, cmd/server/main.go:36-56, cmd/server/main.go:145-167]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Purpose: single-operator admin UI (tenants, tokens, attributes, audit, admin audit, webhooks, ARIA). Not a tenant self-service portal → internal/console/server.go:1-16
- Started only when CONSOLE_ADDR is set; otherwise consoleServer returns nil → cmd/server/main.go:36-44
- Runs as a second http.Server beside the SCIM listener, both started in run() → cmd/server/main.go:150-167
- Server struct holds store, parsed templates, csrf guard, env label (SCIMAGE_ENV), ARIA timezone, SCIM_BASE_URL, narrative cache → internal/console/server.go:64-72
- NewServer parses templates and mints a per-process CSRF key; errors are returned, not panicked → internal/console/server.go:74-98
- Store is an interface (ConsoleTokenStore plus data methods) satisfied by *store.Store; handlers reuse store.* so audit-in-transaction is inherited → internal/console/server.go:31-59, internal/console/server.go:9-16
- Mutations call store functions with actor "console:<key_id>" → internal/console/render.go:70-76
- Templates: 10 HTML files embedded and parsed once at startup, one layout plus page templates → internal/console/static.go:16-17, internal/console/static.go:31-36
- Each page renders into a buffer, then into the layout, so a template error yields a clean 500 → internal/console/render.go:31-63
- Shared chrome (identity, CSRF token, ARIA-enabled flag, env label) is set in render() → internal/console/render.go:38-43
- Static CSS and logo are embedded and served without auth, cache max-age 3600 → internal/console/static.go:13-14, internal/console/static.go:61-76
- CSS URL carries a content-hash ?v= suffix to bust caches → internal/console/static.go:19-29
- Narrative (LLM briefing) cache is in memory, keyed by tenant+window → internal/console/handlers.go:778-812
## Gotchas
- Console startup fails if template parse or CSRF key fails; it does not start a broken console → internal/console/server.go:77-85
- Mutating routes all call checkCSRF before touching the store (7 sites) → internal/console/handlers.go:851-863
