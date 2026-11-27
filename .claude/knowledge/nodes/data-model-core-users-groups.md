---
id: data-model-core-users-groups
kind: data-model
title: Users, groups and group membership tables
summary: SCIM User and Group rows per tenant, with case-insensitive per-tenant uniqueness, soft-deleted users and cascading membership
tags: [users, groups, group_members, schema, scim user, scim group, membership, uniqueness, externalId, userName, duplicate externalId]
links: [depends_on:data-model-tenants-tokens, related:module-store, related:decision-tenant-scoping, related:gotcha-tenant-migration-needs-empty-tables, related:api-scim-errors]
sources: [migrations/000001_create_users_table.up.sql:1-19, migrations/000003_add_users_external_id.up.sql:5-11, migrations/000005_multi_tenancy.up.sql:40-51, migrations/000007_create_groups.up.sql:1-43, migrations/000010_extended_attributes.up.sql:11, internal/store/user.go:20-40, internal/store/user.go:170-176, internal/store/user.go:388-410, internal/store/group.go:145-169, internal/store/group.go:189-193, internal/store/group.go:228-234]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- `users`: id UUID, user_name, given/family name, email (primary value only), active (default true), external_id, extended_attributes JSONB, tenant_id, created/updated timestamps → migrations/000001_create_users_table.up.sql:5-14
- Email is flattened to one value; multi-valued emails would need a child table → migrations/000001_create_users_table.up.sql:1-3
- external_id added as nullable column → migrations/000003_add_users_external_id.up.sql:5
- extended_attributes added as nullable JSONB, so extra SCIM attributes need no schema change → migrations/000010_extended_attributes.up.sql:11
- userName unique per tenant, case-insensitive: index on (tenant_id, lower(user_name)) → migrations/000005_multi_tenancy.up.sql:44-45
- externalId unique per tenant where not null (partial index, idx_users_tenant_external_id) → migrations/000005_multi_tenancy.up.sql:50-51
- A duplicate externalId surfaces as ErrDuplicateExternalID, distinct from a userName clash → internal/store/user.go:170-176
- tenant_id is NOT NULL and references tenants(id) → migrations/000005_multi_tenancy.up.sql:40
- Users are soft-deleted: DeactivateUser sets active=false and the row stays → internal/store/user.go:388-395
- `groups`: id UUID, tenant_id, external_id, display_name, timestamps. No `active` column → migrations/000007_create_groups.up.sql:1-11
- Group displayName unique per tenant, case-insensitive → migrations/000007_create_groups.up.sql:16
- Group externalId unique per tenant where not null (idx_groups_tenant_external_id) → migrations/000007_create_groups.up.sql:20
- Duplicate group externalId surfaces as ErrDuplicateExternalID → internal/store/group.go:232-233
- Deleting a group is a real DELETE → migrations/000007_create_groups.up.sql:1-3
- `group_members`: PK (group_id, user_id), both FKs ON DELETE CASCADE, tenant_id denormalised so queries stay `WHERE tenant_id = $1` → migrations/000007_create_groups.up.sql:32-38
- Reverse lookup (a user's groups) indexed on user_id → migrations/000007_create_groups.up.sql:43
- Membership replace clears, dedupes, then inserts; only users of the same tenant are accepted → internal/store/group.go:145-160
- A member count mismatch returns ErrInvalidMember (fail closed, no silent drop) → internal/store/group.go:166
- Go structs: User and Change (before/after pair for audit) → internal/store/user.go:20, internal/store/user.go:74
- Group rows are locked with FOR UPDATE before membership changes → internal/store/group.go:189-193
- User rows use FOR NO KEY UPDATE so the group FK does not block them → internal/store/user.go:330-336
## Gotchas
- A name differing only by case is a duplicate; uniqueness is on lower(...) → migrations/000001_create_users_table.up.sql:16-18
