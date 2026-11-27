---
id: gotcha-refused-mutation-audit-best-effort
kind: gotcha
title: Refusal audit entries are best-effort, success entries are not
summary: A denied mutation is audited after its transaction is rolled back, outside any transaction, and a failed insert is only logged; success entries commit with the change
tags: [gotcha, audit, refusal, denied, best effort, logging, transaction, duplicate, externalId, row lock, patch, connection pool, 409, 413]
links: [related:data-model-audit-log, related:decision-audit-in-transaction, related:module-store, related:api-scim-patch, related:api-scim-errors, related:module-scim]
sources: [internal/store/audit.go:110-139, internal/store/user.go:170-175, internal/store/user.go:180-181, internal/store/user.go:271, internal/store/user.go:307, internal/store/user.go:315, internal/store/user.go:365-370, internal/store/user.go:406, internal/store/group.go:228-240, internal/store/group.go:344, internal/store/group.go:376, internal/store/group.go:389, internal/store/group.go:417-429, internal/store/group.go:467, internal/scim/handler.go:546-558, internal/scim/userstore.go:34-37, internal/scim/groupstore.go:23]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- `RecordRefusal` inserts the denied row from the pool, after `refuseTx` has rolled back the mutation's transaction, so it never holds a row lock or waits for a second connection → internal/store/audit.go:131-139
- The insert runs on `context.WithoutCancel` with a 5s timeout, so a client hanging up cannot drop the row → internal/store/audit.go:115-117
- A failed insert logs "could not record a refused mutation" and is not retried; the refusal still stands → internal/store/audit.go:125-128
- A non-UUID target id is stored as null, not rejected → internal/store/audit.go:119-123
- Refusal `detail` is a fixed string. A PATCH callback error is recorded as "invalid patch", never its text → internal/store/user.go:315, internal/store/group.go:389
- Missing ids are refused on PUT, PATCH and DELETE ("no such user" / "no such group") → internal/store/user.go:271, internal/store/user.go:307, internal/store/user.go:406, internal/store/group.go:344, internal/store/group.go:376, internal/store/group.go:467
- Duplicate and invalid-member refusals on create, replace and PATCH → internal/store/user.go:170-174, internal/store/user.go:365-369, internal/store/group.go:228-240, internal/store/group.go:417-429
- Handler-side refusals (413 body too large, invalid body, schema failure, id mismatch) use fixed texts → internal/scim/handler.go:546-549, internal/scim/handler.go:552-558
- Refused requests that fail before a store call (401, 429) write no audit row; they are logged only → internal/scim/auth.go:59-60, internal/scim/ratelimit.go:101-102
- Successful mutations have no such escape: an audit insert error rolls the change back → internal/store/user.go:180-181
- Consequence for review: denied rows are best-effort evidence and may be missing; the success trail is complete
