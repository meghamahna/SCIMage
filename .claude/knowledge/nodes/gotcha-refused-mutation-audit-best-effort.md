---
id: gotcha-refused-mutation-audit-best-effort
kind: gotcha
title: Refusal audit entries are best-effort, success entries are not
summary: A denied mutation is audited outside any transaction and a failed insert is only logged, so a refusal can be missing from the trail while a success never is
tags: [gotcha, audit, refusal, denied, best effort, logging, transaction, duplicate, 409]
links: [related:data-model-audit-log, related:decision-audit-in-transaction, related:module-store]
sources: [internal/store/audit.go:105-113, internal/store/user.go:164-170, internal/store/user.go:281-290, internal/store/group.go:214-228]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- `auditRefusal` writes with the pool, not a transaction, because no row changed → internal/store/audit.go:105-107
- If that insert fails it logs "could not record a refused mutation" and returns; the refusal is not retried → internal/store/audit.go:108-113
- Refusals are written on duplicate userName, missing user, duplicate displayName and invalid member paths → internal/store/user.go:168, internal/store/user.go:285-288, internal/store/group.go:218-226
- Successful mutations have no such escape: an audit insert error rolls the change back → internal/store/user.go:174-176
- Consequence for review: treat the denied rows as best-effort evidence; the success trail is complete
