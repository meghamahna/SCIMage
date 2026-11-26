---
id: api-console-routes
kind: api
title: Console HTTP routes (/console)
summary: Read pages under GET /console/*, mutations under POST /console/* with CSRF; all but static need a console token
tags: [routes, endpoints, api, console routes, pages, replay, narrate, aria, webhooks, tenants, tokens, attributes, audit]
links: [implements:module-console, depends_on:module-console-auth]
sources: [internal/console/server.go:114-142, internal/console/handlers.go:31-41, internal/console/handlers.go:424-426]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Read pages (GET): /console, /console/tenants, /console/tokens, /console/attributes → internal/console/server.go:117-122
- Read pages (GET): /console/webhooks, /console/webhooks/delivery-status, /console/audit → internal/console/server.go:122-124
- Read pages (GET): /console/admin-audit, /console/aria → internal/console/server.go:125-126
- Mutations (POST): /console/tenants (create tenant) → internal/console/server.go:129
- Mutations (POST): /console/tokens/issue and /console/tokens/revoke → internal/console/server.go:130-131
- Mutations (POST): /console/attributes/register and /console/attributes/unregister → internal/console/server.go:132-133
- Mutations (POST): /console/webhooks/replay (dead-letter replay) → internal/console/server.go:134
- Mutations (POST): /console/aria/narrate (LLM briefing; fragment when X-Requested-With is set) → internal/console/server.go:135, internal/console/handlers.go:710-722
- Token issue accepts an expiry like the CLI ("90d" or a Go duration; empty means none) → internal/console/handlers.go:863-886
- Replay and delivery-status return JSON, not a page → internal/console/render.go:78-87, internal/console/handlers.go:424-426
- ARIA narrate skips the LLM call on a quiet window and on a cache hit unless force=1 → internal/console/handlers.go:720-738
- LLM failures show a generic message; upstream error text is logged, not shown → internal/console/handlers.go:734-744
- Static assets: GET /console/static/ is served before auth → internal/console/server.go:140
- Home page shows SCIM base URL, tenant count, and webhook status → internal/console/handlers.go:31-41
## Gotchas
- ARIA narrative is advisory only and never re-enters the store or auth path → internal/console/handlers.go:705-709
- Unauthenticated requests get 401 with a Basic challenge, not a login redirect → internal/console/auth.go:44-52
