---
id: decision-tenant-scoping
kind: decision
title: One database, many tenants, isolation by tenant_id in SCIM queries
summary: Tenants share the same tables; SCIM user and group reads and writes filter on tenant_id, with uniqueness enforced per tenant. Some store paths deliberately span tenants (see Exceptions)
tags: [decision, tenant, tenancy, multi-tenant, isolation, tenant_id, scoping, cross-tenant, exceptions]
links: [related:data-model-tenants-tokens, related:data-model-core-users-groups, related:module-store, related:gotcha-tenant-migration-needs-empty-tables]
sources: [migrations/000005_multi_tenancy.up.sql:1-13, migrations/000005_multi_tenancy.up.sql:44-56, internal/store/user.go:56-58, internal/store/user.go:197-203, internal/store/group.go:145-156, internal/store/tenant.go:32-38, internal/store/audit.go:198-206, internal/store/audit.go:233-241, internal/store/adminaudit.go:57-66, internal/scim/auth.go:109-132, internal/store/outbox.go:192-207, internal/store/outbox.go:280-282]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Decision
- One deployment serves many customers; a tenant is the isolation boundary, scoped by tenant_id rather than separate infrastructure → migrations/000005_multi_tenancy.up.sql:1-4
- Every tenant-owned table has tenant_id NOT NULL with FK to tenants → migrations/000005_multi_tenancy.up.sql:40,53,56; migrations/000007_create_groups.up.sql:6,35
- Global uniqueness became per-tenant for userName and externalId, since two customers may both have a "bjensen" → migrations/000005_multi_tenancy.up.sql:44-51
- User store reads take tenantID and filter on it: the UserFilter clause starts with tenant_id = $1 → internal/store/user.go:56-58
- GetUser matches tenant_id and id together, so a cross-tenant id gets the same not-found as a made-up id → internal/store/user.go:197-203
- Group membership only accepts users of the same tenant → internal/store/group.go:155-156
- Tenant ids are app-generated and stable, since they are pasted into a customer's IdP as part of the SCIM base URL → migrations/000005_multi_tenancy.up.sql:9-13, internal/store/tenant.go:32-38
- Exception (no tenant filter by design): console_tokens (system-wide operator) and console.token.* admin entries (NULL tenant_id) → migrations/000012_console_tokens.up.sql:1-8
- Exception: ListAuditEntriesSince and AuditWindowStats treat an empty tenantID as all tenants → internal/store/audit.go:202-206, internal/store/audit.go:237-241
- Exception: ListAdminAuditEntries treats an empty tenantID as all tenants → internal/store/adminaudit.go:64-66
- Exception: GetTokenByKeyID looks up by key id alone; the tenant match is checked afterwards in the auth middleware → internal/scim/auth.go:109, internal/scim/auth.go:132
- Exception: outbox claim and purge queries run across all tenants (ClaimDueDeliveries, PurgeDeliveredBefore) → internal/store/outbox.go:192-207, internal/store/outbox.go:280-282
- Callers must never pass a request-supplied tenant value to the audit read paths, because an empty value means all tenants → internal/store/audit.go:198-206, internal/store/adminaudit.go:64-66
