---
id: gotcha-aria-partial-review-count
kind: gotcha
title: ARIA partial-review flag depends on a second, uncapped count
summary: A full 200-row read does not prove the window is complete, so ARIA counts the same window uncapped; a failed count still flags the review as partial
tags: [gotcha, aria, partial review, truncated, MaxPageSize, cap, count, audit window, newest N of M, race]
links: [related:module-aria, related:entrypoint-cmd-aria-main, related:module-store, related:data-model-audit-log]
sources: [internal/aria/aria.go:96-115, internal/store/audit.go:198-250, internal/store/user.go:99]
updated: 2026-11-27
verified_commit: 7f7fe4d
---
## Facts
- The list is capped at MaxPageSize (200) newest first, so a full batch may be the whole window or only its newest slice → internal/store/user.go:99, internal/aria/aria.go:97-101
- Counting with the list's own predicate is what makes the two agree; the count has no LIMIT → internal/store/audit.go:228-235, internal/store/audit.go:239-245
- Count after read: a row inserted between the two only raises the count, so the flag can be a false positive, never a miss → internal/aria/aria.go:97-102
- A failed count sets Truncated anyway, because hiding a bound is worse than overstating it → internal/aria/aria.go:106-111
