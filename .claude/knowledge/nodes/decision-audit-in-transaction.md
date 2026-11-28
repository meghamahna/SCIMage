---
id: decision-audit-in-transaction
kind: decision
title: Audit and outbox rows commit with the change they describe
summary: Successful user and group mutations write their audit_log row and any webhook outbox row inside the change's transaction, so a committed change always has a record; refusals are recorded best-effort after rollback (see the gotcha)
tags: [decision, audit, audit_log, transaction, atomic, outbox, governance, why, rollback, store, compliance, actor, admin_audit_log, patch, row lock]
links: [related:data-model-audit-log, related:data-model-webhook-outbox, related:module-store, related:module-scim, related:api-scim-patch, related:gotcha-refused-mutation-audit-best-effort, documented_in:doc-architecture]
sources: [migrations/000002_create_audit_log.up.sql:1-4, migrations/000004_create_webhook_deliveries.up.sql:1-7, internal/store/user.go:155-195, internal/store/user.go:297-327, internal/store/user.go:335-386, internal/store/group.go:214-256, internal/store/group.go:366-401, internal/store/audit.go:47-57, internal/store/audit.go:81-99, internal/store/audit.go:110-139, internal/store/adminaudit.go:44-51, internal/store/outbox.go:192-280, internal/scim/userstore.go:24-37, internal/scim/groupstore.go:13-25, internal/scim/handler.go:537-541, internal/scim/handler.go:560-566, docs/ARCHITECTURE.md:259-266]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Decision
- The store, not the handler, writes the audit row inside the mutation's transaction, so a handler cannot skip it → migrations/000002_create_audit_log.up.sql:1-4
- Every mutating UserStore/GroupStore method takes an AuditRecord; handlers never write audit rows themselves → internal/scim/userstore.go:24-37, internal/scim/groupstore.go:13-25
- Admin actions (IssueToken, RevokeToken, CreateTenant, RegisterAttribute/UnregisterAttribute, RevokeConsoleToken, ReplayDeadLetter) take an actor string and write admin_audit_log via insertAdminAudit → internal/store/adminaudit.go:44-51
- Outbox state changes (MarkDelivered, RescheduleDelivery, DeadLetterDelivery, PurgeDeliveredBefore, ClaimDueDeliveries) write no audit row → internal/store/outbox.go:192-280
- AuditRecord carries actor (token key id), client IP and tenant, all taken from the authenticated request → internal/scim/handler.go:537-541
- Client IP comes from RemoteAddr only; X-Forwarded-For is ignored because a caller could forge it → internal/scim/handler.go:560-566
- If a successful mutation's audit insert fails, the mutation rolls back with it; a file log cannot give that guarantee because Postgres and a filesystem share no commit → migrations/000002_create_audit_log.up.sql:1-4, internal/store/user.go:180-181
- Outbound webhook events are queued in the same transaction, not sent after COMMIT, so a crash cannot lose or invent an event → migrations/000004_create_webhook_deliveries.up.sql:1-7, docs/ARCHITECTURE.md:259-262
- Pattern in code: insert row, insertAudit, enqueueChange, Commit → internal/store/user.go:155-195
- Update and PATCH paths lock the row first (lockUserTx), then read the before-image and write the audit and change rows in the same transaction → internal/store/user.go:335-386
- PATCH runs its read, apply and write under one row lock, so the audit before-image is the state the patch was computed from → internal/store/user.go:297-327, internal/store/group.go:366-401
- Group create follows the same pattern → internal/store/group.go:214-256
- Refusals are not in the transaction: RecordRefusal writes via the pool after refuseTx rolls back; a failed insert is logged and swallowed → internal/store/audit.go:110-139
- Actor identity comes from the auth middleware through AuditRecord, not from mutation arguments → internal/store/audit.go:47-57
- Empty audit strings are stored as NULL so absent reads as absent → internal/store/audit.go:81-99
## Why
- Enterprise provisioning needs proof of who changed what, and a committed change with no record is a compliance gap.
- Refusals are recorded too, because a burst of denials is itself a signal; they are best-effort, see the gotcha → internal/store/audit.go:110-139
- Outbox over a post-COMMIT send: a crash between COMMIT and send leaves no way to tell whether the event went out → docs/ARCHITECTURE.md:263-266
