---
id: decision-audit-in-transaction
kind: decision
title: Audit and outbox rows commit with the change they describe
summary: Successful user and group mutations write their audit_log row and any webhook outbox row inside the change's transaction, so a committed change always has a record; refusals are recorded best-effort outside a transaction (see the gotcha)
tags: [decision, audit, audit_log, transaction, atomic, outbox, governance, why, rollback, store, compliance, actor, admin_audit_log, patch, row lock]
links: [related:data-model-audit-log, related:data-model-webhook-outbox, related:module-store, related:module-scim, related:api-scim-patch, related:gotcha-refused-mutation-audit-best-effort]
sources: [migrations/000002_create_audit_log.up.sql:1-4, migrations/000004_create_webhook_deliveries.up.sql:1-7, internal/store/user.go:153-189, internal/store/user.go:290-319, internal/store/user.go:335-370, internal/store/group.go:211-256, internal/store/audit.go:46-113, internal/store/adminaudit.go:44, internal/store/outbox.go:192-280, internal/scim/userstore.go:24-34, internal/scim/groupstore.go:13-25, internal/scim/handler.go:528-541]
updated: 2026-11-26
verified_commit: 4185d2b
---
## Decision
- The store, not the handler, writes the audit row inside the mutation's transaction, so a handler cannot skip it → migrations/000002_create_audit_log.up.sql:1-4
- Every mutating UserStore/GroupStore method takes an AuditRecord; handlers never write audit rows themselves → internal/scim/userstore.go:24-34, internal/scim/groupstore.go:13-25
- Admin actions (IssueToken, RevokeToken, CreateTenant, RegisterAttribute/UnregisterAttribute, RevokeConsoleToken, ReplayDeadLetter) take an actor string and write admin_audit_log via insertAdminAudit → internal/store/adminaudit.go:44
- Outbox state changes (MarkDelivered, RescheduleDelivery, DeadLetterDelivery, PurgeDeliveredBefore, ClaimDueDeliveries) write no audit row → internal/store/outbox.go:192-280
- AuditRecord carries actor (token key id), client IP and tenant, all taken from the authenticated request → internal/scim/handler.go:528-531
- Client IP comes from RemoteAddr only; X-Forwarded-For is ignored because a caller could forge it → internal/scim/handler.go:533-541
- If a successful mutation's audit insert fails, the mutation rolls back with it; a file log cannot give that guarantee because Postgres and a filesystem share no commit → migrations/000002_create_audit_log.up.sql:1-4
- Outbound webhook events are queued in the same transaction, not sent after COMMIT, so a crash cannot lose or invent an event → migrations/000004_create_webhook_deliveries.up.sql:1-7
- Pattern in code: insert row, insertAudit, enqueueChange, Commit → internal/store/user.go:153-189
- Update and PATCH paths lock the row first; the before-image is read in the same statement as the UPDATE, then the audit and change rows are written in the same transaction → internal/store/user.go:335-370
- PATCH also runs its read, apply and write under one row lock, so the audit before-image is the state the patch was computed from → internal/store/user.go:290-319, internal/store/group.go:359-391
- Group create follows the same pattern → internal/store/group.go:211-256
- Refusals are not in the transaction: auditRefusal writes via the pool because no row changed; a failed insert is logged and swallowed → internal/store/audit.go:105-113
- Actor identity comes from the auth middleware through AuditRecord, not from mutation arguments → internal/store/audit.go:46-52
- Empty audit strings are stored as NULL so absent reads as absent → internal/store/audit.go:80-106
## Why
- Enterprise provisioning needs proof of who changed what, and a committed change with no record is a compliance gap.
- Refusals are recorded too, because a burst of denials is itself a signal; they are best-effort, see the gotcha → internal/store/audit.go:105-113
- Outbox over a post-COMMIT send: a crash between COMMIT and send leaves no way to tell whether the event went out → docs/ARCHITECTURE.md:212-220
