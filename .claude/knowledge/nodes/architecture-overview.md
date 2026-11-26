---
id: architecture-overview
kind: architecture
title: Architecture overview
summary: Three binaries over one Postgres database; SCIM listener with auth, rate limit, handlers and transactional store; outbox to webhook dispatcher; optional admin console and ARIA
tags: [architecture, overview, go, binaries, request path, multi-tenancy, outbox, console, aria, layout, design, structure]
links: [depends_on:module-scim, depends_on:module-store, depends_on:module-webhook, depends_on:module-console, depends_on:module-console-auth, depends_on:module-apidocs, depends_on:module-logging, depends_on:module-aria, related:entrypoint-cmd-server-main, related:entrypoint-cmd-scimage-admin-main, related:entrypoint-cmd-aria-main, related:workflow-migrations, related:workflow-docker, related:workflow-ci, related:workflow-precommit-hook, decided_by:decision-audit-in-transaction, decided_by:decision-tenant-scoping, decided_by:decision-request-timeout-before-auth, decided_by:decision-credentials-issued-by-cli, decided_by:decision-console-separate-listener, decided_by:decision-aria-advisory-only, decided_by:decision-stdlib-and-raw-sql, related:glossary-core-terms]
sources: [cmd/server/main.go:1-200, cmd/scimage-admin/main.go:1-100, cmd/aria/main.go:1-100, docs/ARCHITECTURE.md:1-433, internal/scim/handler.go:1-100, internal/store/store.go:1-80, internal/webhook/dispatcher.go:1-100, internal/console/server.go:1-100]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Binaries
- cmd/server: SCIM listener, optional admin console, webhook dispatcher → cmd/server/main.go:58-63, cmd/server/main.go:65-113
- cmd/scimage-admin: tenant, token, attribute, audit and webhook-replay CLI; writes Postgres directly → cmd/scimage-admin/main.go:21-26, cmd/scimage-admin/main.go:33-39
- cmd/aria: audit reviewer CLI; prints a report to stdout and writes nothing back → cmd/aria/main.go:1-7, cmd/aria/main.go:24-29
- Only cmd/server opens a network listener; the other two are operator-run processes → cmd/server/main.go:115-118, cmd/aria/main.go:49-71
- Startup order: logging, DSN, webhook config, pool, dispatcher goroutine, listeners → cmd/server/main.go:65-113
## Request path (SCIM listener)
- Order: request timeout → auth → rate limit → router → handler → store transaction → docs/ARCHITECTURE.md:24-42
- Routes() wraps the mux as logRequests → withTimeout → requireToken → limiter.throttle → mux → internal/scim/handler.go:63-98
- Auth: key id lookup, revoked/expired check, constant-time secret compare, tenant match → docs/ARCHITECTURE.md:100-107
- Rate limiter is a token bucket keyed per issued token, after auth → docs/ARCHITECTURE.md:35
- Handlers validate payloads and map them to rows, always scoped to the URL tenant → docs/ARCHITECTURE.md:39
- Store: one pgx transaction per mutation writes row, audit_log row and outbox row, then commits → internal/store/user.go:153-189
- Health probes (/healthz, /readyz) and /docs sit on the root mux outside SCIM auth → cmd/server/main.go:120-134
## Change delivery
- Outbox: each change queues a webhook_deliveries row inside the same transaction → docs/ARCHITECTURE.md:212-216
- Dispatcher runs only when SCIM_WEBHOOK_URL is set; it claims due rows with FOR UPDATE SKIP LOCKED → internal/webhook/dispatcher.go:74-77, docs/ARCHITECTURE.md:224-238
- Lease is next_attempt_at pushed forward, so no row lock is held across the HTTP send → docs/ARCHITECTURE.md:240-243
- Dispatcher context is cancelled only after the HTTP listeners drain → cmd/server/main.go:100-104
- Failure handling: retry with backoff, then dead-letter; replay requeues → docs/ARCHITECTURE.md:258-272
## Admin console (optional)
- Started only when CONSOLE_ADDR is set, as a second http.Server beside the SCIM listener → cmd/server/main.go:36-44, cmd/server/main.go:150-167
- Own listener so it never shares a socket with tenant SCIM traffic → docs/ARCHITECTURE.md:63-70
- Authentication is a system-wide console token with no sessions or cookies; mutating forms carry a CSRF token → internal/console/auth.go:38-42, internal/console/csrf.go:16-53
- Mutations call store functions, so audit-in-transaction is inherited → internal/console/server.go:31-59
- Admin surface only; tenant self-service is not offered → internal/console/server.go:1-16
## Multi-tenancy
- One process and one Postgres database serve every tenant; a tenant is a row plus tenant_id on each table → docs/ARCHITECTURE.md:89-94
- Addressing is /scim/v2/{tenantID}; a token is valid only for its own tenant → docs/ARCHITECTURE.md:96-107
- Every failure answers the same generic 401 → docs/ARCHITECTURE.md:105-107
- Lookups by id filter on tenant_id and id in one WHERE clause → docs/ARCHITECTURE.md:109-113
- Storage: ten tables, including audit_log, admin_audit_log and webhook_deliveries → docs/ARCHITECTURE.md:74-87
## ARIA
- Go computes all signals; the LLM only narrates the computed facts, and the output is advisory → docs/ARCHITECTURE.md:392-412
- Never wired into provisioning or auth → docs/ARCHITECTURE.md:408-412
## Cross-cutting
- Logging is set up first; JSON to stdout, optional daily file → internal/logging/logging.go:26-28
- Swagger UI and OpenAPI spec are embedded and served on the SCIM listener → cmd/server/main.go:127-132
- Shutdown: 15s grace per server, dispatcher stopped last → cmd/server/main.go:169-197
