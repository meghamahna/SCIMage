---
id: architecture-overview
kind: architecture
title: Architecture overview
summary: Three binaries over one Postgres database; SCIM listener with auth, rate limit, handlers and transactional store; outbox to webhook dispatcher; optional admin console and ARIA
tags: [architecture, overview, go, binaries, request path, middleware, multi-tenancy, outbox, console, aria, layout, design, structure, docs]
links: [depends_on:module-scim, depends_on:module-store, depends_on:module-webhook, depends_on:module-console, depends_on:module-console-auth, depends_on:module-apidocs, depends_on:module-logging, depends_on:module-aria, related:entrypoint-cmd-server-main, related:entrypoint-cmd-scimage-admin-main, related:entrypoint-cmd-aria-main, related:workflow-migrations, related:workflow-docker, related:workflow-ci, related:workflow-precommit-hook, decided_by:decision-audit-in-transaction, decided_by:decision-tenant-scoping, decided_by:decision-request-timeout-before-auth, decided_by:decision-credentials-issued-by-cli, decided_by:decision-console-separate-listener, decided_by:decision-aria-advisory-only, decided_by:decision-stdlib-and-raw-sql, related:glossary-core-terms, documented_in:doc-architecture]
sources: [cmd/server/main.go:36-44, cmd/server/main.go:58-113, cmd/server/main.go:122-134, cmd/server/main.go:147-197, cmd/scimage-admin/main.go:21-39, cmd/aria/main.go:24-29, docs/ARCHITECTURE.md:28-52, docs/ARCHITECTURE.md:77-82, docs/ARCHITECTURE.md:89-134, docs/ARCHITECTURE.md:259-349, docs/ARCHITECTURE.md:440-548, internal/scim/handler.go:93-98, internal/store/user.go:155-189, internal/webhook/dispatcher.go:82-106, internal/console/auth.go:38-42, internal/logging/logging.go:20-34]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Binaries
- cmd/server: SCIM listener, optional admin console, webhook dispatcher → cmd/server/main.go:58-113
- cmd/scimage-admin: tenant, token, attribute, audit and webhook-replay CLI; writes Postgres directly → cmd/scimage-admin/main.go:21-39
- cmd/aria: audit reviewer CLI; reads the audit log and prints a report → cmd/aria/main.go:1-7, cmd/aria/main.go:24-29
- Startup order: logging, DSN, webhook config, pool, dispatcher goroutine, listeners → cmd/server/main.go:65-113
## Request path (SCIM listener)
- Order: request logging → request timeout → auth → body capture → rate limit → router → handler → store transaction → docs/ARCHITECTURE.md:33-52
- Routes() chain is logRequests(withTimeout(requireToken(captureBody(throttle(mux))))) → internal/scim/handler.go:93-98
- Auth: key id lookup, revoked/expired check, constant-time secret compare, tenant match → internal/scim/auth.go:45-63, internal/scim/auth.go:98-136
- Rate limiter is a token bucket keyed per issued token, after auth → docs/ARCHITECTURE.md:46
- Handlers validate payloads and map them to rows, always scoped to the URL tenant → docs/ARCHITECTURE.md:50
- Store: one pgx transaction per mutation writes the row, audit_log row and outbox row, then commits → internal/store/user.go:155-189
- /healthz, /readyz and /docs are mounted outside SCIM auth, since none carries tenant data → docs/ARCHITECTURE.md:77-82, cmd/server/main.go:122-132
## Change delivery
- Outbox: each change queues a webhook_deliveries row inside the same transaction → docs/ARCHITECTURE.md:259-262
- Dispatcher runs only when SCIM_WEBHOOK_URL is set; it claims due rows with FOR UPDATE SKIP LOCKED → internal/webhook/dispatcher.go:82-106, cmd/server/main.go:111
- Lease is next_attempt_at pushed forward, so no row lock is held across the HTTP send → docs/ARCHITECTURE.md:287-289
- Dispatcher context is cancelled only after the HTTP listeners drain → cmd/server/main.go:100-104
- Failure handling: retry with backoff, then dead-letter; replay requeues → docs/ARCHITECTURE.md:305-318
## Admin console (optional)
- Started only when CONSOLE_ADDR is set, as a second http.Server beside the SCIM listener → cmd/server/main.go:36-44, cmd/server/main.go:147-167
- Own listener so it never shares a socket with tenant SCIM traffic → docs/ARCHITECTURE.md:443-446
- Authentication is a system-wide console token with no sessions or cookies; mutating forms carry a CSRF token → internal/console/auth.go:38-42, docs/ARCHITECTURE.md:475-496
- Admin surface only; tenant self-service is not offered → internal/console/server.go:1-16
## Multi-tenancy
- One process and one Postgres database serve every tenant; a tenant is a row plus tenant_id on each table → docs/ARCHITECTURE.md:110-115
- Addressing is /scim/v2/{tenantID}; a token is valid only for its own tenant → docs/ARCHITECTURE.md:117-119, docs/ARCHITECTURE.md:121-129
- Every failure answers the same generic 401 → docs/ARCHITECTURE.md:127-129
- Lookups by id filter on tenant_id and id in one WHERE clause → docs/ARCHITECTURE.md:130-134
- Storage: audit_log, admin_audit_log and webhook_deliveries are among the tables → docs/ARCHITECTURE.md:89-102
## ARIA
- Go computes all signals; the LLM only narrates the computed facts, and the output is advisory → docs/ARCHITECTURE.md:511-521
- Never wired into provisioning or authorization decisions → docs/ARCHITECTURE.md:519-523
## Cross-cutting
- Logging is set up first; JSON to stdout, optional daily file under LOG_DIR → internal/logging/logging.go:20-34, docs/ARCHITECTURE.md:527-530
- Swagger UI and OpenAPI spec are embedded and served on the SCIM listener → cmd/server/main.go:131-132
- Shutdown: 15s grace per server, dispatcher stopped last → cmd/server/main.go:169-197
