---
id: gotcha-scim-unauthenticated-not-limited
kind: gotcha
title: Unauthenticated requests are not rate limited
summary: The per-caller limiter sits behind auth, so a flood of bad tokens is bounded only by the cost of one token lookup per request
tags: [gotcha, rate limit, DoS, unauthenticated, brute force, token lookup, throttle, 429]
links: [related:component-scim-ratelimit, related:component-scim-auth, related:decision-scim-uniform-401]
sources: [internal/scim/ratelimit.go:84-88, internal/scim/handler.go:93-98, internal/scim/auth.go:98-117]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- The limiter is after requireToken, so rejected requests never reach it → internal/scim/handler.go:93-98
- Each request with a bad or unknown token still does one token lookup in the store, with no limit → internal/scim/auth.go:109-117
- The code comment records this as a known trade-off → internal/scim/ratelimit.go:84-88
- Nothing in this package limits it; the limit has to come from the edge (proxy or network) → internal/scim/ratelimit.go:84-88
