---
id: gotcha-refused-mutation-audit-best-effort
kind: gotcha
title: Refusal audit entries are best-effort, success entries are not
summary: A denied mutation is audited outside the mutation's transaction and a failed insert is only logged, so a refusal can be missing from the trail while a success never is; a PATCH refusal is written while its row lock is still held
tags: [gotcha, audit, refusal, denied, best effort, logging, transaction, duplicate, 409, row lock, patch, connection pool]
links: [related:data-model-audit-log, related:decision-audit-in-transaction, related:module-store, related:api-scim-patch]
sources: [internal/store/audit.go:105-113, internal/store/user.go:168, internal/store/user.go:174-176, internal/store/user.go:265, internal/store/user.go:355-358, internal/store/user.go:391-394, internal/store/group.go:225, internal/store/group.go:233, internal/store/group.go:405, internal/store/group.go:413]
updated: 2026-11-26
verified_commit: 4185d2b
---
## Facts
- `auditRefusal` writes with the pool, not a transaction, because no row changed → internal/store/audit.go:105-107
- If that insert fails it logs "could not record a refused mutation" and returns; the refusal is not retried → internal/store/audit.go:108-113
- Refusals are written on duplicate userName (create, and replace used by PUT and PATCH), missing user (PUT, DELETE), duplicate displayName and invalid member paths → internal/store/user.go:168, internal/store/user.go:265, internal/store/user.go:355-358, internal/store/user.go:391-394, internal/store/group.go:225, internal/store/group.go:233, internal/store/group.go:405, internal/store/group.go:413
- Successful mutations have no such escape: an audit insert error rolls the change back → internal/store/user.go:174-176
- A PATCH refusal is written from the pool while its transaction still holds one connection and the row lock; the refusal needs a second connection, so it can wait on a saturated pool → internal/store/user.go:355-358, internal/store/audit.go:109
- A PATCH on a missing id writes no refusal row at all; the lock step returns ErrNotFound first → internal/store/user.go:297-303
- Consequence for review: treat the denied rows as best-effort evidence; the success trail is complete
