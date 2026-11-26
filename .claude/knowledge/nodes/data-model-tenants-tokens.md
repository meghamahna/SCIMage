---
id: data-model-tenants-tokens
kind: data-model
title: Tenants, SCIM tokens, console tokens and tenant attributes
summary: Tenant isolation root, hashed per-tenant bearer tokens, a system-wide console credential, and the per-tenant extra attribute registry
tags: [tenant, tenants, scim_tokens, token, bearer, sha256, key_id, console_tokens, tenant_attributes, extensible attributes, revoke, rotation]
links: [related:decision-token-hashing, related:decision-tenant-scoping, related:module-store, related:data-model-core-users-groups, related:data-model-audit-log]
sources: [migrations/000005_multi_tenancy.up.sql:1-38, migrations/000006_admin_governance.up.sql:1-15, migrations/000010_extended_attributes.up.sql:14-25, migrations/000012_console_tokens.up.sql:1-21, internal/store/tenant.go:31-37, internal/store/token.go:23-47]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- `tenants`: id TEXT (app-generated, not a UUID), name, created_at, created_by (nullable) → migrations/000005_multi_tenancy.up.sql:15-19
- Tenant id format `tenant_` + 16 hex chars from crypto/rand → internal/store/tenant.go:31-37
- Tenant names are unique case-insensitively → migrations/000006_admin_governance.up.sql:10
- `scim_tokens`: key_id TEXT PK, tenant_id FK, secret_hash BYTEA, label, created_by, last_used_at, expires_at, revoked_at → migrations/000005_multi_tenancy.up.sql:25-35
- Several live tokens per tenant are allowed (rotation without downtime) → migrations/000005_multi_tenancy.up.sql:20-24
- Token wire format `scimage_<keyID>_<secret>`; key id 8 bytes, secret 32 bytes → internal/store/token.go:17, internal/store/token.go:23-26
- ParseToken splits on the first underscore after the prefix → internal/store/token.go:47-58
- Only sha256(secret) is stored; plaintext is returned once from IssueToken → internal/store/token.go:64-74
- RevokeToken sets revoked_at and writes an admin audit row in the same transaction → internal/store/token.go:156-186
- `console_tokens`: same shape as scim_tokens but no tenant_id; system-wide operator credential → migrations/000012_console_tokens.up.sql:1-21
- Console token issue writes an admin audit row with NULL tenant_id → internal/store/consoletoken.go:54-84
- `tenant_attributes`: PK (tenant_id, name), type default 'string', created_by → migrations/000010_extended_attributes.up.sql:18-25
- Reserved core attribute names are rejected at registration → internal/store/attribute.go:27-44
