---
id: api-console-routes
kind: api
title: Console HTTP routes (/console)
summary: Read pages under GET /console/*, mutations under POST /console/* with CSRF; all but static need a console token and an allowlisted Host
tags: [routes, endpoints, api, console routes, pages, replay, narrate, aria, webhooks, tenants, tokens, attributes, audit, ARIA window, since]
links: [implements:module-console, depends_on:module-console-auth, related:decision-console-host-allowlist]
sources: [internal/console/server.go:125-153, internal/console/handlers.go:47-70, internal/console/handlers.go:201-228, internal/console/handlers.go:429-433, internal/console/handlers.go:524-532, internal/console/handlers.go:595-622, internal/console/handlers.go:727-770, internal/console/handlers.go:900-920]
updated: 2026-11-27
verified_commit: 7f7fe4d
---
## Facts
- Read pages (GET): /console, /console/tenants, /console/tokens, /console/attributes → internal/console/server.go:129-133
- Read pages (GET): /console/webhooks, /console/webhooks/delivery-status, /console/audit → internal/console/server.go:133-135
- Read pages (GET): /console/admin-audit, /console/aria → internal/console/server.go:136-137
- Mutations (POST): /console/tenants (create tenant) → internal/console/server.go:140
- Mutations (POST): /console/tokens/issue and /console/tokens/revoke → internal/console/server.go:141-142
- Mutations (POST): /console/attributes/register and /console/attributes/unregister → internal/console/server.go:143-144
- Mutations (POST): /console/webhooks/replay (dead-letter replay) → internal/console/server.go:145
- Mutations (POST): /console/aria/narrate (LLM briefing; fragment when X-Requested-With is set) → internal/console/server.go:146, internal/console/handlers.go:727-746
- Token issue accepts an expiry like the CLI ("90d" or a Go duration; empty means none) → internal/console/handlers.go:201-228, internal/console/handlers.go:900-920
- Replay returns JSON when the client asks for it, not a page → internal/console/handlers.go:429-433
- ARIA page reviews only the windows its selector offers (1h, 24h, 7d, 30d); any other since falls back to 24h → internal/console/handlers.go:524-532, internal/console/handlers.go:601-607
- Windows are counted uncapped only over those fixed sizes, so a page load stays bounded → internal/console/handlers.go:524-526
- ARIA page shows "Partial review" when the newest 200-row batch may not be the whole window → internal/console/handlers.go:618-620, internal/console/templates/aria.html:30-33
- ARIA narrate skips the LLM call on a quiet window and on a cache hit unless force=1 → internal/console/handlers.go:742-752
- LLM failures show a generic message; upstream error text is logged, not shown → internal/console/handlers.go:756-761
- Static assets: GET /console/static/ is served before auth → internal/console/server.go:151
- Home page shows SCIM base URL, tenant count, and webhook status → internal/console/handlers.go:47-70
- ARIA narrative is advisory only and never re-enters the store or auth path → internal/console/handlers.go:727-770
