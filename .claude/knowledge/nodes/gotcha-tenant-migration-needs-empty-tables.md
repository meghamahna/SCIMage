---
id: gotcha-tenant-migration-needs-empty-tables
kind: gotcha
title: Migration 000005 only applies cleanly to empty tables, and its down is lossy
summary: The tenant_id columns are NOT NULL with no default, so existing rows need a real tenant first; the down migration restores global unique indexes that can fail
tags: [gotcha, migration, 000005, tenant_id, not null, backfill, multi-tenancy, upgrade, rollback, existing data]
links: [related:workflow-migrations, related:decision-tenant-scoping, related:data-model-core-users-groups]
sources: [migrations/000005_multi_tenancy.up.sql:6-13, migrations/000005_multi_tenancy.up.sql:40, migrations/000005_multi_tenancy.up.sql:53, migrations/000005_multi_tenancy.up.sql:56, migrations/000005_multi_tenancy.down.sql:1-13]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- The migration header says the new tenant_id columns are NOT NULL with no default → migrations/000005_multi_tenancy.up.sql:6-8
- So it is safe on a fresh database; an existing deployment must backfill a tenant on every row first → migrations/000005_multi_tenancy.up.sql:9-13
- The NOT NULL columns are on users, audit_log and webhook_deliveries → migrations/000005_multi_tenancy.up.sql:40, 53, 56
- The down migration drops tenant_id, tenant_attributes-free tables scim_tokens and tenants, and recreates the global userName and externalId indexes → migrations/000005_multi_tenancy.down.sql:1-13
- Recreating those global unique indexes fails if two tenants now share a userName or externalId, so a rollback after multi-tenant use needs manual cleanup → migrations/000005_multi_tenancy.down.sql:6-10
