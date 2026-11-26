---
id: module-aria
kind: module
title: ARIA audit reviewer
summary: Audit Risk Intelligence Advisor: deterministic Go flags on audit_log activity, then an optional OpenAI-compatible LLM narrates them; advisory only
tags: [aria, audit, audit review, audit risk, bulk deactivation, denial burst, off-hours, high volume, llm, narrate, briefing, ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY, ARIA_LLM_MODEL, ARIA_TIMEZONE]
links: [implements:decision-aria-advisory-only, depends_on:data-model-audit-log, depends_on:module-store, related:api-console-routes, related:entrypoint-cmd-aria-main, related:module-console]
sources: [internal/aria/aria.go:1-34, internal/aria/aria.go:41-45, internal/aria/aria.go:97-187, internal/aria/client.go:26-64, internal/aria/client.go:103-149, internal/aria/prompt.go:14-40, internal/aria/prompt.go:45-108]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Split: Go computes every figure (Detect), the LLM only writes prose about the Report → internal/aria/aria.go:1-9
- Four flags, most severe first: bulk_deactivation, denial_burst, off_hours, high_volume → internal/aria/aria.go:41-44, internal/aria/aria.go:146-184
- Bulk deactivation: ≥5 deactivate or delete by one caller within a 10-minute window (sliding max cluster) → internal/aria/aria.go:23-24, internal/aria/aria.go:147-156, internal/aria/aria.go:201-224
- Denial burst: ≥5 denied results per caller in the window → internal/aria/aria.go:26, internal/aria/aria.go:158-167
- Off-hours: any mutation before 07:00, at or after 19:00, or on a weekend in the report timezone → internal/aria/aria.go:28-31, internal/aria/aria.go:168-174, internal/aria/aria.go:189-196
- High volume: ≥50 total entries per caller in the window → internal/aria/aria.go:25, internal/aria/aria.go:175-184
- Thresholds are constants, not config; the comment favours a low bar that over-flags → internal/aria/aria.go:19-21
- Before and after images are dropped; the report carries actor token, action, result and target id only → internal/aria/aria.go:47-58, internal/aria/aria.go:255-265
- Examples capped at 5 rows per flag → internal/aria/aria.go:33, internal/aria/aria.go:226-245
- Report.Truncated is set by the caller when a full store page came back, and the header warns about it → cmd/aria/main.go:82-86, internal/aria/prompt.go:63-65
- LLM config is read from ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY, ARIA_LLM_MODEL; all three required → internal/aria/client.go:32-56
- Enabled() reports presence only, never values; the console uses it to show the feature → internal/aria/client.go:58-64
- Client posts to {BASE_URL}/chat/completions with a Bearer key, 60s timeout, 1 MiB response cap → internal/aria/client.go:17-19, internal/aria/client.go:116-121
- Any OpenAI-compatible chat-completions endpoint works; provider is not hard-coded → internal/aria/client.go:22-25
- System prompt bans advice, intent guessing, and em-dashes; it narrates only the given facts → internal/aria/prompt.go:14-33
- Header (tenant, window, totals) is printed deterministically above the model text → internal/aria/prompt.go:45-68, cmd/aria/main.go:110-112
- Quiet windows print Render output and make no LLM call → internal/aria/prompt.go:82-85, cmd/aria/main.go:91-94
- Console narration is a POST under /console/aria/narrate, which recomputes the report → internal/console/server.go:135, internal/console/handlers.go:576-586
- Detect does not assume store ordering; it sorts timestamps itself → internal/aria/aria.go:94-96, internal/aria/aria.go:206-208
