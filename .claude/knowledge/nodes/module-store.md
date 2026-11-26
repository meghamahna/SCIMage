---
id: module-store
kind: module
title: Store (Postgres persistence layer)
summary: internal/store owns every SQL query, pgx pool setup, tenant scoping and the same-transaction audit and outbox writes
tags: [store, postgres, pgx, sql, repository, persistence, database, tenant, audit, outbox]
links: [depends_on:data-model-core-users-groups, depends_on:data-model-tenants-tokens, depends_on:data-model-audit-log, depends_on:data-model-webhook-outbox, decided_by:decision-audit-in-transaction, decided_by:decision-tenant-scoping, decided_by:decision-token-hashing, related:module-scim, related:workflow-migrations]
sources: [internal/store/store.go:1-149, internal/store/user.go:1-99, internal/store/group.go:1-30, internal/store/audit.go:1-60, internal/store/outbox.go:1-70]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Package `store` is the Postgres-backed persistence layer; raw SQL via pgx v5 pool, no ORM → internal/store/store.go:1-2
- `New` parses the DSN, builds a pgxpool and pings it so a bad DSN fails at startup → internal/store/store.go:60-80
- `WithChangeEvents()` turns on webhook outbox writes; off by default → internal/store/store.go:51-53
- `DSNFromEnv` reads `DATABASE_URL`, else `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_PORT` → internal/store/store.go:103-131
- Pool ceilings come from `DATABASE_MAX_CONNS` and `DATABASE_MAX_CONN_LIFETIME`, else pgx defaults → internal/store/store.go:133-149
- Sentinel errors (ErrNotFound, ErrDuplicateUserName, ErrDuplicateGroupName, ErrDuplicateTenantName, ErrInvalidMember, ErrDuplicateAttribute, ErrReservedAttribute) are mapped to 404/409 by the HTTP layer → internal/store/store.go:18-40
- Public API, users: CreateUser, GetUser, ListUsers, UpdateUser, DeactivateUser → internal/store/user.go:153,191,214,256,312
- Public API, groups: CreateGroup, GetGroup, ListGroups, UpdateGroup, DeleteGroup → internal/store/group.go:204,253,274,321,383
- Public API, tokens: IssueToken, GetTokenByKeyID, ListTokens, TouchToken, RevokeToken → internal/store/token.go:64,100,116,143,156
- Public API, console tokens: IssueConsoleToken, GetConsoleTokenByKeyID, ListConsoleTokens, TouchConsoleToken, RevokeConsoleToken → internal/store/consoletoken.go:54,90,106,133,145
- Public API, tenants: CreateTenant, GetTenant, ListTenants → internal/store/tenant.go:43,76,92
- Public API, extensible attributes: RegisterAttribute, UnregisterAttribute, ListAttributes → internal/store/attribute.go:39,81,111
- Public API, audit reads: ListAuditEntries, ListAuditEntriesSince, AuditWindowStats, ListAdminAuditEntries → internal/store/audit.go:116,151,186 and internal/store/adminaudit.go:57
- Public API, outbox: ClaimDueDeliveries, MarkDelivered, RescheduleDelivery, DeadLetterDelivery, ReplayDeadLetter, PurgeDeliveredBefore → internal/store/outbox.go:192,227,238,250,346,280
- Every mutation is one pgx transaction: write row, insert audit, enqueue event, commit → internal/store/user.go:153-189
- Every query takes tenantID and filters on it (`tenant_id = $1`) → internal/store/user.go:192, internal/store/token.go:118
- Reads are not audited; only mutations and refusals → internal/store/audit.go:15
- ListUsers is capped at MaxPageSize (200) before any row is read → internal/store/user.go:96-99
- Tests give each test its own tenant and delete its rows on cleanup, so packages can share one Postgres → internal/store/store_test.go:34-70
## Gotchas
- Refused mutations are audited outside the transaction and best-effort → gotcha-refused-mutation-audit-best-effort
- Token struct exposes SecretHash so the auth middleware can compare; the store never decides auth → internal/store/token.go:28-31
