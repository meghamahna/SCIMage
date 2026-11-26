---
id: entrypoint-cmd-aria-main
kind: entrypoint
title: Entry: cmd/aria/main.go (ARIA CLI)
summary: Read-only CLI that reviews audit_log for a window, prints flags, and calls the LLM only when there are findings; flags -tenant, -since, -timezone
tags: [entrypoint, main, cli, aria, audit review, briefing, since, timezone, ARIA_TIMEZONE, ARIA_LLM_BASE_URL, command, flags]
links: [calls:module-aria, depends_on:module-store, implements:decision-aria-advisory-only, related:entrypoint-cmd-scimage-admin-main]
sources: [cmd/aria/main.go:1-8, cmd/aria/main.go:24-29, cmd/aria/main.go:31-114, cmd/aria/main.go:119-155]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Advisory-only by its own header; output goes to stdout, nothing written back → cmd/aria/main.go:1-7
- main prints "aria: <err>" to stderr and exits 1 → cmd/aria/main.go:24-29
- Flags: -tenant (all tenants if empty), -since (default 24h), -timezone → cmd/aria/main.go:32-35
- -since accepts "Nd" day counts or a Go duration; must be positive → cmd/aria/main.go:119-138
- Timezone: -timezone, then ARIA_TIMEZONE, then host local zone → cmd/aria/main.go:142-155
- DSN from store.DSNFromEnv; -tenant is resolved first so an unknown id fails fast → cmd/aria/main.go:49-71
- Window read: ListAuditEntriesSince from now minus window → cmd/aria/main.go:73-79
- Truncated when the read hit store.MaxPageSize (newest first) → cmd/aria/main.go:82-86
- No findings: prints the deterministic render and returns, with no LLM env needed → cmd/aria/main.go:91-94
- With findings: needs ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY, ARIA_LLM_MODEL → cmd/aria/main.go:96-102, internal/aria/client.go:32-56
- Output is the header, a blank line, then the model summary → cmd/aria/main.go:110-112
