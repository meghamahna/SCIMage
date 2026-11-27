---
id: module-store
kind: module
title: Store (Postgres persistence layer)
summary: internal/store owns every SQL query, pgx pool setup, tenant scoping, row locks for user and group writes, and the same-transaction audit and outbox writes
tags: [store, postgres, pgx, sql, repository, persistence, database, tenant, audit, outbox, row lock, FOR UPDATE, FOR NO KEY UPDATE, concurrency, patch, externalId, duplicate, refusal]
links: [depends_on:data-model-core-users-groups, depends_on:data-model-tenants-tokens, depends_on:data-model-audit-log, depends_on:data-model-webhook-outbox, decided_by:decision-audit-in-transaction, decided_by:decision-tenant-scoping, decided_by:decision-token-hashing, related:module-scim, related:api-scim-patch, related:api-scim-errors, related:workflow-migrations]
sources: [internal/store/store.go:1-152, internal/store/user.go:1-99, internal/store/group.go:1-36, internal/store/audit.go:1-60, internal/store/outbox.go:1-70, internal/store/patch_test.go:1-50]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Package `store` is the Postgres-backed persistence layer; raw SQL via pgx v5 pool, no ORM → internal/store/store.go:1-2
- `New` builds a pgxpool from the DSN and pings it so a bad DSN fails at startup → internal/store/store.go:63-90
- `WithChangeEvents()` turns on webhook outbox writes; off by default → internal/store/store.go:51-55
- `DSNFromEnv` reads `DATABASE_URL`, else `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_PORT` → internal/store/store.go:106-134
- Pool ceilings come from `DATABASE_MAX_CONNS` and `DATABASE_MAX_CONN_LIFETIME`, else pgx defaults → internal/store/store.go:136-150
- Sentinel errors (ErrNotFound, ErrGroupNotFound, ErrDuplicateUserName, ErrDuplicateGroupName, ErrDuplicateExternalID, ErrInvalidMember and others) are mapped to 404/409/400 by the HTTP layer → internal/store/store.go:18-41
- Users API: CreateUser, GetUser, ListUsers, UpdateUser, DeactivateUser → internal/store/user.go:155, 197, 220, 262, 388
- User PATCH: PatchUser (lock, apply callback, write, one transaction) → internal/store/user.go:297
- Groups API: CreateGroup, GetGroup, ListGroups, UpdateGroup, DeleteGroup → internal/store/group.go:214, 267, 288, 334, 457
- Group PATCH: PatchGroup (same contract as PatchUser) → internal/store/group.go:366
- Tokens API: IssueToken, GetTokenByKeyID, ListTokens, TouchToken, RevokeToken → internal/store/token.go:67, 103, 119, 146, 159
- Console tokens API: IssueConsoleToken, GetConsoleTokenByKeyID, ListConsoleTokens, TouchConsoleToken, RevokeConsoleToken → internal/store/consoletoken.go:54, 90, 106, 133, 145
- Tenants API: CreateTenant, GetTenant, ListTenants → internal/store/tenant.go:57, 90, 106
- Extensible attributes API: RegisterAttribute, UnregisterAttribute, ListAttributes → internal/store/attribute.go:39, 81, 111
- Audit reads: ListAuditEntries, ListAuditEntriesSince, AuditWindowStats → internal/store/audit.go:163, 198, 233; admin audit reads: ListAdminAuditEntries → internal/store/adminaudit.go:57
- Outbox API: ClaimDueDeliveries, MarkDelivered, RescheduleDelivery, DeadLetterDelivery, PurgeDeliveredBefore, ReplayDeadLetter → internal/store/outbox.go:192, 227, 238, 250, 280, 346
- Every mutation is one pgx transaction: write row, insert audit, enqueue event, commit → internal/store/user.go:155-195
- User writers (UpdateUser, PatchUser, DeactivateUser) lock the row first with lockUserTx → internal/store/user.go:269, 304, 404
- lockUserTx uses FOR NO KEY UPDATE, so group membership FK locks on users do not block it → internal/store/user.go:330-336
- getGroupTx locks the group row with FOR UPDATE, then reads members in the same transaction → internal/store/group.go:189-193
- PatchUser and PatchGroup pass the locked current state to a callback and write its result in the same transaction → internal/store/user.go:297-327, internal/store/group.go:366-401
- Every query takes tenantID and filters on it (`tenant_id = $1`) → internal/store/user.go:336
- Reads are not audited; only mutations and refusals → internal/store/audit.go:14-15
- ListUsers is capped at MaxPageSize (200) before any row is read → internal/store/user.go:99, 224
- Tests give each test its own tenant and delete its rows on cleanup, so packages can share one Postgres → internal/store/store_test.go:34-62
- Test helper waitForLockWaiters polls pg_locks until N writers queue behind a row lock → internal/store/patch_test.go:16
## Gotchas
- Refused mutations are audited after rollback, outside the transaction, and best-effort → gotcha-refused-mutation-audit-best-effort
- Token struct exposes SecretHash so the auth middleware can compare; the store never decides auth → internal/store/token.go:28-35
