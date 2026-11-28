---
id: doc-architecture
kind: doc
title: docs/ARCHITECTURE.md section map
summary: Section map of the architecture document: request path, storage, tenancy, stores, change delivery, admin console, ARIA, logging and shutdown, with the line where each starts
tags: [docs, architecture doc, ARCHITECTURE.md, sections, request path, admin console, middleware, outbox, logging, aria, design doc]
links: [related:architecture-overview, related:module-console, related:module-console-auth, related:component-scim-request-middleware, related:decision-stdlib-and-raw-sql, related:module-webhook, related:module-aria]
sources: [docs/ARCHITECTURE.md:1-548]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Facts
- Request path block: request logging, timeout, auth, body capture, rate limiter, router, handler, store → docs/ARCHITECTURE.md:28-52
- Storage model table lists the ten tables, with migration 000009 trigger note → docs/ARCHITECTURE.md:87-107
- Multi-tenancy: shared pool model, token format and generic 401, id lookups → docs/ARCHITECTURE.md:110-134
- Store interfaces obligations (audit in transaction, non-nil result) → docs/ARCHITECTURE.md:203-215
- Extensible attributes registry (tenant_attributes, extended_attributes) → docs/ARCHITECTURE.md:225-232
- Change delivery: outbox, claiming and leases, retry and dead-letter, retention, signing, event semantics → docs/ARCHITECTURE.md:255-438
- Admin console: middleware order, routes, credential, CSRF, listener limits → docs/ARCHITECTURE.md:440-499
- Audit review (ARIA) advisory design → docs/ARCHITECTURE.md:501-523
- Logging and SCIM_LOG_REQUESTS redaction summary → docs/ARCHITECTURE.md:525-541
- Shutdown order: HTTP listener drains before the dispatcher stops → docs/ARCHITECTURE.md:543-548
- Contents list at the top links each section by anchor (emoji-prefixed, e.g. #-retry-and-dead-letter) → docs/ARCHITECTURE.md:7-25
