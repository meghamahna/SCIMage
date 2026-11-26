---
id: entrypoint-cmd-scimage-admin-main
kind: entrypoint
title: Entry: cmd/scimage-admin/main.go (admin CLI)
summary: Operator CLI with six groups (tenant, token, console-token, attribute, audit, webhook); opens the store directly from the environment and never starts a listener
tags: [entrypoint, main, cli, admin, scimage-admin, subcommands, tenant, token, console-token, attribute, audit, webhook replay, usage, command]
links: [calls:module-store, calls:module-webhook, depends_on:module-store, related:data-model-tenants-tokens, related:data-model-audit-log, related:architecture-overview]
sources: [cmd/scimage-admin/main.go:21-61, cmd/scimage-admin/main.go:63-79, cmd/scimage-admin/main.go:85-93, cmd/scimage-admin/main.go:470-476, cmd/scimage-admin/main.go:481-502]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- main → run(os.Args[1:]); any error prints "scimage-admin:" to stderr and exits 1 → cmd/scimage-admin/main.go:21-26
- run needs at least a group and an action; otherwise usage → cmd/scimage-admin/main.go:28-31
- Reads DSN via store.DSNFromEnv and opens the store with no options (so no outbox writes) → cmd/scimage-admin/main.go:33-39, internal/store/store.go:51-53
- Dispatch by group name → cmd/scimage-admin/main.go:45-60
- Subcommands (usage text) → cmd/scimage-admin/main.go:63-79
- tenant: create -name [-created-by], list → cmd/scimage-admin/main.go:95-139
- token: issue -tenant -label [-expires 90d] [-created-by], list -tenant, revoke <keyID> → cmd/scimage-admin/main.go:141-226
- console-token: issue -label [-expires] [-created-by], list, revoke <keyID> (system-wide, not tenant-scoped) → cmd/scimage-admin/main.go:229-302
- attribute: register -tenant -name [-type], list -tenant, unregister -tenant -name → cmd/scimage-admin/main.go:306-384
- audit: list [-tenant] → cmd/scimage-admin/main.go:386-413
- webhook: replay <deliveryID> (dead letter back to pending), replay-all → cmd/scimage-admin/main.go:417-465
- -created-by defaults to $USER, then $USERNAME, then "unknown" → cmd/scimage-admin/main.go:81-93
- -expires accepts a day count such as 90d or any Go duration → cmd/scimage-admin/main.go:478-502
- Generated SCIM base URL is SCIM_BASE_URL + /scim/v2/<tenantID>, with a placeholder when unset; never stored → cmd/scimage-admin/main.go:466-476
