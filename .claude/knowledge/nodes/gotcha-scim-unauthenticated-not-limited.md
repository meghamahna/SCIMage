---
id: gotcha-scim-unauthenticated-not-limited
kind: gotcha
title: Unauthenticated requests are not rate limited
summary: The per-caller limiter sits behind auth, so a flood of bad tokens is bounded only by the cost of one token lookup per request
tags: [gotcha, rate limit, DoS, unauthenticated, brute force, token lookup, throttle, 429]
links: [related:component-scim-ratelimit, related:component-scim-auth, related:decision-scim-uniform-401]
sources: [internal/scim/ratelimit.go:68-72, internal/scim/handler.go:94-98, internal/scim/auth.go:84-93]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Limiter is after requireToken, so rejected requests never reach it → internal/scim/handler.go:94-98
- Each bad request still does one token lookup in the store with no limit → internal/scim/auth.go:84-93
- The code comment records this as a known trade-off → internal/scim/ratelimit.go:68-72
- Mitigation at the edge (reverse proxy or network limits) is outside this package → internal/scim/ratelimit.go:68-72
