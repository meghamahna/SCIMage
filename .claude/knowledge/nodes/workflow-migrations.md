---
id: workflow-migrations
kind: workflow
title: Database migrations (golang-migrate)
summary: Numbered golang-migrate SQL files in /migrations, applied by make migrate against the Compose Postgres through scripts/migrate.sh
tags: [migrations, golang-migrate, migrate, schema, sql, ddl, make migrate, down migration, migrate-down, trigger, partial index, check constraint]
links: [related:data-model-core-users-groups, related:data-model-tenants-tokens, related:data-model-audit-log, related:data-model-webhook-outbox, related:gotcha-tenant-migration-needs-empty-tables, related:command-dev-commands, documented_in:doc-architecture]
sources: [migrations/000001_create_users_table.up.sql:1-19, migrations/000012_console_tokens.up.sql:1-29, migrations/000009_audit_log_fault_injection.up.sql:6-17, Makefile:73-82, scripts/migrate.sh:1-2, docs/ARCHITECTURE.md:104-107]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Facts
- Files are migrations/NNNNNN_<name>.up.sql and .down.sql, six digits, 000001 to 000012 → migrations/000001_create_users_table.up.sql:1
- Apply with `make migrate`, which runs scripts/migrate.sh and needs no host migrate binary; also `migrate-down` and `migrate-version` → Makefile:73-82
- The script runs golang-migrate against the compose Postgres only, not DATABASE_URL → scripts/migrate.sh:1-2
- Each up file has a matching down file that reverses it (drop columns, tables or indexes) → migrations/000003_add_users_external_id.down.sql:1-2
- Header comments explain why each table exists; read the top of a file before changing it → migrations/000004_create_webhook_deliveries.up.sql:1-7
- Optional unique values use partial unique indexes WHERE col IS NOT NULL → migrations/000003_add_users_external_id.up.sql:8-11
- Case-insensitive uniqueness uses an index on lower(column) → migrations/000001_create_users_table.up.sql:16-19
- Status values are a CHECK constraint, not a Postgres enum → migrations/000004_create_webhook_deliveries.up.sql:30
- Relaxing a constraint is an ALTER, e.g. admin_audit_log.tenant_id DROP NOT NULL → migrations/000012_console_tokens.up.sql:29
- Migration 000009 adds a BEFORE INSERT trigger on audit_log as a test lever (see data-model-audit-log) → migrations/000009_audit_log_fault_injection.up.sql:6-17, docs/ARCHITECTURE.md:104-107
- Migration 000005 is not safe on populated tables → gotcha-tenant-migration-needs-empty-tables
