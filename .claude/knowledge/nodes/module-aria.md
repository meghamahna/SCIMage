---
id: module-aria
kind: module
title: ARIA audit reviewer
summary: Audit Risk Intelligence Advisor: deterministic Go flags on audit_log activity, an uncapped count that marks partial windows, then an optional OpenAI-compatible LLM narrates them; advisory only
tags: [aria, audit, audit review, audit risk, bulk deactivation, denial burst, off-hours, high volume, partial review, truncated, newest N of M, llm, narrate, briefing, ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY, ARIA_LLM_MODEL, ARIA_TIMEZONE]
links: [implements:decision-aria-advisory-only, depends_on:data-model-audit-log, depends_on:module-store, related:api-console-routes, related:entrypoint-cmd-aria-main, related:module-console, related:gotcha-aria-partial-review-count]
sources: [internal/aria/aria.go:1-9, internal/aria/aria.go:21-35, internal/aria/aria.go:40-46, internal/aria/aria.go:74-115, internal/aria/aria.go:127-216, internal/aria/aria.go:231-240, internal/aria/aria.go:270-287, internal/aria/client.go:17-64, internal/aria/client.go:73-76, internal/aria/client.go:103-121, internal/aria/prompt.go:14-33, internal/aria/prompt.go:45-74, cmd/aria/main.go:82-85, internal/console/templates/aria.html:30-33]
updated: 2026-11-27
verified_commit: 7f7fe4d
---
## Facts
- Split: Go computes every figure (Detect), the LLM only writes prose about the Report → internal/aria/aria.go:1-9
- Four flags, most severe first: bulk_deactivation, denial_burst, off_hours, high_volume → internal/aria/aria.go:40-46
- Bulk deactivation: at least 5 deactivate or delete by one caller within a 10-minute window (sliding max cluster) → internal/aria/aria.go:24-26, internal/aria/aria.go:231-240
- Denial burst: at least 5 denied results per caller in the window → internal/aria/aria.go:28, internal/aria/aria.go:127-216
- Off-hours: any mutation before 07:00, at or after 19:00, or on a weekend in the report timezone → internal/aria/aria.go:32-33, internal/aria/aria.go:219-229
- High volume: at least 50 total entries per caller in the window → internal/aria/aria.go:27, internal/aria/aria.go:127-216
- Thresholds are constants, not config; the comment favours a low bar that over-flags → internal/aria/aria.go:21-23
- Detect sorts timestamps itself, so it does not assume store ordering → internal/aria/aria.go:231-240
- Event keeps actor token, action, result and target id only; before and after images are dropped → internal/aria/aria.go:52-62, internal/aria/aria.go:285-287
- Examples are capped at 5 rows per flag → internal/aria/aria.go:35, internal/aria/aria.go:270-275
- Report.Truncated means the review may be missing older in-window entries; Report.InWindow is the uncapped window size when counted → internal/aria/aria.go:82-83
- MarkPartial runs after the list read: it does nothing below store.MaxPageSize, else counts the window with the same predicate and sets InWindow and Truncated = count > Total → internal/aria/aria.go:96-115
- A count error sets Truncated, still flags the window, and returns a wrapped error for the caller to log or print → internal/aria/aria.go:106-111
- The count runs after the read, so a row written in between only raises the count: a race can over-flag, never miss → internal/aria/aria.go:97-102
- Partial notice text: console "Partial review" card → internal/console/templates/aria.html:30-33; prompt and CLI header "newest N of M" → internal/aria/prompt.go:63-68
- LLM config is read from ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY, ARIA_LLM_MODEL; all three are required → internal/aria/client.go:32-56
- Enabled() reports presence only, never values; the console uses it to show the feature → internal/aria/client.go:58-64
- Client posts to {BASE_URL}/chat/completions with a Bearer key, 60s timeout, 1 MiB response cap → internal/aria/client.go:17-19, internal/aria/client.go:73-76, internal/aria/client.go:116-121
- Any OpenAI-compatible chat-completions endpoint works; provider is not hard-coded → internal/aria/client.go:22-25
- System prompt bans advice, intent guessing and em-dashes; it narrates only the given facts → internal/aria/prompt.go:14-33
- Header (tenant, window, totals, partial note) is printed deterministically above the model text → internal/aria/prompt.go:45-74
- Quiet windows print Render output and make no LLM call → cmd/aria/main.go:90-93
- Console narration is a POST under /console/aria/narrate, which recomputes the report → internal/console/handlers.go:727-746
