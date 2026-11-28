---
id: component-scim-ratelimit
kind: module
title: SCIM per-caller rate limiter
summary: Token-bucket limiter keyed by tenant and key id, placed after auth; 429 tooMany on excess with one Warn log line; env-tunable
tags: []
links: [implements:module-scim, depends_on:component-scim-auth, related:gotcha-scim-unauthenticated-not-limited, related:api-scim-errors]
sources: [internal/scim/ratelimit.go:1-109, internal/scim/handler.go:93-98]
updated: 2026-11-28
verified_commit: 924cf37
---
## Facts
- Token bucket per caller using golang.org/x/time/rate; one bucket per key, created on first use → internal/scim/ratelimit.go:23-36, internal/scim/ratelimit.go:57-67
- Key is TenantID + "/" + KeyID of the authenticated identity → internal/scim/ratelimit.go:96
- Defaults: 20 requests/s sustained, burst 40 → internal/scim/ratelimit.go:15-18
- SCIM_RATE_LIMIT sets the rate; 0 or less disables limiting (nil limiter passes through) → internal/scim/ratelimit.go:38-46, internal/scim/ratelimit.go:90-92
- SCIM_RATE_BURST sets burst; default when unset or invalid → internal/scim/ratelimit.go:46, internal/scim/ratelimit.go:49-55
- Over-limit requests get 429 with Retry-After: 1 and SCIM error tooMany → internal/scim/ratelimit.go:103-104
- Each 429 logs one slog Warn, "rate limit exceeded", with key_id, tenant_id, method and path → internal/scim/ratelimit.go:97-102
- method is the verb when it is a standard one (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS), otherwise "other" → internal/scim/ratelimit.go:73-82
- path is truncated to maxLoggedPathLen (256) before logging → internal/scim/ratelimit.go:69-71, internal/scim/ratelimit.go:97-100
- The 429 path makes no store call, so it writes no audit row → internal/scim/ratelimit.go:96-105
- Sits after requireToken in the chain, so only authenticated callers consume budget → internal/scim/handler.go:93-98
- Bucket map is never pruned: one entry per distinct tenant and key seen → internal/scim/ratelimit.go:23-28, internal/scim/ratelimit.go:57-64
