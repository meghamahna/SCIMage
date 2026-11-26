---
id: component-scim-ratelimit
kind: module
title: SCIM per-caller rate limiter
summary: Token-bucket limiter keyed by tenant and key id, placed after auth; 429 tooMany on excess; env-tunable
tags: [rate limit, ratelimit, throttle, token bucket, 429, retry-after, dos, SCIM_RATE_LIMIT, burst]
links: [implements:module-scim, depends_on:component-scim-auth, related:gotcha-scim-unauthenticated-not-limited, related:api-scim-errors]
sources: [internal/scim/ratelimit.go:1-87, internal/scim/handler.go:94-98]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Token bucket per caller using golang.org/x/time/rate, one bucket per map key → internal/scim/ratelimit.go:1-12, internal/scim/ratelimit.go:22-27, internal/scim/ratelimit.go:56-70
- Key is TenantID + "/" + KeyID from the authenticated identity → internal/scim/ratelimit.go:80
- Defaults: 20 requests/s sustained, burst 40 → internal/scim/ratelimit.go:14-17
- SCIM_RATE_LIMIT sets the rate; a value of 0 or less disables limiting (nil limiter passes through) → internal/scim/ratelimit.go:37-46, internal/scim/ratelimit.go:73-76
- SCIM_RATE_BURST sets burst (falls back to default when unset or invalid) → internal/scim/ratelimit.go:45, internal/scim/ratelimit.go:48-54
- Over-limit requests get 429 with Retry-After: 1 and SCIM error tooMany → internal/scim/ratelimit.go:80-83
- Sits after requireToken in the chain, so only authenticated callers consume budget → internal/scim/handler.go:94-98
- Bucket map is never pruned; one entry per distinct tenant+key seen → internal/scim/ratelimit.go:56-62
- Doc comment on the struct says "keyed by token fingerprint"; the code keys by tenant and key id → internal/scim/ratelimit.go:19-21, internal/scim/ratelimit.go:80
