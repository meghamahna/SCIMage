---
id: data-model-audit-log
kind: data-model
title: Audit log tables (audit_log and admin_audit_log)
summary: Append-only trails for every SCIM mutation and refusal, and every privileged admin action, written inside the mutating transaction
tags: [audit, audit_log, admin_audit_log, governance, provenance, before after, refusal, denied, aria, trail]
links: [decided_by:decision-audit-in-transaction, related:module-store, related:gotcha-refused-mutation-audit-best-effort, related:data-model-tenants-tokens]
sources: [migrations/000002_create_audit_log.up.sql:1-28, migrations/000005_multi_tenancy.up.sql:53-54, migrations/000006_admin_governance.up.sql:17-34, migrations/000008_audit_log_resource_type.up.sql:1-6, migrations/000009_audit_log_fault_injection.up.sql:1-17, migrations/000012_console_tokens.up.sql:23-29, internal/store/audit.go:14-60, internal/store/adminaudit.go:9-52]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- `audit_log` columns: id BIGSERIAL, at, actor_token (token key id, never the secret), actor_ip, action, result, detail, target_id UUID, before/after JSONB → migrations/000002_create_audit_log.up.sql:6-21
- `resource_type` (default 'user') separates user and group images in one table → migrations/000008_audit_log_resource_type.up.sql:6
- `tenant_id` NOT NULL, FK tenants, with index → migrations/000005_multi_tenancy.up.sql:53-54
- No FK from target_id to users, so history survives hard deletes and refusals have no target → migrations/000002_create_audit_log.up.sql:15-16
- Indexes: (at DESC) and partial (target_id) WHERE NOT NULL → migrations/000002_create_audit_log.up.sql:24,27
- Actions: create, replace, deactivate, delete; results: success, denied → internal/store/audit.go:16-29
- Writer `insertAudit` maps empty strings to NULL → internal/store/audit.go:80-106
- Reads: ListAuditEntries (newest first, tenant-filtered, capped), ListAuditEntriesSince, AuditWindowStats → internal/store/audit.go:116-122, 151-156, 186
- `admin_audit_log`: id, at, tenant_id, actor, action, target_id, detail; index (tenant_id, at DESC) → migrations/000006_admin_governance.up.sql:24-34
- admin_audit_log.tenant_id is nullable so console.token.* system actions can be stored → migrations/000012_console_tokens.up.sql:23-29
- Admin action names: tenant.create, token.issue, token.revoke, attribute.register, attribute.unregister, webhook.replay, console.token.issue, console.token.revoke → internal/store/adminaudit.go:13-23
- Writer `insertAdminAudit` runs inside the caller's transaction → internal/store/adminaudit.go:44-52
- Gotcha: migration 000009 adds a BEFORE INSERT trigger on audit_log that raises when the GUC scimage.simulate_audit_failure is 'true'; it is a test lever living in the production schema → migrations/000009_audit_log_fault_injection.up.sql:6-17
