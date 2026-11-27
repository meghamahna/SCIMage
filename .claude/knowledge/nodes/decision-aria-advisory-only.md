---
id: decision-aria-advisory-only
kind: decision
title: ARIA is advisory only and never feeds provisioning or auth
summary: ARIA output is shown to a human and never written back to the store or consulted by the SCIM auth path; deterministic flags come before any LLM text
tags: [aria, advisory, read-only, llm, authorization, provisioning, auth path, security design, guardrail, console, narrative]
links: [decided_by:module-aria, related:module-aria, related:module-scim, related:decision-audit-in-transaction]
sources: [internal/aria/aria.go:1-9, internal/aria/client.go:100-102, internal/aria/prompt.go:9-13, cmd/aria/main.go:1-7, internal/console/handlers.go:706-762, .claude/CLAUDE.md:92-96]
updated: 2026-11-26
verified_commit: 794ebee
---
## Facts
- Design rule: the model never detects, decides or touches the store or auth path → internal/aria/aria.go:6-9
- Summarize has no audit side effects; its string is only printed → internal/aria/client.go:100-102
- The system prompt forbids recommending create, change, deactivate or delete → internal/aria/prompt.go:25-27
- cmd/aria reads the store but never writes; its output goes to stdout → cmd/aria/main.go:1-7, cmd/aria/main.go:91-112
- Console path: handleARIANarrate shows the LLM text to the operator and caches it in memory per tenant and window; the text is still advisory and never re-enters the store or auth path → internal/console/handlers.go:706-709, internal/console/handlers.go:730-748
- Console prompt carries only the deterministic findings; the model adds phrasing, not signals → internal/aria/prompt.go:9-13, internal/console/handlers.go:755-762
- Project guide: AI must never influence authorization or provisioning; stop and flag such a task → .claude/CLAUDE.md:92-96
- Config is read from the environment only; the key is never a flag or logged → internal/aria/client.go:32-34
