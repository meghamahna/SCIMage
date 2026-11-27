---
id: decision-credentials-issued-by-cli
kind: decision
title: Tenant and console credentials are issued by the CLI, never over HTTP
summary: No network endpoint can create a SCIM token or console token; scimage-admin writes them straight to Postgres and audits the action
tags: [decision, token, credential, cli, scimage-admin, console-token, attack-surface, admin-audit, why]
links: [related:entrypoint-cmd-scimage-admin-main, related:module-console-auth, related:data-model-tenants-tokens, related:decision-token-hashing, related:decision-scim-uniform-401]
sources: [docs/ARCHITECTURE.md:119-133, internal/console/server.go:8-12, cmd/scimage-admin/main.go:81-93, cmd/scimage-admin/main.go:141-226, cmd/scimage-admin/main.go:229-302]
updated: 2026-11-26
verified_commit: 794ebee
---
## Decision
- cmd/scimage-admin creates tenants and issues, lists and revokes tokens by talking to Postgres directly → docs/ARCHITECTURE.md:119-122
- Token issue and revoke subcommands → cmd/scimage-admin/main.go:141-226
- Console tokens are issued and revoked only through the CLI (`console-token`), never through the console → internal/console/server.go:8-12
- Each privileged action writes an admin_audit_log row in the same transaction; a no-op revoke writes nothing → docs/ARCHITECTURE.md:124-133
- The actor defaults to $USER or $USERNAME and can be overridden with -created-by → cmd/scimage-admin/main.go:81-93
## Why
- No HTTP endpoint can create a credential, which is a smaller attack surface than an admin API → docs/ARCHITECTURE.md:119-122
- Each issued credential is attributed to the operator who ran the command → docs/ARCHITECTURE.md:124-129
