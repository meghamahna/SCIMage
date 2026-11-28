---
id: entrypoint-cmd-aria-main
kind: entrypoint
title: Entry: cmd/aria/main.go (ARIA CLI)
summary: Read-only CLI that reviews audit_log for a window, marks partial windows, prints flags, and calls the LLM only when there are findings; flags -tenant, -since, -timezone
tags: [entrypoint, main, cli, aria, audit review, briefing, since, timezone, ARIA_TIMEZONE, ARIA_LLM_BASE_URL, command, flags, partial review]
links: [calls:module-aria, depends_on:module-store, implements:decision-aria-advisory-only, related:entrypoint-cmd-scimage-admin-main, related:gotcha-aria-partial-review-count]
sources: [cmd/aria/main.go:1-7, cmd/aria/main.go:24-29, cmd/aria/main.go:31-35, cmd/aria/main.go:49-71, cmd/aria/main.go:73-85, cmd/aria/main.go:90-112, cmd/aria/main.go:118-154]
updated: 2026-11-27
verified_commit: 7f7fe4d
---
## Facts
- Advisory-only by its own header; output goes to stdout, nothing written back → cmd/aria/main.go:1-7
- main prints "aria: <err>" to stderr and exits 1 → cmd/aria/main.go:24-29
- Flags: -tenant (all tenants if empty), -since (default 24h), -timezone → cmd/aria/main.go:32-35
- -since accepts "Nd" day counts or a Go duration, of any length; must be positive → cmd/aria/main.go:118-138
- Timezone: -timezone, then ARIA_TIMEZONE, then host local zone → cmd/aria/main.go:141-154
- DSN from store.DSNFromEnv; -tenant is resolved first so an unknown id fails fast → cmd/aria/main.go:49-71
- Window read: ListAuditEntriesSince from now minus window → cmd/aria/main.go:73-79
- After the read, MarkPartial sets the partial flag; a count error is printed to stderr as a warning and the run continues → cmd/aria/main.go:82-85
- No findings: prints the deterministic render and returns, with no LLM env needed → cmd/aria/main.go:90-93
- With findings: needs ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY, ARIA_LLM_MODEL → cmd/aria/main.go:95-101, internal/aria/client.go:32-56
- Output is the header, a blank line, then the model summary → cmd/aria/main.go:110-112
