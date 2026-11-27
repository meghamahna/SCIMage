---
id: api-console-routes
kind: api
title: Console HTTP routes (/console)
summary: Read pages under GET /console/*, mutations under POST /console/* with CSRF; all but static need a console token
tags: [routes, endpoints, api, console routes, pages, replay, narrate, aria, webhooks, tenants, tokens, attributes, audit]
links: [implements:module-console, depends_on:module-console-auth]
sources: [internal/console/server.go:114-143, internal/console/handlers.go:196-213, internal/console/handlers.go:424-426, internal/console/handlers.go:493-495, internal/console/handlers.go:706-744, internal/console/handlers.go:876-878]
updated: 2026-11-26
verified_commit: 794ebee
---
## Facts
- Read pages (GET): /console, /console/tenants, /console/tokens, /console/attributes → internal/console/server.go:118-121
- Read pages (GET): /console/webhooks, /console/webhooks/delivery-status, /console/audit → internal/console/server.go:122-124
- Read pages (GET): /console/admin-audit, /console/aria → internal/console/server.go:125-126
- Mutations (POST): /console/tenants (create tenant) → internal/console/server.go:129
- Mutations (POST): /console/tokens/issue and /console/tokens/revoke → internal/console/server.go:130-131
- Mutations (POST): /console/attributes/register and /console/attributes/unregister → internal/console/server.go:132-133
- Mutations (POST): /console/webhooks/replay (dead-letter replay) → internal/console/server.go:134
- Mutations (POST): /console/aria/narrate (LLM briefing; fragment when X-Requested-With is set) → internal/console/server.go:135, internal/console/handlers.go:710-722
- Token issue accepts an expiry like the CLI ("90d" or a Go duration; empty means none) → internal/console/handlers.go:207, internal/console/handlers.go:876-878
- Replay and delivery-status return JSON, not a page → internal/console/render.go:81-89, internal/console/handlers.go:424-426
- ARIA narrate skips the LLM call on a quiet window and on a cache hit unless force=1 → internal/console/handlers.go:724-738
- LLM failures show a generic message; upstream error text is logged, not shown → internal/console/handlers.go:738-744
- Static assets: GET /console/static/ is served before auth → internal/console/server.go:140
- Home page shows SCIM base URL, tenant count, and webhook status → internal/console/handlers.go:42-58
## Gotchas
- ARIA narrative is advisory only and never re-enters the store or auth path → internal/console/handlers.go:706-709
- Unauthenticated requests get 401 with a Basic challenge, not a login redirect → internal/console/auth.go:44-52
